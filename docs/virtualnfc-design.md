# A common `virtualnfc` package

**Status:** proposal / design note (no code changes yet)
**Scope:** `nfc/remotenfc`, `nfc/nfctest`, and a future standalone virtual reader

## Why this note exists

Two packages in this tree build "virtual" NFC out of the same `nfc` primitives,
from opposite ends:

- **`nfc/nfctest`** emulates the **card** side. In-memory `nfc.CardTransport`
  emulators speak the real PC/SC and DESFire wire protocols, are wrapped by the
  production tag drivers (`nfc.NewEmulatedTag`), and are then **presented on a
  virtual reader** — `EmulatedReader` (one device) and `EmulatedLanes` (many),
  both driving a real `nfc.Supervisor` over `nfc.MockManager`/`nfc.MockDevice`.
- **`nfc/remotenfc`** emulates the **reader/device** side. A phone that dials in
  over WebSocket becomes an `nfc.Device`; the tag it holds becomes an `nfc.Tag`
  whose writes/locks/transceives are **routed back** to the phone; a `Manager`
  registers devices, tracks which one holds which tag, and publishes scans.

Both are, underneath, the same machine: *a software-defined reader that carries
software-defined tags to everything above them.* They independently re-derived a
device registry, a present/remove model, a scan-publication path, a tag whose
capabilities are computed from what was declared, and a content→NDEF builder.

This note inventories that overlap and proposes a `nfc/virtualnfc` package that
holds the shared core, so:

1. `nfctest` and `remotenfc` are both thin shells over one implementation, and
2. a **standalone virtual reader** (a reader whose tags come from software, not
   PC/SC hardware) can be built from that core without pulling in `testing` or
   the phone wire protocol.

## What each package actually contains

### `nfc/nfctest` (card emulation + virtual reader for tests)

| File | Role | Test-specific? |
|------|------|----------------|
| `emulator.go` | `memEmulator`/`classicEmulator`/`desfireEmulator` — datasheet-fidelity `nfc.CardTransport`s; `EmulatedCard` (kind + preloaded NDEF); `EmulatedReader` (registry + present/remove over `MockManager`+`Supervisor`) | Reader/registry: **no**. Silicon: separable. |
| `lanes.go` | `laneManager` (one `MockDevice` per lane) + `EmulatedLanes` (multi-reader, plug/unplug) | **no** — pure device-registry machinery |
| `faults.go` | `FailingWrites`/`Corrupting`/`Slow`/`RemovingAfter` | reader-agnostic, but silicon-coupled |
| `contract.go` | `AssertTagContract` — the promises any `nfc.Tag` must keep | shared contract, imports `testing` |
| `*_test.go` | the actual tests | yes |

The only hard tie to `testing` is the `TB` interface (`Helper`/`Fatalf`/`Cleanup`)
that `EmulatedReader`/`EmulatedLanes` take, and `contract.go`'s `*testing.T`.

### `nfc/remotenfc` (phone-as-reader over WebSocket)

| File | Role | Wire/transport-specific? |
|------|------|--------------------------|
| `device.go` | `Device` — `nfc.Device` for an event-based phone (held-tag, declared caps, health) | registry: **no**; phone semantics: partly |
| `tag.go` | `Tag` — `nfc.Tag` that routes ops to the holder via `tagRoute`; three-valued capability merge; `AtomicLockWriter` | routing model: **no**; snapshot semantics: partly |
| `deviceops.go` | `TagOn`/`WriteTag`/`LockTag`/`TagCapabilities`/`TransceiveTag` consumer façade | **no** — mirrors `Supervisor` ops |
| `requests.go` | active-tag registry (`ActiveTag`/`ActiveTagDevices`/`setActiveTag`) + request/response correlation | registry: **no**; correlation: **yes** |
| `manager.go` | `Manager` — device registry + `TagReporter` + `DeviceDisconnector` + WebSocket session ownership + scan queue | registry/scan: **no**; sessions: **yes** |
| `server.go`, `wire.go`, `protocol.go`, `version.go` | WebSocket handshake, message types, handshake versioning | **yes** — stays in `remotenfc` |
| `convert.go`, `convert_ndef.go` | `ParseUID`; protocol-records→`nfc.NDEFMessage` | UID/NDEF: **no**; protocol shape: partly |

