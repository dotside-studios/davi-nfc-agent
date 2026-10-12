# Davi NFC Agent documentation

## Start here

- [Getting started](getting-started.md): run the agent, read a tag and write
  to it from a short script

## Guides

- [Installation](installation.md): install the agent, set up the reader, and
  troubleshoot it
- [Connect from a browser](browser-access.md): let a web page reach the agent
- [Pair a phone](device-setup.md): make an iOS or Android app a reader for the
  agent
- [Card keys](card-keys.md): give the agent keys for MIFARE Classic, DESFire
  and NTAG 424 DNA
- [Control Center](control-center.md): the agent's web console

## Reference

- [Client API](client-api.md): the WebSocket protocol applications speak
- [Device API](device-api.md): the WebSocket protocol phones and readers speak
- [Errors](errors.md): every error code
- [Configuration](configuration.md): flags, environment variables, ports and
  files
- [JavaScript client](javascript-client.md): `NFCClient`, `NFCDeviceClient`
  and the React hook

## Background

- [How the agent is secured](security.md): credentials, origins, TLS and the
  raw channel

## Building on the agent in Go

- [Custom builds](custom-builds.md): embed the agent and assemble your own
  binary
- [Extending NFC support](extending-nfc-support.md): add a reader backend or a
  tag type
- [Hardware tests](hardware-tests.md): run the tests that need a real reader
