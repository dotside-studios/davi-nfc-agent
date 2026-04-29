# Contributing

Thanks for your interest in contributing to the NFC Agent!

## Development Setup

### Requirements

- Go 1.24 or later
- PC/SC library (for running with hardware)

### Installing Dependencies

**Linux:**
```bash
sudo apt install pcscd libpcsclite-dev
```

No additional dependencies needed for **macOS** and **Windows**.

### Building

```bash
git clone https://github.com/dotside-studios/davi-nfc-agent.git
cd davi-nfc-agent
go build .
```

### Running Tests

```bash
go test ./...
```

## Advanced Building

### Cross-Platform Builds

**macOS / Linux / WSL** (`scripts/build.sh`):
```bash
# Build for current platform
./scripts/build.sh

# Build for specific platform
./scripts/build.sh linux amd64
./scripts/build.sh linux arm64
./scripts/build.sh darwin amd64
./scripts/build.sh darwin arm64
./scripts/build.sh windows amd64
```

**Windows** (`scripts/build.ps1`, PowerShell 5.1+):
```powershell
# Build for current platform (windows/amd64 by default)
./scripts/build.ps1

# Build for a specific Windows arch
./scripts/build.ps1 windows arm64
```

Cross-compilation _from Windows_ to Linux/macOS is not supported by
`build.ps1` (needs mingw/clang); use WSL + `build.sh` for that case.

### Build with Version Info

```bash
BUILD_VERSION=1.0.0 ./scripts/build.sh
```

### Build Artifacts

Binaries are created in the current directory:

- `davi-nfc-agent-linux-amd64`
- `davi-nfc-agent-linux-arm64`
- `davi-nfc-agent-darwin-amd64`
- `davi-nfc-agent-darwin-arm64`
- `davi-nfc-agent-windows-amd64.exe`

### CI/CD

The GitHub Actions workflow (`.github/workflows/build.yml`) automatically:

1. Builds for all platforms on push to master
2. Creates releases with binaries
3. Sets version info from git tags

**Manual Release:**

```bash
# Tag a release
git tag v1.0.0
git push origin v1.0.0

# CI will build with BUILD_VERSION=v1.0.0
```

## Project Architecture

```
davi-nfc-agent/
├── main.go              # Entry point, CLI flags
├── agent.go             # Core agent logic
├── systray.go           # System tray UI
├── buildinfo/           # Version and build metadata
├── nfc/                 # NFC abstraction layer
│   ├── manager.go       # Device manager interface
│   ├── device.go        # Device interface
│   ├── tag.go           # Tag interface
│   ├── reader.go        # NFC reader implementation
│   ├── remotenfc/       # Smartphone NFC support
│   └── multimanager/    # Multiple manager aggregation
├── server/              # WebSocket servers
│   ├── deviceserver/    # Device server (port 9470)
│   └── clientserver/    # Client server (port 9471)
├── tls/                 # Auto-TLS certificate management
├── protocol/            # Protocol definitions
├── client/              # JavaScript client library
├── scripts/             # Build scripts
└── docs/                # Documentation
```

### Key Components

**NFC Layer** (`nfc/`)
- Abstraction over PC/SC (ebfe/scard)
- Modular design supporting hardware and smartphone readers
- See [nfc/README.md](nfc/README.md) for details

**Server Layer** (`server/`)
- Two-server architecture:
  - **DeviceServer**: Handles NFC devices and hardware readers
  - **ClientServer**: Handles client applications
- Bridge component for inter-server communication

**TLS Layer** (`tls/`)
- Automatic certificate generation
- Network change detection
- CA bootstrap server for device setup

## Code Style

- Follow standard Go conventions
- Run `go fmt` before committing
- Use meaningful variable and function names
- Add comments for exported functions

## Submitting Changes

### Pull Request Process

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/my-feature`
3. Make your changes
4. Run tests: `go test ./...`
5. Commit with clear message: `git commit -m "Add feature X"`
6. Push to your fork: `git push origin feature/my-feature`
7. Open a Pull Request

### Commit Messages

Use clear, descriptive commit messages:

```
feat: add support for DESFire EV3 cards
fix: handle connection timeout gracefully
docs: update API documentation
refactor: simplify device manager interface
```

### What to Include

- Clear description of changes
- Tests for new functionality
- Documentation updates if needed
- Update CHANGELOG if applicable

## Adding NFC Support

To add support for new NFC readers or tag types, see [docs/extending-nfc-support.md](docs/extending-nfc-support.md).

## Reporting Issues

When reporting bugs, please include:

- Operating system and version
- NFC reader model
- Steps to reproduce
- Expected vs actual behavior
- Relevant log output

## Questions?

Open an issue for questions or discussions.
