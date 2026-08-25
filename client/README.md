# @davi/nfc-agent-client

The client library for the Davi NFC Agent. Docs in
[docs/client/](../docs/client).

`src/` is the one implementation of the client protocol. `dist/` is the same
code built for a page with no build step, and is committed for that reason.

`nfc-device-client.js` is the other role — a phone or Node process acting as a
reader for the agent. Different protocol.

```bash
npm install
npm test        # vitest, against a fake WebSocket
npm run typecheck
npm run build   # regenerate dist/ — commit the result
```
