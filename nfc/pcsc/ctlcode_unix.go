//go:build !windows

package pcsc

// escapeControlCode is IOCTL_SMARTCARD_VENDOR_IFD_EXCHANGE, the control code
// pcsc-lite (Linux) and the PCSC framework (macOS) take vendor commands on:
// SCARD_CTL_CODE(1). Windows builds the same code differently, which is why
// this is per-platform.
//
// pcsc-lite refuses escape commands unless its CCID driver was configured with
// them authorised (ifdDriverOptions bit 0 in libccid_Info.plist), reporting
// SCARD_E_NOT_TRANSACTED while it is not.
const escapeControlCode uint32 = 0x42000000 + 1
