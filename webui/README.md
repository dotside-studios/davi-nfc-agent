# Control Center

The agent's web console. Vite + React + TypeScript, embedded into the Go binary
with `go:embed` and served at the agent's root.

```bash
make webui      # build; commit the resulting dist/
make webui-dev  # dev server on :5273, proxying /control and /ws to a real agent
```

`dist/` is committed on purpose — it keeps `go build .` working for anyone
without Node. Rebuild and commit it whenever `src/` changes.

See [docs/control-center.md](../docs/control-center.md) for what it does and how
its API is gated.
