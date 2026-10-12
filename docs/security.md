# How the agent is secured

The agent drives a card reader, and a reader can do things that cannot be
undone: a lock makes a tag read-only for good, and a raw command can burn its
one-time bits. Anyone who can reach the agent's port can ask for those things,
so every way in carries a check of its own. This page explains what each check
is for and how they fit together. The flags behind them are in
[Configuration](configuration.md).

## Who is asking: credentials

Every connection to the agent port presents a credential, and there are two
kinds.

The **API secret** is one value shared by every client application. The agent
creates it on first start, so there is no unauthenticated default to forget to
change. Rotating it from the tray locks out everything that held the old one,
which is why it suits a handful of applications on one machine rather than a
fleet of phones.

A **device token** belongs to one paired device. Pairing trades the PIN shown
on the kiosk for a token issued to that device alone, so revoking one phone
leaves the others connected. The agent stores only a hash of each token. The
PIN is short, so pairing stops answering after five wrong attempts, and pairing
is refused over cleartext from anywhere but this host: the answer carries a
token and the pin, and an observer on the network could take the first and an
active attacker could substitute the second.

Devices may present the API secret as well, so that existing setups keep
working. `-require-paired-devices` withdraws that, and is meant to be turned on
once the devices that matter have paired.

### The loopback bypass

A connection from the agent's own machine presents a credential like any other.
Loopback identifies the machine, not the person: every account on it, every
local proxy and every port forwarded into it would arrive as loopback too.
`-allow-loopback-bypass` admits them all without a credential, for a local
program that cannot be given the secret. It covers the shared secret only;
under `-require-paired-devices` a device still needs its token.

## Which page is asking: origins

A credential says which program is connecting. In a browser that is not
enough, because the page doing the connecting may not be the one the operator
meant to trust: any site open in a tab can try to open a WebSocket to
`localhost`. The agent therefore admits a browser connection only when its
`Origin` is on an allowlist, and the allowlist starts with the Davi consoles
on it.

A refused origin is offered in the tray as **Allow example.com**, so admitting
a new console is one click. **Allow any origin (this session)** turns the check off until the
agent exits, and while it is off any page the operator opens can write and lock
cards.

A program that is not a browser sends no `Origin`, and is not checked.

## Whether the connection is to the agent: TLS

The agent serves TLS by default, with a certificate it issues itself. A
self-signed certificate proves nothing to a client that has not met this agent
before, so the two kinds of client establish trust differently.

**Devices pin the key.** The certificate is issued for a key the agent creates
once and keeps. Pairing hands the device a hash of that key, and from then on
the device accepts the agent only if the key matches. The certificate can be
reissued freely, as it is whenever the machine's addresses change, and the pin
still holds. Nothing is installed on the phone.

**Browsers need a certificate they already trust,** because a page cannot pin a
key. Either the operator provides a certificate from a public authority for a
name that resolves to the agent, or `-install-ca` creates a local certificate
authority and installs it in the system trust store. The second is convenient
and broader than it looks: an authority in the trust store can vouch for any
name, not only this agent, so a stolen CA key could impersonate any site to
that machine. A provided certificate avoids that.

Running without TLS is possible, and puts tag identifiers and contents on the
network in the clear.

## What a request may do: the raw channel and read-only mode

Ordinary requests are ones the agent understands: it encodes a write itself,
checks it fits, and confirms it. A raw exchange is bytes the agent passes to the
tag unread, and it cannot tell a harmless `SELECT` from a write to a
configuration page. So the raw APDU channel is closed until an operator opens
it, and it stays refused while the agent is in read-only mode.

Read-only mode refuses everything that changes a tag (writes, locks, raw
exchanges, the NTAG 424 DNA operations that change the tag) on every route, a
phone's tag included. Capabilities sent to clients reflect it, so an
application never offers an action the agent would refuse.

## Card keys

Keys for MIFARE Classic, DESFire and NTAG 424 DNA live in the agent's memory,
loaded from a file only the agent's account can read. They never appear in an
answer, a log line or the Control Center. A client asks for an operation, and
the agent runs the authenticated session itself. See [Card keys](card-keys.md).

## The Control Center

The console can rotate the secret, revoke devices, change the allowlist and lock
tags, so it is held to more than any client. A request to it must come from
this machine, from a page the agent served itself, and carry a session cookie
that only the tray can start. The origin allowlist does not apply: an allowed
origin may read and write tags, and never administers the agent. See
[Control Center](control-center.md#who-can-open-it).