## The overlap, concretely

| Concern | `nfctest` | `remotenfc` | Shared core it wants |
|---------|-----------|-------------|----------------------|
| **Virtual `nfc.Device`** | `MockDevice` per lane (poll-mode, `CanPoll`, `GetTags`) | `Device` (event-mode, `SupportsEvents`, held-tag) | one virtual device supporting **both** poll and event modes |
| **Virtual `nfc.Manager`** | `laneManager` (paths→devices, `DeviceChanges`, plug/unplug) | `Manager` (deviceID→devices, `DeviceChanges`, register/unregister) | a virtual manager: named devices, change-notify, plug/unplug |
| **Present / remove a tag** | `EmulatedReader.Present/Remove`, `EmulatedLanes.Present/Remove` | `SendTagData`/`SendTagRemoved` → `setActiveTag`/`clearActiveTag` | one "field" abstraction: cards on/off a device |
| **"Who holds what" + scans** | tags pushed to `MockDevice.SetTags` | active-tag registry + `ScannedTag` publication via `TagReporter` | a holder registry that can also publish `ScannedTag` |
| **Virtual `nfc.Tag`** | driver-backed (`NewEmulatedTag` over silicon) | route-backed (forwards to holder), `ReadsAreSnapshot` | one `Tag` over a pluggable **backend** (driver *or* route) |
| **Capability computation** | fixed caps on lanes | three-valued merge: tag-declared ∧ device-declared ∧ route-available | shared capability-merge helpers + `TagCapabilities` |
| **Content → NDEF** | `WithText/WithURI/WithRecords/WithNDEF` (`NDEFRecordBuilder`) | `ConvertNDEFRecordInput` (protocol records) | one content-declaration → `nfc.NDEFMessage` path |
| **UID handling** | raw hex strings | `ParseUID` normalization | shared UID normalize/format |
| **Tag contract** | `AssertTagContract` (runs on both backends already) | consumed by remotenfc's tests | the contract lives with the shared `Tag` |

The last row is the tell: `AssertTagContract` is **already** run against both a
reader-driven tag and a phone-routed tag, precisely because "a tag reached
through a reader and a tag reached through a phone are the same thing to
everything above them." That shared contract is the seam a `virtualnfc` package
formalizes.

## Proposed `nfc/virtualnfc`

A package with **no dependency on `testing`** and **no dependency on any wire
protocol**, depending only on `nfc` (and `event`). It owns the reader/device/tag
scaffolding; the silicon emulators, the WebSocket protocol, and the fault/test
helpers stay in their current homes and build on it.

### Core types (sketch)

```go
package virtualnfc

// Backend performs a virtual tag's operations. It is the generalization of
// nfctest's driver-backed tag and remotenfc's route-backed tag.
//
//   - DriverBackend wraps an nfc.CardTransport via nfc.NewEmulatedTag, so real
//     driver I/O runs against in-memory silicon (today's nfctest).
//   - RouteBackend forwards write/lock/transceive to a remote holder and treats
//     reads as a snapshot (today's remotenfc.Tag + tagRoute).
type Backend interface {
    nfc.Tag // UID/Type/Read/Write/Lock/Transceive/Capabilities/Connect/Disconnect
}

// Card is a virtual tag ready to be presented: identity + declared capabilities
// + the backend that performs its operations.
type Card struct {
    UID        string
    Kind       nfc.DetectedTagType
    Technology string
    // declared TagCapabilities (three-valued: unset ≠ false), merged at query time
    backend    Backend
}

// Field is the set of cards currently on one virtual device. Present/Remove are
// the present/remove semantics both packages re-implement.
type Field struct { /* cards, mutex */ }
func (f *Field) Present(cards ...*Card)
func (f *Field) Remove(uid string)

// Device is an nfc.Device over a Field. Mode selects poll (GetTags) or event
// (Scans + held-tag) semantics, covering nfctest's MockDevice and remotenfc's
// Device respectively.
type Device struct { /* field, caps, mode */ }

// Manager is an nfc.Manager + DeviceChangeNotifier (+ optional TagReporter) over
// named virtual devices, with Plug/Unplug. Generalizes laneManager and the
// registry half of remotenfc.Manager.
type Manager struct { /* devices by path, changes chan */ }
func (m *Manager) Plug(path string, d *Device)
func (m *Manager) Unplug(path string)
```

