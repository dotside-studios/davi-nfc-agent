# Installation

## Pre-built Binaries

Download pre-built binaries from [releases](https://github.com/dotside-studios/davi-nfc-agent/releases).

## Building from Source

```bash
git clone https://github.com/dotside-studios/davi-nfc-agent.git
cd davi-nfc-agent
go build .
```

## Requirements

The agent uses PC/SC for NFC reader communication. PC/SC is built into all major operating systems:

- **macOS**: Built-in (CCID framework)
- **Windows**: Built-in (WinSCard)
- **Linux**: Install `pcsclite`:
  ```bash
  # Debian/Ubuntu
  sudo apt install pcscd libpcsclite1

  # Fedora/RHEL
  sudo dnf install pcsc-lite

  # Arch Linux
  sudo pacman -S pcsclite
  ```

  The agent loads the PC/SC library at runtime, so the `-dev` headers are not
  needed to build or run it.

## Supported Readers

Any PC/SC-compatible NFC reader works, including:
- ACR122U, ACR1252U, ACR1552
- HID Omnikey readers
- Identiv readers
- Most USB CCID readers

## Troubleshooting

### "No NFC devices found"

- Ensure your NFC reader is connected
- Check that PC/SC service is running:
  ```bash
  # Linux
  sudo systemctl status pcscd
  sudo systemctl start pcscd
  ```
- Verify the reader is detected:
  ```bash
  # Linux
  pcsc_scan
  ```

### Reader LED and Buzzer

Reader feedback (**Flash and Beep on Scan**) drives the reader over
`SCardControl`. Two stacks refuse to carry those commands until told to:

- **Linux (pcsc-lite):** set bit 0 of `ifdDriverOptions` in the CCID driver's
  `libccid_Info.plist` (usually `/etc/libccid_Info.plist` or under
  `/usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist`) and restart
  `pcscd`. Without it pcsc-lite reports `SCARD_E_NOT_TRANSACTED`.
- **Windows:** the ACS driver answers out of the box. The generic CCID class
  driver needs `EscapeCommandEnable` set for the reader under
  `HKLM\SYSTEM\CurrentControlSet\Enum\...\Device Parameters\WUDFUsbccidDriver`.

Neither is required while a tag is on the reader: the agent falls back to
sending the same command over the card connection, and logs once when it does.
A reader that answers on neither channel is left alone for the rest of the
session.

### Permission Denied (Linux)

Add your user to the `pcscd` group or add udev rules:

```bash
# Create udev rule for common NFC readers
sudo tee /etc/udev/rules.d/99-nfc.rules << 'EOF'
SUBSYSTEM=="usb", ATTR{idVendor}=="072f", MODE="0666"
SUBSYSTEM=="usb", ATTR{idVendor}=="04e6", MODE="0666"
EOF

sudo udevadm control --reload-rules
sudo udevadm trigger
```
