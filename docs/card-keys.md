# Card keys

Cards that authenticate (MIFARE Classic sectors, DESFire applications, NTAG 424
DNA) need keys the agent holds. The shipped agent reads them from one JSON key
file at startup:

```bash
chmod 600 keys.json
./davi-nfc-agent -keys keys.json
# or
DAVI_NFC_KEYS=/etc/davi/keys.json ./davi-nfc-agent
```

`-keys` wins over `DAVI_NFC_KEYS` when both are set. One flag carries all three
card families, so there is a single mechanism and a single file to protect.

## Format

Hex strings throughout, upper or lower case, no `0x` prefix or separators.
Every section is optional.

```json
{
  "ntag424": {
    "master": "00112233445566778899AABBCCDDEEFF",
    "diversify": true,
    "systemID": "4E4643",
    "slots": { "0": "00112233445566778899AABBCCDDEEFF" },
    "allowLRP": false
  },
  "classic": ["A0A1A2A3A4A5", "D3F7D3F7D3F7"],
  "desfire": {
    "slots": { "0": "00112233445566778899AABBCCDDEEFF" }
  }
}
```

(The values above are placeholders, not keys from any card.)

| Field | Meaning |
|-------|---------|
| `ntag424.master` | 16-byte key used for every NTAG 424 key number with no `slots` entry, as it is or diversified |
| `ntag424.diversify` | Derive each card's key from `master` and the card's UID (NXP AN10922) instead of using `master` itself. Needs `master` |
| `ntag424.systemID` | Up to 24 bytes folded into diversification. Only with `diversify` |
| `ntag424.slots` | 16-byte keys by key number, 0 to 4, as decimal strings. A slot wins over `master`. A random-UID card with `diversify` needs one undiversified slot so the agent can learn the UID |
| `ntag424.allowLRP` | Let the agent authenticate to a card in LRP mode. Off by default: the LRP exchange is not yet validated against a published transcript or hardware |
| `classic` | 6-byte MIFARE Classic keys, tried before the built-in defaults |
| `desfire.slots` | 16-byte AES keys by key number, 0 to 13, as decimal strings |

The file is validated strictly. An unknown field, malformed hex, a key of the
wrong length, a slot number out of range or a second JSON document after the
first is an error. Errors name the section and field (`ntag424.slots: slot 1:
must be 32 hex characters`) and never print a value from the file.

## Permissions

On Linux and macOS the agent refuses a key file that is not a regular file or
that group or others can access (any of the `0o077` mode bits), as `ssh` does
for a private key, and tells you to run `chmod 600`. A key another account can
read is not a secret, so loading it anyway would be a quiet failure.

On Windows there are no mode bits to check: access is an ACL, which the mode
Go reports does not reflect. The check is skipped, so restrict the file to the
account the agent runs as yourself.

## Startup and reload

A key file that cannot be loaded stops the agent from starting, with the reason
on stderr. Running on without keys would answer every keyed operation with a
refusal that hides why.

On start the log says which keys were loaded and nothing more, for example
`Loaded keys from keys.json: MIFARE Classic keys: 2; NTAG 424 keys: master
(diversified), slots 1,2`.

On Linux and macOS, `kill -HUP <pid>` reloads the file. A reload that fails
keeps the keys already loaded and logs why. Replacing the NTAG 424 keys ends the
sessions open under the old ones; the next operation authenticates again.

## What the agent does with the keys

Keys live in the agent's memory for as long as it runs. They are not part of the
settings the Control Center shows or the agent saves, not in API responses
(`keysHeld` lists key numbers, never values), and not in logs or error text.
Anyone who can read the process's memory or the file can read the keys, so run
the agent as its own account and keep the file readable by that account only.

An `ntag424Request` for an operation that needs a key the agent does not hold
answers that no key is held and that keys are loaded with `-keys <file>`.

## Not covered

The file is plaintext. Encrypting it, reading keys from an operating-system
keychain, and loading them from the tray or the Control Center are not
implemented. A program embedding the agent can set keys directly with
`Agent.SetKeys`, or `Supervisor.SetClassicKeys`, `SetDESFireKeys` and
`SetNTAG424Keys`.
