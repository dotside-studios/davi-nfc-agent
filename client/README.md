# @davi/nfc-agent-client

The client library for the Davi NFC Agent. Docs in
[docs/client/](../docs/client).

`dist/` is generated from `src/` — don't hand-edit it.

`nfc-device-client.js` is the other role: a phone or Node process acting as a
reader for the agent, on a different protocol.

```bash
npm install
npm test        # vitest, against a fake WebSocket
npm run typecheck
npm run build   # regenerate dist/, and commit it
```