Plus shared helpers already duplicated today:

- `MergeCapabilities(tagDeclared, deviceDeclared, routeAvailable) nfc.TagCapabilities`
  — the three-valued merge from `remotenfc/tag.go`, reusable by any route backend.
- `ParseUID` / UID formatting — lifted from `remotenfc/convert.go`.
- A content builder (`Text`/`URI`/`Records`/`NDEF` → `*nfc.NDEFMessage`) — the
  intersection of `EmulatedCard.With*` and `ConvertNDEFRecordInput`.

### Explicit non-goals (what stays out)

- **Silicon emulators** (`memEmulator`, `classicEmulator`, `desfireEmulator`) and
  **fault injection** — datasheet fidelity and NAK/corrupt/slow modelling stay in
  `nfctest` (or a `nfctest/emulator` subpackage). `virtualnfc` only needs the
  `Backend` interface they satisfy, not the silicon.
- **`testing` coupling** — the `TB` façade and `AssertTagContract`'s `*testing.T`
  stay in `nfctest`. (`AssertTagContract` may move next to the `Backend` contract
  as an exported checker that takes a minimal interface, but it keeps importing
  `testing` and so lives in a test-facing package.)
- **WebSocket / sessions / handshake / idempotency-over-network / pairing** —
  everything in `server.go`/`wire.go`/`protocol.go`/`version.go` and the session
  and request-correlation machinery stays in `remotenfc`.

## How the two packages refactor onto it

**`remotenfc`** keeps its wire layer and becomes:

- `remotenfc.Device` → a `virtualnfc.Device` in event mode + phone metadata
  (platform, app version, `MaxHoldMs`).
- `remotenfc.Tag` → a `virtualnfc.Card` with a `RouteBackend` whose `route` is
  the WebSocket `Manager`; the three-valued capability merge comes from the core.
- `remotenfc.Manager` → embeds/uses `virtualnfc.Manager` for the registry and
  scan publication, and adds sessions, correlation, and the WS endpoint on top.
