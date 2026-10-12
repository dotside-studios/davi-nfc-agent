# Control Center

The web console the agent serves, for an operator managing it and for tag
operations beyond what an application offers.

## Opening it

Choose **Open Control Center** from the tray menu. That mints a single-use token,
opens your browser at `https://localhost:9470/control/session?token=…`, and
exchanges the token for a session cookie.

The token is spent on first use and expires after two minutes, so a URL that
ends up in shell history or browser autocomplete is already useless. Sessions
last 12 hours.

There is no way in other than the tray.

> **On a self-signed certificate**, the browser shows its usual warning the
> first time and you click through. This is the one browser client that works on
> a fresh install without `-install-ca`: a page visit can be accepted manually,
> whereas a bare `wss://` connection to an untrusted certificate fails outright.

## Who can open it

The console is privileged. It rotates the API secret, revokes device
credentials, edits the origin allowlist and can lock a tag irreversibly. Three
checks must all pass on every control request:

| Check | Refuses |
|---|---|
| Loopback only | Administering the agent from another host |
| `Origin`/`Referer` is the agent itself | A page you happen to be visiting driving the console via your cookie |
| A tray-minted session cookie | Another local account or process pointing at the port |

The loopback check uses the connection's address, never `X-Forwarded-For`, so a
proxy on this machine does not make a remote request look local.

The origin allowlist is not consulted. An allowed origin may read and write
tags; it never administers the agent. The control routes are also served without the
permissive CORS headers the client endpoints carry.

Use **Security → sign out all** to end every console session at once, including
your own.

## Tabs

**Overview**: the page an operator works from, split a third to two thirds.

The left third holds state and the knobs that get turned: start/stop, reader
selection, mode, card-type filter, port and quit, plus the server URLs
and certificate summary needed to decide whether to turn any of them.

The right two thirds hold what the reader is doing: the tag on it now with its
capabilities and record list, the tail of the log, and every distinct tag seen
this session with a scan count and when it was last presented. That history is
in the console only; the agent does not persist it.

**Tag**: inspector and NDEF composer.

- Reads: UID, type, technology, the record tree, and the capabilities the agent
  determined for that tag (memory, usable capacity, writable, lockable,
  password support).
- Writes: every record type the agent supports (text, URI, smart poster,
  vCard, MIME, geo, tel/sms/mailto, Android Application Records and fully raw
  records), with a live size estimate against the tag's actual capacity.
- Also erase, and lock (irreversible, and gated behind typing `lock`).
- A **raw exchange (APDU)** console: hex in, hex and ASCII out, with ISO 7816
  status words decoded, per-exchange timing, presets for common commands, and a toggle between APDU-level and
  framing-level exchange. Gated behind the **raw APDU channel**, a toggle under
  the reader controls that is off by default: a raw command reaches the tag
  unmodified and can lock or brick it, so the channel that carries one stays
  closed until an operator opens it. Also refused in read-only mode, since the
  agent cannot tell a `SELECT` from a write to a configuration page.
- An **NTAG 424 DNA** panel for a tag the reader or a phone holds (a tag not
  reported as one can be driven anyway on request): the real UID, key versions,
  originality signature and NDEF file settings; an SDM form whose layout preview
  is computed by the agent (`ntag424Request` op `planSDM`, which touches no
  tag), showing the URL with each mirror marked, the span the MAC covers and the
  offsets; and Configure SDM. Replacing a key and locking the NDEF file confirm
  in the page, by typing a phrase, before the request carries `confirm: true`.
  The agent runs the authenticated session with the keys it holds, which never
  reach the page; the one key that does is the one an operator types for an
  explicit key change, sent once and cleared.

The console writes over the ordinary client endpoint, exactly as an application
would, so there is one implementation of the write path.

**Activity**: tag events on the left, agent log on the right, both filterable
and both filling the window. They are read together: a failed write and the line
explaining it arrive at the same moment. Events export as NDJSON,
the log downloads as text.

**Security**: who may reach this agent and with what credential. One page with
section links down the side that scroll to each part; everything stays on screen
so it can be read straight through. Each section is linkable:
`#/security/origins`, `#/security/certificate` and so on.

| Section | Holds |
|---|---|
| Connected clients | Every application on the client endpoint: origin, address, how long it has been connected, how many writes and locks it has issued, and a disconnect |
| Paired devices | Platform, pairing time, last seen, whether connected now; revoke one or all; the paired-device policy |
| Browser origins | The allowlist, every origin refused since startup with a one-click **allow**, and the session override |
| Credentials | API secret, pairing PIN, open console sessions, and the rotations for each |
| Device trust | Public key pin, local CA status and fingerprint, and **Trust this agent in browsers** when no CA is installed |
| Certificate | Expiry, issuer, fingerprint, the names it covers, and a warning when the agent is reachable on an address it omits |

The reader and server settings are not here; they live on
Overview, because those get turned while working and these do not.

## Logs

The agent logs through the standard library logger, which writes to stderr.
Started from a desktop launcher there is no stderr to read, so a certificate
warning, refused origin or reader failure would be discarded as it was
produced.

The agent keeps the last 5000 lines in memory. They are visible under
**Activity**, streamed live, and downloadable as text for a bug report. Severity
is inferred from the message text, which is good enough to filter on but not to
rely on otherwise.

The buffer is in memory only, and is lost when the agent exits.

## Browser origins

**Security → Browser origins** lists the allowlist, every origin refused since
the agent started with a one-click **allow**, and **Allow any origin (this
session)**, which turns the check off until the agent exits. Setting up a web
page to connect is covered in [Connect from a browser](browser-access.md).

## Preferences

The console and the tray change the same settings in the agent, and both show
what the agent is set to, so they never disagree. A change lasts until the
agent exits. To start the agent with a setting, use its flag; see
[Configuration](configuration.md). The origin allowlist and paired devices are
saved to files and survive a restart.
