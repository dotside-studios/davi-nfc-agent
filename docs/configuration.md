# Configuration reference

The shipped agent's flags, environment variables, ports and files.

## Flags

| Flag | Default | Environment | Description |
|------|---------|-------------|-------------|
| `-device-port` | `9470` | | The agent port: clients, devices, pairing, the Control Center and the health check |
| `-bootstrap-port` | `9472` | | The cleartext setup port, serving the phone setup page and the local CA. `0` turns it off, and with it the pairing QR in the log |
| `-device` | | | The PC/SC reader to use, by name, such as `"ACS ACR122U"`. Detected when empty |
| `-api-secret` | generated | | The API secret. When empty, the agent uses the one in `api-secret.txt`, creating it on first start |
| `-allowed-origins` | | `DAVI_NFC_ALLOWED_ORIGINS` | Comma-separated browser origins to allow, as `host:port` or full URLs. Saved to `allowed-origins.json`. `*` turns the origin check off until the agent exits |
| `-require-paired-devices` | `false` | `DAVI_NFC_REQUIRE_PAIRED_DEVICES=1` | Admit only devices that have paired. The shared API secret and the loopback bypass stop admitting devices. Clients are unaffected |
| `-allow-loopback-bypass` | `false` | `DAVI_NFC_ALLOW_LOOPBACK_BYPASS=1` | Admit connections from this host without a credential. See [Security](security.md#the-loopback-bypass) |
| `-allow-raw-apdu` | `false` | `DAVI_NFC_ALLOW_RAW_APDU=1` | Open the raw APDU channel, so clients can send `transceiveRequest` and `transceiveSequenceRequest` |
| `-keys` | | `DAVI_NFC_KEYS` | A JSON key file for MIFARE Classic, DESFire and NTAG 424 DNA. See [Card keys](card-keys.md). The flag wins when both are set |
| `-auto-tls` | `true` | | Generate and manage a self-signed certificate. If that fails, the agent logs a warning and serves cleartext |
| `-cert`, `-key` | | | Serve this certificate and key instead |
| `-install-ca` | `false` | | Create a local certificate authority and install it in the system trust store, so browsers trust the agent. See [Connect from a browser](browser-access.md) |
| `-config-dir` | per OS | | The config directory |
| `-version` | | | Print the version and exit |

A boolean environment variable takes effect when set to `1`.
`DAVI_NFC_APDU_TRACE=1` has no flag: it logs every APDU sent to a reader and its
reply.

Settings changed from the tray or the Control Center while the agent runs, such
as the selected reader, read-only mode or the raw APDU channel, last until it
exits. Use the flags for what it should start with.

## Signals

On Linux and macOS, `SIGHUP` reloads the key file given with `-keys`.

## Ports

| Port | Scheme | Serves |
|------|--------|--------|
| 9470 | HTTPS / WSS | `/ws` (clients), `/ws?mode=device` (devices), `/pair`, `/health`, `/api/v1/health`, and the Control Center at `/` |
| 9472 | HTTP | The phone setup page, `/ca.pem` and `/ca.crt` (PIN-gated) |

The agent also advertises port 9470 over mDNS as `_nfc-device._tcp`. See
[Device API](device-api.md#discovery).

## Files

The config directory is `davi-nfc-agent` inside the OS's user config directory:

| OS | Config directory |
|----|------------------|
| Linux | `~/.config/davi-nfc-agent` |
| macOS | `~/Library/Application Support/davi-nfc-agent` |
| Windows | `%AppData%\davi-nfc-agent` |

| File | Holds |
|------|-------|
| `api-secret.txt` | The API secret. Copy it from the tray with **Copy API Secret** |
| `allowed-origins.json` | The browser origin allowlist, as a JSON array of `host:port` strings. Created with `davi.social`, `shop.davi.social`, `localhost:3000` and `localhost:3002` |
| `paired-devices.json` | Paired devices, with token hashes |
| `tls/server.crt`, `tls/server.key` | The certificate the agent serves, reissued when the host's addresses change |
| `tls/agent.key` | The long-lived key the certificate is issued for. Its pin is what devices store. Deleting it means every device pairs again |
| `tls/hosts.txt` | The names and addresses the certificate covers |
| `ca/rootCA.pem`, `ca/rootCA-key.pem` | The local certificate authority, with `-install-ca` |

The agent restricts these files to the account it runs as.
