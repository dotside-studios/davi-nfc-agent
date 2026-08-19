# Pure-Go PC/SC — Prior Art and Feasibility

Research notes on getting rid of the cgo dependency in the reader path. The
question this answers: does a pure-Go PC/SC stack already exist, and if not,
what would it cost us to write one?

## 1. What cgo costs us today

`nfc/manager_pcsc.go` and `nfc/device_pcsc.go` go through
[`github.com/ebfe/scard`](https://github.com/ebfe/scard), which is a cgo
binding. Per platform:

| Platform | `ebfe/scard` implementation | cgo? |
|---|---|---|
| Windows | `scard_windows.go`, `syscall.NewLazyDLL("winscard.dll")` | **no** |
| Linux | `scard_unix.go`, `#cgo pkg-config: libpcsclite` | yes |
| macOS | `scard_darwin.go`, `#cgo LDFLAGS: -framework PCSC` | yes |

So Windows is already cgo-free; only the unix paths pull in C. Measured on this
tree:

```
$ CGO_ENABLED=0 go build ./nfc/
# github.com/ebfe/scard
scard.go:35:12: undefined: scardEstablishContext
...
```

The bill this leaves in `.github/workflows/build.yml`:

- `libpcsclite-dev` must be installed on the Linux runners.
- linux/arm64 needs a full cross toolchain — `gcc-aarch64-linux-gnu`,
  `dpkg --add-architecture arm64`, a hand-written `ports.ubuntu.com` apt source,
  and `libpcsclite-dev:arm64`. That is the single largest block in the build
  workflow.
- `CC` juggling, and no `CGO_ENABLED=0` static builds anywhere.
- Cross-compiling darwin from a non-darwin runner is impossible, so the macOS
  jobs are pinned to macOS runners.

The API surface we actually use is small — `EstablishContext`, `ListReaders`,
`GetStatusChange`, `Connect`, `Transmit`, `Status`, `Disconnect`, `Release`,
plus `ErrCancelled`/`ErrRemovedCard`/`ErrResetCard`/`ErrNoSmartcard`/
`ErrUnpoweredCard`. No transactions, no `Control`, no attributes.

## 2. Three different things "pure Go" can mean

Worth separating before looking at prior art, because the three have wildly
different costs:

1. **cgo-free client** — call the platform's existing PC/SC library
   (`winscard.dll`, `libpcsclite.so.1`, `PCSC.framework`) by dynamic symbol
   lookup instead of by C linkage. No C compiler at build time; the native
   library is still there at runtime.
2. **Reimplemented client library** — speak the pcscd IPC protocol ourselves
   over its unix socket, replacing `libpcsclite.so` entirely. The daemon still
   runs.
3. **Reimplemented whole stack** — no pcscd, no CCID driver: drive the USB
   reader directly from Go.

Only (3) is "pure Go" in the strict sense. (1) is what almost everyone means in
practice, and it is what actually removes the CI pain above.

## 3. Prior art

### 3.1 goscard — cgo-free client via purego (scope 1)

[`github.com/ElMostafaIdrassi/goscard`](https://github.com/ElMostafaIdrassi/goscard),
Apache-2.0, v1.0.0. Uses [purego](https://github.com/ebitengine/purego) to
`dlopen` the platform library and call into it without cgo:

- Windows → `winscard.dll`
- Linux → `libpcsclite.so.1` (tries several sonames and distro paths)
- macOS → `/System/Library/Frameworks/PCSC.framework/Versions/Current/PCSC`

It covers the whole WinSCard surface — including everything we use:
`NewContext`, `ListReaders`, `GetStatusChange`, `Cancel`, `Connect`,
`Transmit`, `Status`, `Disconnect`, plus transactions and attributes.

Verified locally (Go 1.25, `CGO_ENABLED=0`), a program importing goscard builds
for every target we ship:

```
linux/amd64: OK    darwin/amd64: OK    windows/amd64: OK
linux/arm64: OK    darwin/arm64: OK    windows/arm64: OK
```

and at runtime on linux/amd64 with `CGO_ENABLED=0`, purego loaded
`/lib/x86_64-linux-gnu/libpcsclite.so.1` (pcsc-lite 2.0.3), resolved the
symbols, and returned a genuine `SCARD_E_NO_SERVICE` from
`SCardEstablishContext` — correct, since no pcscd is running in that sandbox.
Dependency footprint is just `purego` + `golang.org/x/sys`.

Caveats: small project (18 commits, last activity January 2025), so we should
pin or vendor it and carry our own smoke tests; purego needs `CGO_ENABLED=0`
support per GOARCH, which holds for all six targets we ship but not for
linux/386, arm, riscv64, s390x; and one cosmetic startup error is logged on
Linux for the macOS-only `SCardUnload` symbol.

### 3.2 go-libpcsclite and friends — reimplemented client (scope 2)

[`gballet/go-libpcsclite`](https://github.com/gballet/go-libpcsclite) speaks the
pcscd socket protocol directly. Linux only, macOS and Windows explicitly not a
priority, several functions still unimplemented (the README's TODO), and no
context locking.

The decisive problem is that pcscd's IPC is a *private* ABI with a negotiated
version. `winscard_msg.h` in pcsc-lite currently declares:

```
#define PROTOCOL_VERSION_MAJOR 4
#define PROTOCOL_VERSION_MINOR 6
#define PROTOCOL_VERSION_MINOR_CLIENT_BACKWARD 4
#define PROTOCOL_VERSION_MINOR_SERVER_BACKWARD 4
```

go-libpcsclite hardcodes 4:3. A client below the server's backward floor gets
`SCARD_E_SERVICE_STOPPED` — the well-known "communication protocol mismatch",
which real users hit when a 4:3 client meets a pcsc-lite ≥ 2.3.0 daemon.

That version stood still at 4:3 from 2013 to 2024 and then moved three times in
six months: `CMD_GET_READER_EVENTS` (Jul 2024), backward-version support on both
sides (Dec 2025, shipped in 2.4.1, Jan 2026), and 4:6 with
`CMD_GET_READERS_STATE_SIZE` / `CMD_GET_READERS_STATE_ARRAY` (May 2026, shipped
in 2.5.0 alongside removal of the 16-reader limit). Anyone reimplementing this
protocol now signs up to track it.

Same family, same limits, all Linux+Windows only and none maintained:
[`sf1/go-card`](https://github.com/sf1/go-card) (explicitly "currently not
maintained"), [`deeper-x/gopcsc`](https://github.com/deeper-x/gopcsc),
[`agambier/smartcard`](https://pkg.go.dev/github.com/agambier/smartcard). All
three state that macOS "isn't and won't be supported, because its modified
PCSC-Lite variant can't be accessed without cgo".

### 3.3 Nothing exists for scope 3

There is no pure-Go CCID driver. The closest Go USB work is Linux-only usbfs
([`rafaelmartins/usbfs`](https://github.com/rafaelmartins/usbfs),
[`pzl/usb`](https://github.com/pzl/usb)); the mainstream binding
[`gousb`](https://github.com/gousb/gousb) is cgo over libusb.

## 4. Feasibility of writing our own

**Scope 1 (cgo-free client): easy, and mostly already done for us.** If we did
not want the goscard dependency, the same trick is roughly 500–800 lines for our
eight calls: `syscall.NewLazyDLL` on Windows (which `ebfe/scard` already does),
`purego.Dlopen` + `RegisterLibFunc` on Linux and macOS. The only real subtleties
are the `SCARD_READERSTATE` struct layout and pcsc-lite's `DWORD` being a 32-bit
`unsigned int` while macOS's `PCSC.framework` uses `uint32_t` in a differently
padded struct — which is exactly the kind of detail goscard has already
debugged.

**Scope 2 (reimplement libpcsclite): possible on Linux, pointless overall.**
Twenty commands with fixed-layout structs over `/run/pcscd/pcscd.comm` is a few
weeks of work, but it buys nothing goscard doesn't give us, and it takes on
permanent exposure to an unversioned-in-practice private protocol that started
moving again in 2025. It also does not help macOS at all: modern macOS has no
pcscd socket — `PCSC.framework` talks to the `com.apple.ctkpcscd` XPC service —
so macOS would still need `dlopen`. And on Windows there is no daemon protocol
to speak, only the DLL.

**Scope 3 (drive CCID readers directly): not worth attempting.** It fails on
device access before it even gets to the protocol work:

- *Windows*: the in-box CCID class driver owns the device. Talking to it from
  userspace means replacing that driver with WinUSB (Zadig-style), which is a
  non-starter for an end-user install.
- *macOS*: `com.apple.ifdreader` / the CryptoTokenKit stack claims the reader,
  and raw USB from Go needs IOKit — i.e. framework calls again, plus
  entitlements.
- *Linux*: workable via usbfs, but requires udev rules and, in practice,
  stopping pcscd so it stops claiming the reader — which breaks every other
  smart-card app on the machine.

On top of that we would owe a CCID implementation (bulk-in/out pipes, T=0 and
T=1 protocol handling, ATR parsing, PPS negotiation, escape/pseudo-APDU
handling for contactless readers) plus the per-reader quirk database that makes
`LudovicRousseau/CCID` work at all — that driver is tens of thousands of lines
plus a large device table, precisely because real readers misbehave in specific
ways. For an agent whose value is NDEF handling, this is the wrong mountain.

## 5. Recommendation

Adopt **goscard** behind our existing interfaces — `nfc.Manager` and
`nfc.Device` already isolate the PC/SC calls to two files, so this is a
contained change:

1. Add a thin internal adapter exposing the eight calls and five sentinel errors
   we use, so the rest of `nfc/` keeps compiling unchanged.
2. Implement it with goscard; keep the `ebfe/scard` implementation behind a
   build tag (`-tags cgopcsc`) as an escape hatch for a release or two.
3. Map goscard's `SCardError` values onto our sentinels — the codes are the same
   `0x8010xxxx` numbers, so this is a lookup table.
4. Drop `libpcsclite-dev`, the arm64 apt/cross-gcc block, and `CC` from
   `build.yml`; build everything with `CGO_ENABLED=0`.

Expected payoff: one static binary per target, no C toolchain, a much shorter
build workflow, and darwin/windows builds that could in principle cross-compile
from Linux runners.

Risks to check on real hardware before committing to it:

- `GetStatusChange` behaviour under purego. Our monitor polls with a 500 ms
  bound (`nfc/device_pcsc.go`) rather than blocking on `INFINITE`, which keeps
  us away from the worst case, but the reader-disconnect and `Cancel` paths need
  a real reader to validate.
- macOS: verify against `PCSC.framework` on both arm64 and amd64, including the
  reader-unplugged path.
- goscard's staleness: pin the version, and consider vendoring the ~4 files we
  depend on if upstream stays quiet.

## 6. References

- [ebfe/scard](https://github.com/ebfe/scard) — what we use today
- [ElMostafaIdrassi/goscard](https://github.com/ElMostafaIdrassi/goscard) — cgo-free PC/SC via purego
- [ebitengine/purego](https://github.com/ebitengine/purego) — cgo-free dynamic library calls, and its per-GOARCH support matrix
- [gballet/go-libpcsclite](https://github.com/gballet/go-libpcsclite) — pcscd socket client in Go, protocol 4:3
- [sf1/go-card](https://github.com/sf1/go-card), [deeper-x/gopcsc](https://github.com/deeper-x/gopcsc), [agambier/smartcard](https://pkg.go.dev/github.com/agambier/smartcard) — same approach, unmaintained, no macOS
- [pcsc-lite `winscard_msg.h`](https://github.com/LudovicRousseau/PCSC/blob/master/src/winscard_msg.h) — the IPC protocol and its version constants
- [pcsc-lite ChangeLog](https://github.com/LudovicRousseau/PCSC/blob/master/ChangeLog) — 2.4.1 backward version support, 2.5.0 reader-limit removal
- [FAQ: pcsc-lite and SCARD_E_SERVICE_STOPPED](https://blog.apdu.fr/posts/2023/04/faq-pcsc-lite-and-scardeservicestopped/) — what a protocol mismatch looks like
- [LudovicRousseau/CCID](https://github.com/LudovicRousseau/CCID) — the driver a scope-3 rewrite would have to replace
- [rafaelmartins/usbfs](https://github.com/rafaelmartins/usbfs), [pzl/usb](https://github.com/pzl/usb) — pure-Go USB, Linux only
