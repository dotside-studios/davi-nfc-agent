# Setting up an iOS or Android device

How a native app pairs with the agent and connects to it. For the message
protocol itself see the [Device API](api.md#device-api).

## Do devices need TLS?

Devices do not need a certificate authority, and native apps should not install
one. A CA in a trust store can sign for any name, not just this agent, and on
iOS granting Full Trust is a system-wide decision.

TLS itself stays on. The agent serves `wss://` with a self-signed
certificate, and your app verifies it by **pinning the agent's public key**
rather than by chain of trust. The pin is handed to you at pairing and does not
change when the agent's certificate is reissued.

So the setup cost moved off the user and onto the app: no profile to install, a
few lines of trust-evaluation code instead.

You *can* run the agent without TLS (`-auto-tls=false`) and connect over `ws://`,
but see [Running without TLS](#running-without-tls): both platforms block
cleartext by default, and the traffic carries card UIDs and tag contents across
whatever network the device is on.

## 1. Pair

Pairing exchanges the PIN shown on the kiosk for a credential belonging to this
device. Do it once.

### Read the pairing QR off the kiosk screen

The agent prints a QR at startup, next to the PIN, encoding:

```
davi-pair://<agent-host>:9470/?spki=sha256%2F47DE…&code=123456&name=Davi%20NFC%20Agent
```

`spki` is the agent's public key pin and `code` is the pairing PIN. Read this
off the screen. A QR fetched over the network carries no more authority than the
connection that served it, and the pin is the value that authenticates that
connection.

### Post to /pair over TLS pinned to `spki`

Pairing is served from the agent's own port, which serves the certificate
`spki` covers. Pin the connection to `spki` before sending anything.

```
POST https://<agent-host>:9470/pair?pin=123456
Content-Type: application/json

{"deviceName": "Warehouse iPhone", "platform": "ios"}
```

Pairing over a cleartext connection is refused (`426 Upgrade Required`) from
anything but loopback: the response carries a durable token and the key pin, so
issuing it in the clear hands both to an observer and lets an active attacker
substitute a pin of their own.

```json
{
  "deviceID": "6f1c…",
  "deviceToken": "kQ8x…",
  "publicKeyPin": "sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
  "agentPort": 9470
}
```

Store `deviceToken` and `publicKeyPin` in the keychain / keystore. **The token
is shown once**, since the agent keeps only a hash of it, so losing it means
pairing again.

`publicKeyPin` in the response repeats the `spki` the QR carried. They must
match; a mismatch means the QR did not come from this agent.

The PIN is on the kiosk: system tray, agent logs, and the pairing page at
`http://<agent-host>:9472`. Five wrong attempts lock pairing until the agent
restarts.

Port 9472 is the cleartext bootstrap listener. It serves the setup page and
hands out the local certificate authority to a device that does not trust the
agent's certificate yet, which is why it is not TLS. Pairing is not served from
it.

## 2. Connect

```
wss://<agent-host>:9470/ws?mode=device
```

Present the token as `Authorization: Bearer <deviceToken>`, or `?secret=` if
your WebSocket client cannot set headers. Offer the `davi-nfc-device.v1`
subprotocol, then send `hello`. See
[Device Registration](api.md#device-registration).

## 3. Verify the agent by its pin

This is the part that replaces the CA. On each connection, compare the SHA-256
of the server certificate's SubjectPublicKeyInfo against the stored
`publicKeyPin`. Refuse the connection when it differs.

### Android

**Do not use OkHttp's `CertificatePinner` for this.** It runs *after* the trust
manager has validated the chain, so a self-signed certificate is rejected during
the handshake before pinning is ever consulted, so the pin appears to be
ignored.
This is [documented OkHttp behavior](https://square.github.io/okhttp/5.x/okhttp/okhttp3/-certificate-pinner/index.html),
not a bug, and it is the most common way this goes wrong.

Supply a custom `X509TrustManager` that accepts the certificate when its SPKI
hash matches, and rejects it otherwise:

```kotlin
class PinnedTrustManager(private val expectedPin: String) : X509TrustManager {
    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
        val spki = chain.first().publicKey.encoded          // already SPKI DER
        val digest = MessageDigest.getInstance("SHA-256").digest(spki)
        val pin = "sha256/" + Base64.encodeToString(digest, Base64.NO_WRAP)
        if (pin != expectedPin) throw CertificateException("agent key pin mismatch")
    }

    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) = Unit
    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
}
```

`PublicKey.getEncoded()` returns SPKI DER directly, which the agent
hashes, with no reassembly needed.

Pair it with a hostname verifier appropriate to how you address the agent; a
self-signed certificate carries the agent's hostnames and LAN IPs as SANs, so
standard verification works if you connect by one of those.

### iOS

Handle the server-trust challenge in `URLSessionDelegate` and compare the pin
yourself:

```swift
func urlSession(_ session: URLSession,
                didReceive challenge: URLAuthenticationChallenge,
                completionHandler: @escaping (URLSession.AuthChallengeDisposition, URLCredential?) -> Void) {
    guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust,
          let trust = challenge.protectionSpace.serverTrust,
          pinMatches(trust, expected: storedPin)
    else { return completionHandler(.cancelAuthenticationChallenge, nil) }

    completionHandler(.useCredential, URLCredential(trust: trust))
}
```

**Watch the key encoding.** `SecKeyCopyExternalRepresentation` returns the *raw*
key, not SPKI DER, so hashing it directly produces a value that will never match.
Prepend the ASN.1 SubjectPublicKeyInfo header for the key's algorithm before
hashing. The agent uses ECDSA P-256, whose header is a fixed 26-byte constant.
Getting this wrong is the iOS equivalent of the `CertificatePinner` trap: the
code looks right and every connection fails.

## Local network permission (iOS 14+)

Any connection to a LAN address needs the local network permission, whether or
not you use TLS. Add `NSLocalNetworkUsageDescription` to `Info.plist`, and
expect the system prompt on first use. Without it the connection fails with no
useful error.

If you discover the agent via mDNS, also declare `NSBonjourServices` with
`_nfc-device._tcp`.

## Running without TLS

`-auto-tls=false` makes the agent serve `ws://`. Both platforms block cleartext
by default, so this needs a declaration either way:

- **iOS**: App Transport Security blocks it. `NSAllowsLocalNetworking` covers
  `.local` names and private IP ranges, which is the narrow way to allow it.
- **Android**: cleartext has been off by default since API 28. Add a
  `network_security_config.xml` permitting it for the agent's address only,
  never `cleartextTrafficPermitted="true"` globally.

Consider what is on the wire before choosing this: tag UIDs and NDEF contents,
readable by anything on the same network. It is reasonable for a wired bench
setup, and a poor default for a shop floor on shared WiFi.

## Requiring pairing

Once your devices pair, the agent can be set to admit nothing else. See
[Requiring pairing](api.md#requiring-pairing). Until then the shared API secret
still works, so an unpaired device is not locked out. A device connecting over
loopback presents a credential too, unless the agent was started with
`-allow-loopback-bypass`. See [The loopback bypass](api.md#the-loopback-bypass).

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Handshake fails immediately, no error detail | Pin mismatch, or `CertificatePinner` used instead of a trust manager (Android) |
| Every connection fails despite a correct-looking pin | Raw key hashed instead of SPKI (iOS) |
| Connection fails only on a real device, works in a simulator | Local network permission not declared or not granted |
| `401 Unauthorized` on upgrade | Token missing, or revoked from the tray |
| Worked yesterday, fails after the host moved network | The certificate was reissued, which is expected, and the pin should still match. If it does not, the agent's key was regenerated, so pair again |
