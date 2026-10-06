# hwtest

Tests that need a real reader and tag, for issues
[#96](https://github.com/dotside-studios/davi-nfc-agent/issues/96) (raw framing
on PC/SC readers) and
[#99](https://github.com/dotside-studios/davi-nfc-agent/issues/99) (NTAG 424 DNA
in LRP mode). Commands, setup and what to send back are in
[docs/hardware-tests.md](../docs/hardware-tests.md).

```bash
go test -tags hardware -v -count=1 -run TestRawFraming ./hwtest
go test -tags hardware -v -count=1 -run TestLRP ./hwtest
```

They are behind the `hardware` build tag. Plain `go test ./...` skips them;
`go vet -tags hardware ./...` and `golangci-lint run --build-tags hardware`
check them.

## Environment

| Variable | Meaning |
|----------|---------|
| `DAVI_HW_READER` | PC/SC reader name substring (default: the first reader) |
| `DAVI_HW_OUT` | Output directory for fixtures (default `hwtest-out/` at the repository root, git-ignored) |
| `DAVI_HW_KEYS` | Key file in the format of `docs/card-keys.md`, loaded by `nfc/keyfile` with its permission checks |
| `DAVI_HW_ENABLE_LRP_UID` | UID of the one tag to switch to LRP, permanently. Any other tag is never switched |
| `DAVI_HW_INCLUDE_KEYS` | `1` to store the keys in the LRP fixture, which a replay needs. Spare tags only |
| `DAVI_HW_KEEP_SDM` | `1` to leave the SDM configuration on the tag |

## Layout

| Path | Holds |
|------|-------|
| `*_hardware_test.go` | The tagged tests: environment, reader selection, fixture output. Thin wrappers |
| `scenario/` | The procedures, `RunRawFraming` and `RunLRP`, over an `nfc.Supervisor`. Also run against emulators in the normal suite |
| `fixture/` | The fixture format, the recorder, the LRP replay and the loaders. No hardware dependency |
| `fixture/testdata/` | Committed fixtures. The ones there are from emulators; add captures from hardware beside them |

The tests install `pcsc.SetObserver` before the supervisor starts, so the
recording is the bytes the real driver sent. Nothing in the agent sets an
observer.
