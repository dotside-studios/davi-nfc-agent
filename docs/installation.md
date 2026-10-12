# Installation

## Download

Each [release](https://github.com/dotside-studios/davi-nfc-agent/releases)
has an archive per platform, holding the `davi-nfc-agent` binary:

| Platform | Archive |
|----------|---------|
| Linux x86-64 | `davi-nfc-agent-linux-amd64.tar.gz` |
| Linux ARM64 | `davi-nfc-agent-linux-arm64.tar.gz` |
| macOS Intel | `davi-nfc-agent-darwin-amd64.tar.gz` |
| macOS Apple silicon | `davi-nfc-agent-darwin-arm64.tar.gz` |
| Windows x86-64 | `davi-nfc-agent-windows-amd64.zip` |

```bash
tar -xzf davi-nfc-agent-linux-amd64.tar.gz
./davi-nfc-agent-linux-amd64/davi-nfc-agent
```

To build from source instead, with Go 1.25 or later:

```bash
git clone https://github.com/dotside-studios/davi-nfc-agent.git
cd davi-nfc-agent
go build ./cmd/davi-nfc-agent
```

## Set up the reader

The agent talks to USB readers over PC/SC. macOS and Windows include it.

### Linux

Install and start the PC/SC service:

```bash
# Debian, Ubuntu
sudo apt install pcscd libpcsclite1
# Fedora, RHEL
sudo dnf install pcsc-lite
# Arch
sudo pacman -S pcsclite

sudo systemctl enable --now pcscd
```

The agent loads the PC/SC library when it starts, so no `-dev` package is
needed.

## Supported readers

Any PC/SC reader works, among them the ACS ACR122U, ACR1252U and ACR1552, HID
Omnikey and Identiv readers, and most USB CCID readers. Framing-level raw
exchanges and the LED and buzzer depend on the reader; see
[Framing-level exchange](client-api.md#framing-level-exchange) and
[Reader LED and buzzer](#reader-led-and-buzzer).

## Run

```bash
./davi-nfc-agent
```

The agent starts in the system tray. On first start it creates its config
directory, a certificate and an API secret; see
[Configuration](configuration.md#files). Every flag is listed in
[Configuration](configuration.md#flags).

Where there is no desktop session, the agent logs that the tray could not
start and keeps serving.

## Troubleshooting

### The agent finds no reader

Check that the reader is plugged in and that PC/SC sees it. On Linux:

```bash
systemctl status pcscd
pcsc_scan
```

`SCARD_E_NO_SERVICE` in the agent's log means the PC/SC service is not
running.

### Permission denied on Linux

Give your user access to the reader with a udev rule for its USB vendor (`072f`
is ACS, `04e6` is Identiv):

```bash
sudo tee /etc/udev/rules.d/99-nfc.rules << 'EOF'
SUBSYSTEM=="usb", ATTR{idVendor}=="072f", MODE="0666"
SUBSYSTEM=="usb", ATTR{idVendor}=="04e6", MODE="0666"
EOF

sudo udevadm control --reload-rules
sudo udevadm trigger
```

### Reader LED and buzzer

**Flash and Beep on Scan** in the tray makes the reader signal each operation:
one green flash and a short beep for a read or a write, two red flashes for a
failed write or lock. It is off by default and lasts until the agent exits.

The commands are from the ACR122U instruction set, so ACR122 readers answer
them and other readers are skipped. They are sent with `SCardControl`, which
two stacks refuse until configured:

- **Linux (pcsc-lite):** set bit 0 of `ifdDriverOptions` in the CCID driver's
  `Info.plist` (usually `/etc/libccid_Info.plist`, or
  `/usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist`) and restart
  `pcscd`. Without it pcsc-lite answers `SCARD_E_NOT_TRANSACTED`.
- **Windows:** the ACS driver works as installed. The generic CCID class
  driver needs `EscapeCommandEnable` set for the reader under
  `HKLM\SYSTEM\CurrentControlSet\Enum\...\Device Parameters\WUDFUsbccidDriver`.

While a tag is on the reader the agent can send the same commands over the card
connection instead, and logs once when it does.
