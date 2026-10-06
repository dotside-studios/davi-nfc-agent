# Hardware tests

Two checks need a real reader and a real tag, and cannot be settled by the
emulators: raw (framing-level) exchanges on PC/SC readers
([#96](https://github.com/dotside-studios/davi-nfc-agent/issues/96)), and
NTAG 424 DNA in LRP mode
([#99](https://github.com/dotside-studios/davi-nfc-agent/issues/99)). Each is one
`go test` command. They live in `hwtest/` behind the `hardware` build tag, so
`go test ./...` and CI never run them.

The tests open the reader through `nfc/pcsc` under an `nfc.Supervisor`, the code
the agent runs, and send through `Supervisor.TransceiveTag` and
`WithNTAG424Tag`, the calls the server makes for a client. A hook on the PC/SC
layer (`pcsc.SetObserver`, nothing sets it in the agent) records every command
the driver hands to the reader and every answer. What they write is a JSON
fixture a unit test can replay.

You need Go and a PC/SC stack (`pcscd` on Linux). Close anything else that holds
the reader, including a running agent.

## Environment

| Variable | Meaning |
|----------|---------|
| `DAVI_HW_READER` | Substring of the PC/SC reader name, case-insensitive. Default: the first reader. |
| `DAVI_HW_OUT` | Directory for fixtures. Default `hwtest-out/` at the repository root, which git ignores. A relative path is taken from the repository root. |
| `DAVI_HW_KEYS` | Key file for the NTAG 424 test, in the format of [card-keys.md](card-keys.md), loaded with `nfc/keyfile`, so the permission check applies (`chmod 600`). |
| `DAVI_HW_ENABLE_LRP_UID` | The 14-hex-character UID of the one tag to switch to LRP, **permanently**. The switch runs only when the tag on the reader has exactly this UID. Otherwise the test skips the switch and says why. |
| `DAVI_HW_INCLUDE_KEYS` | Set to `1` to store the AES keys in the LRP fixture. A replay needs them to authenticate. Only for a spare tag with throwaway keys. |
| `DAVI_HW_KEEP_SDM` | Set to `1` to leave the SDM configuration on the tag, so you can tap it in a browser. Default: file settings and message are put back. |

`DAVI_NFC_APDU_TRACE=1` and `-allow-raw-apdu` are not needed: the tests do not go
through the server's gate, and they record the bytes themselves. Setting the
trace as well does no harm.

## #96: raw framing

For each reader, put an NTAG213/215/216 or an Ultralight EV1 on it and run:

```bash
DAVI_HW_READER="ACR122" go test -tags hardware -v -count=1 -run TestRawFraming ./hwtest
```

Repeat with `DAVI_HW_READER` set to each reader, and with each tag type:

1. an **ACR122U** (Direct Transmit and `InCommunicateThru`),
2. an **ACR1252U** or another reader that offers PC/SC Part 3 (the transparent
   session and the probe),
3. a reader with **neither**: the test then asserts `canTransceiveRaw` is false,
   that every frame is refused as `NOT_SUPPORTED`, and that nothing reached the
   card.

The test sends `60`, `3C 00`, `39 02`, `3A 04 07` and `30 FF` as raw frames and
requires 8, 32, 3 and 16 byte replies and a typed error for `30 FF`. Then it
reads the tag's NDEF the normal way. A failed `READ_CNT` is logged, not failed:
an NTAG21x answers it only with its NFC counter enabled, which says nothing
about the framing.

Not covered, and still by hand: removing the card in the middle of a
transparent session.

**Send back:** the `hwtest-out/raw-*.json` files (one per reader and tag) and the
`-v` output. Each file holds the reader name, ATR, method (`acr122`, `part3` or
`none`), the `canTransceiveRaw` capability, and per frame the unwrapped frame and
reply and the exact bytes the reader saw.

## #99: NTAG 424 DNA in LRP mode

Use a **spare** tag. Switching to LRP is permanent. Make a key file for it. A
factory tag has all-zero keys:

```bash
cat > hw-keys.json <<'EOF'
{ "ntag424": { "master": "00000000000000000000000000000000" } }
EOF
chmod 600 hw-keys.json
export DAVI_HW_KEYS=$PWD/hw-keys.json DAVI_HW_INCLUDE_KEYS=1
```

`master` covers key numbers 0 to 4. A tag with its own keys needs them in
`slots` instead.

First a run that switches nothing. It records the AES baseline (`getFileSettings`,
`getKeyVersion`, an authentication), prints the tag's UID, and skips the rest:

```bash
go test -tags hardware -v -count=1 -run TestLRP ./hwtest
```

Then, naming that UID to confirm you mean this tag:

```bash
DAVI_HW_ENABLE_LRP_UID=04A1B2C3D4E5F6 go test -tags hardware -v -count=1 -run TestLRP ./hwtest
```

A warning prints before the switch. Run it again to repeat the LRP checks: a tag
already in LRP skips the switch.

The checks, in order:

1. Baseline on AES, then the switch with `NTAG424LRPSwitch.EnableLRP`.
2. Capabilities report `lrp: true`. The driver refuses the card while `AllowLRP`
   is false, with `ntag424.ErrLRP`.
3. With `AllowLRP`: authenticate, `GetCardUID`, `GetKeyVersion` and
   `GetFileSettings` in a session.
4. Protected NDEF write and read in MAC mode, then Full mode. The file settings
   are changed to need each mode, then restored.
5. `ChangeKey` on slot 3 to a test key (`A5` repeated) and back. If the way back
   fails, the failure names the slot and the test key.
6. `ConfigureSDM` with an LRP plan, once with PICCData encrypted and once
   mirrored in the clear. Each is tapped three times, and every URL is verified
   with `VerifyURLFresh` and `Keys.LRP`, with a repeat refused as a replay.

Encrypted file data (`{enc}`) is not offered for an LRP card by `PlanSDM`, so it
is not tested.

**Send back:** `hwtest-out/lrp-<uid>-<time>.json` and the `-v` output. The
fixture holds every APDU for each step, the SUN URLs and their counters, and,
with `DAVI_HW_INCLUDE_KEYS=1`, the keys, so treat it as a secret unless the tag
is a throwaway. Also say what the tag did if a step failed: a failed run is the
most useful one.

## Using what comes back

Copy the fixtures into `hwtest/fixture/testdata/` (`raw-*.json`, `lrp-*.json`).
The ordinary test suite then replays them:

- raw fixtures: `nfc/pcsc` rebuilds each wrapped command from the recorded frame
  with `acr122Thru` and the Part 3 builders, and decodes each recorded reader
  answer with `acr122Unthru` and `transparentReply`, requiring the recorded
  frames and replies;
- LRP fixtures: `hwtest/fixture` reproduces each authentication from the key and
  the reader's random number (recorded in the clear in the second message),
  requires the commands to match the recorded bytes, and verifies the MAC of
  every secure command and answer with `ev2.LRPSession`, then verifies the SUN
  URLs. Data inside a Full-mode message is covered by its MAC, not decrypted.

The files already there, `*-emulator-*.json`, come from the emulators in
`nfc/nfctest`, not from a card. They check that the harness and replay work. Only
a capture from hardware says anything about a reader or a card. Regenerate them
with `go test ./hwtest/scenario -args -update` and
`go test ./nfc/pcsc -run Harness -args -update-hwfixtures`.

See `hwtest/README.md` for the layout of the harness.
