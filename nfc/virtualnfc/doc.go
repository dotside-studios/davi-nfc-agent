// Package virtualnfc is the shared core for software-defined NFC: a virtual
// reader that carries virtual tags to everything above it, with no dependency on
// PC/SC hardware, on the test harness, or on any wire protocol. It provides:
//
//   - Device / Manager: an in-memory reader and registry that hold a field of
//     Cards, support Present/Remove and Plug/Unplug, and drive an nfc.Supervisor.
//   - Card: a virtual tag ready to present, wrapping any nfc.Tag — either a
//     driver-backed tag over emulated silicon (NewDriverCard) or a route-backed
//     tag whose operations happen elsewhere (NewRoutedCard).
//   - RoutedTag / MergeCapabilities: a capability-gated tag that forwards
//     write/lock/transceive to a Route and serves reads from a scan-time snapshot.
//   - Helpers for building the values above: ParseUID, NDEF message builders.
//
// # Poll vs event: one field, two adapters
//
// How an nfc.Supervisor learns of a device's tags is an either/or at its
// boundary, keyed on nfc.DeviceCapabilities.CanPoll: a poll device is opened and
// its GetTags polled; an event device is never opened and instead reports through
// its Manager's TagReporter (Scans). A Device models both over one field that is
// always event-driven — Present and Remove are the events. Mode selects the
// read-out: PollMode answers GetTags from the current field snapshot, EventMode
// emits a ScannedTag on every change. A single Manager can host both kinds at
// once.
package virtualnfc
