# Client library

`@davi/nfc-agent-client` — for an application that consumes tags from the agent
and writes to them. Source in [`client/`](../../client).

| | |
|---|---|
| [Tutorial](tutorial.md) | Read and write your first tag. Start here |
| [How-to guides](how-to.md) | Recipes: record kinds, locking, raw APDUs, React, error handling |
| [Reference](reference.md) | Every class, method, event, option and type |

Related:

- [api.md](../api.md) — the protocol underneath this library
- [javascript-client.md](../javascript-client.md) — `NFCDeviceClient`, for a
  phone or Node process acting as a reader *for* the agent. The two roles share
  the agent's port and nothing else
- [Connecting from a web console](../../README.md#connecting-from-a-web-console)
  — the origin allowlist a hosted page needs
