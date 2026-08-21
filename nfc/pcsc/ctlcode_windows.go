//go:build windows

package pcsc

// escapeControlCode is IOCTL_CCID_ESCAPE, the control code the Windows CCID and
// ACS drivers take vendor commands on:
// CTL_CODE(FILE_DEVICE_SMARTCARD, 3500, METHOD_BUFFERED, FILE_ANY_ACCESS).
//
// The ACS driver answers escape commands out of the box; the CCID class driver
// Windows otherwise falls back to needs the reader's EscapeCommandEnable value
// set in the registry, and reports SCARD_E_NOT_SUPPORTED until it is.
const escapeControlCode uint32 = 0x31<<16 | 3500<<2
