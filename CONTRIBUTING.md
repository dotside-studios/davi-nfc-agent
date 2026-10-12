# Contributing

## Setting up

You need Go 1.25 or later. Node.js is needed only to rebuild the client library
or the Control Center frontend.

On Linux, install the PC/SC service to run the agent against a reader:

```bash
sudo apt install pcscd libpcsclite1
```

macOS and Windows need nothing extra. The agent loads the PC/SC library at
runtime and builds without cgo, so no C toolchain or `-dev` package is needed.
`libpcsclite-dev` is only for the `-tags cgopcsc` backend.

```bash
git clone https://github.com/dotside-studios/davi-nfc-agent.git
cd davi-nfc-agent
go build ./cmd/davi-nfc-agent
```

## Checks

Run these before opening a pull request. CI runs the same tests, lint and
client checks on every pull request to `master`, along with `govulncheck` and a
build of every release target:

```bash
go test -race -shuffle=on ./...
golangci-lint run
make test-nowebui     # the suite without the Control Center
make client-test      # the TypeScript client
```

`make lint` runs `go vet` in both builds and lists files `gofmt` would change.

`e2e/` builds an agent the way [Custom builds](docs/custom-builds.md) does, on a
real TLS listener, and drives it over the published protocols. Run it on its own
with `go test ./e2e/ -v` after changing anything an embedding program depends
on.

Tests that need a reader and a tag are behind the `hardware` build tag and are
never run by `go test ./...`. See [Hardware tests](docs/hardware-tests.md).

Without a reader, `go run ./cmd/davi-virtual-reader` runs a software reader on
the same supervisor the agent uses, and takes commands to tap, remove and write
tags. Type `help` at its prompt.

## Generated files

Three things are generated and committed, so building the agent needs no Node
and using the client library needs no build step. CI regenerates them and fails
if the tree differs.

| Path | Rebuild with | From |
|------|--------------|------|
| `client/src/session/wire.generated.ts` | `make types` | package `protocol` |
| `client/dist/` | `make client` | `client/src/` |
| `agent/console/frontend/dist/` | `make webui` | `agent/console/frontend/src/` |

Run `make types` after changing anything in `protocol/`. The error codes,
message types and payload shapes are declared once in Go and written out as
TypeScript by `cmd/sdkgen`, which keeps the client library in step with the
agent. What the library adds on top is hand-written in
`client/src/session/types.ts`. `make types` needs only Go.

Working on the Control Center itself, including its dev server and the
screenshot harness, is covered in [agent/console](agent/console/README.md).

## Cross-compiling

`scripts/build.sh` builds for the current platform, or for the one named:

```bash
./scripts/build.sh
./scripts/build.sh linux arm64
BUILD_VERSION=1.0.0 ./scripts/build.sh darwin arm64
```

The targets are `linux`, `darwin` and `windows` on `amd64` and `arm64`. The
binary is written to the current directory as `davi-nfc-agent-<os>-<arch>`,
with `.exe` on Windows.

On Windows, `scripts/build.ps1` (PowerShell 5.1+) builds Windows targets. Use
WSL and `build.sh` to build for Linux or macOS from Windows.

## Releases

Pushing a tag of the form `v1.2.3` runs `.github/workflows/release.yml`, which
builds every platform with `BUILD_VERSION` set to the tag, smoke-tests each
binary and publishes a GitHub release with the archives.

```bash
git tag v1.6.0
git push origin v1.6.0
```

Record user-visible changes in [CHANGELOG.md](CHANGELOG.md) in the same pull
request as the change.

## Project layout

```
davi-nfc-agent/
├── cmd/
│   ├── davi-nfc-agent/       The shipped binary: flags, NFC backends, plugins
│   ├── davi-virtual-reader/  A software reader for development
│   └── sdkgen/               Writes the client library's wire types
├── agent/                    The agent: lifecycle, preferences, plugins
│   ├── serverplugin/         The listener and what is served from it
│   ├── pairingplugin/        The pairing listener and its tray entries
│   ├── trustplugin/          Installing the local certificate authority
│   ├── console/              The Control Center (-tags nowebui drops it)
│   └── tray/                 The system tray
├── nfc/                      Readers, tags and the supervisor over them
│   ├── pcsc/                 PC/SC readers
│   ├── remotenfc/            Phones and other devices, and their endpoint
│   ├── multimanager/         Several backends as one
│   ├── virtualnfc/           Software tags
│   ├── nfctest/              Tag emulators for tests
│   ├── keyfile/              Loading the card key file
│   └── ntag424/, ev1/, ev2/, lrp/  NTAG 424 DNA and DESFire cryptography
├── server/                   Credential checks and the client/device split
│   ├── listener/             One HTTP listener: port, mux, TLS, mDNS
│   ├── clientserver/         Client connections and the tag operations they ask for
│   └── netinfo/, deviceid/, wsconn/
├── protocol/                 The wire types both endpoints share
├── secure/                   Restricting files to the current user
│   ├── tls/                  Certificates, the local CA, the setup listener
│   └── pairing/              Device pairing and tokens
├── traymenu/                 Declarative tray menus; see traymenu/README.md
├── client/                   The TypeScript client library
├── e2e/, hwtest/             End-to-end and hardware tests
└── docs/
```

The NFC layer is described in [nfc/README.md](nfc/README.md), and adding a
reader or tag type in [Extending NFC support](docs/extending-nfc-support.md).

## Pull requests

- Branch from `master`, and keep one change per pull request.
- Write commit messages as [Conventional Commits](https://www.conventionalcommits.org/):

  ```
  feat(ntag424): give NTAG 424 operations on a phone an overall deadline
  fix(server): refuse a write when the named tag is not present
  docs: split the protocol reference by endpoint
  ```

- Add tests for new behaviour, and update the docs a change affects.

## Reporting a bug

Include the operating system, the reader model, the tag type, what you did,
what you expected, and the log. The Control Center's **Activity** tab downloads
the log as text.