- `deviceops.go` stays (it's the consumer façade), now delegating to the core.

**`nfctest`** keeps its silicon and test ergonomics and becomes:

- `EmulatedCard` → a `virtualnfc.Card` with a `DriverBackend` over the emulator,
  plus the `With*`/fault builders.
- `EmulatedReader`/`EmulatedLanes` → thin `TB`-aware wrappers that Plug a
  `virtualnfc.Device`/`Manager` and drive a real `nfc.Supervisor` (unchanged).
- `AssertTagContract` stays; it now documents the `virtualnfc.Backend` contract.

Nothing above the `nfc.Supervisor` changes: both still produce ordinary
`nfc.Tag`s reached through an ordinary `nfc.Manager`.

## The payoff: a standalone virtual reader

A **virtual reader** is a reader whose tags come from software rather than PC/SC
hardware — useful for demos, integration environments, load/soak testing, and
running the agent with no reader plugged in. Today that capability exists only
inside `nfctest` (bound to `testing`) or inside `remotenfc` (bound to phones).

With `virtualnfc` it becomes a small, dependency-light composition:

```
virtualnfc.Manager  (nfc.Manager + DeviceChangeNotifier [+ TagReporter])
    └── virtualnfc.Device(s)         ← Plug/Unplug at runtime
            └── virtualnfc.Field     ← Present/Remove cards
                    └── virtualnfc.Card
                            └── Backend:
                                 • DriverBackend  → local silicon (real driver I/O)
                                 • RouteBackend    → tag held elsewhere (network/API)
```

Slot that `Manager` into the existing `MultiManager` alongside `HardwareManager`
and `remotenfc.Manager` (see `docs/extending-nfc-support.md`) and the rest of the
agent — Supervisor, HTTP/WebSocket API, control center — treats it as just
another reader. A tag source (an in-memory set, a config file, an admin API, a
peer machine) drives `Present`/`Remove`; no `testing` import, no phone protocol.

This is the "later foundation" the branch name points at: `virtualnfc` is the
reusable middle, `nfctest` and `remotenfc` are two existing consumers of it, and
a `cmd/virtualreader` (or a `MultiManager` backend) is a third that the
extraction makes cheap.

## Migration plan (incremental, each step green)

1. **Land `nfc/virtualnfc` with no consumers.** Core `Backend`, `Card`, `Field`,
   `Device`, `Manager`, `MergeCapabilities`, `ParseUID`, content builder + unit
   tests. Move `AssertTagContract`'s substance next to `Backend` (still
   test-facing). No behavior change anywhere else.
2. **Migrate `nfctest` onto the core.** `EmulatedCard`/`EmulatedReader`/
   `EmulatedLanes` re-expressed over `virtualnfc`; silicon and fault builders
   stay. The existing `nfctest` test suite is the regression gate — it must stay
   green unchanged.
3. **Migrate `remotenfc` onto the core.** `Device`/`Tag`/registry re-expressed
   over `virtualnfc`; WS/session/correlation untouched. `remotenfc`'s contract,
   handshake, and capability tests are the gate.
4. **Build the standalone virtual reader** as a `MultiManager` backend and/or a
   small `cmd`, once 1–3 prove the core carries both existing consumers.

Steps 2 and 3 are independent and can land in either order.

## Risks & open questions

- **Poll vs event device modes.** `nfctest` uses poll-mode `MockDevice`
  (`CanPoll`); `remotenfc` uses event-mode (`SupportsEvents`, `GetTags` returns
  the held tag with a timeout). The core `Device` must support both cleanly
  rather than forcing one — this is the main design pressure on the abstraction.
- **Snapshot vs live reads.** Route-backed tags set `ReadsAreSnapshot=true`
  (a write can't be confirmed by reading back); driver-backed tags read live.
  This is a per-`Backend` property, so it fits, but the capability-merge helper
  must carry it faithfully.
- **Where the silicon lives.** Keeping emulators in `nfctest` means a non-test
  virtual reader that wants *real* local tag behavior imports `nfctest`. If that
  becomes awkward, factor the emulators into `nfctest/emulator` (or
  `virtualnfc/emulator`) with no `testing` dependency — deferred until a concrete
  consumer needs it.
- **Scope creep.** The temptation is to also unify the consumer-ops façade
  (`remotenfc.deviceops` vs `Supervisor` ops). That already converges at
  `nfc.Supervisor`; `virtualnfc` should stop at the device/tag/registry layer and
  not try to own operation orchestration.

## Recommendation

The overlap is real and structural, not incidental: both packages re-implement a
virtual device, a present/remove field, a holder registry, a computed-capability
tag, and a content→NDEF path, and both already agree on the `nfc.Tag` contract.
Extracting `nfc/virtualnfc` is worth doing, is low-risk because each existing
test suite is a tight regression gate, and directly enables a standalone virtual
reader. Recommend proceeding with step 1.
