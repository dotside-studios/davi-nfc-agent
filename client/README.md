# @davi/nfc-agent-client

The client library for the Davi NFC Agent. Documented in
[docs/client/](../docs/client) — tutorial, how-to guides and reference.

`src/` is the one implementation of the client protocol. The agent's own
control center (`webui/frontend`) imports it, so a change that breaks a
consumer breaks a build in this repository. `dist/` is the same code built for
a page with no build step.

`nfc-device-client.js` is a separate library for the other role — a phone or
Node process acting as a reader for the agent. It speaks the device protocol,
not this one.

## Working on it

```bash
npm install
npm test        # vitest, against a fake WebSocket
npm run typecheck
npm run build   # regenerate dist/ — commit the result
```

`dist/` is committed for the same reason `webui/frontend/dist` is: consuming
this library should not require Node.
