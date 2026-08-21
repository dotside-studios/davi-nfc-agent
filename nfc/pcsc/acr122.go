// LED and buzzer commands for ACS ACR122 readers.
//
// These are reader commands, not card commands. The reader answers them
// itself, either as a vendor command over SCardControl or, because they are
// shaped like APDUs, as a pseudo-APDU over a card connection. feedback.go
// picks between the two.

package pcsc

import (
	"fmt"
	"strings"
)

// The ACR122U "Bi-Color LED and Buzzer Control" command (ACS ACR122U API,
// section 6.2): CLA FF, INS 00, P1 40, P2 the LED state control below, then
// four bytes of blinking duration control.
const (
	acr122CLA       = 0xFF
	acr122INS       = 0x00
	acr122P1LEDBuzz = 0x40
	acr122Lc        = 0x04
)

// Bits of the LED state control byte. A state bit is only read when its mask
// bit is set, so leaving a mask clear leaves that LED as the reader's firmware
// had it.
const (
	acr122FinalRedOn     byte = 1 << 0 // red state once blinking ends
	acr122FinalGreenOn   byte = 1 << 1 // green state once blinking ends
	acr122FinalRedMask   byte = 1 << 2 // apply the final red state
	acr122FinalGreenMask byte = 1 << 3 // apply the final green state
	acr122BlinkRedOn     byte = 1 << 4 // red is lit for T1 of each cycle
	acr122BlinkGreenOn   byte = 1 << 5 // green is lit for T1 of each cycle
	acr122BlinkRedMask   byte = 1 << 6 // blink the red LED
	acr122BlinkGreenMask byte = 1 << 7 // blink the green LED
)

// Where in a blink cycle the buzzer sounds. T1 is the first half of the cycle
// and T2 the second, so T1 alone gives one short beep per repetition.
const (
	acr122BuzzerOff  byte = 0x00
	acr122BuzzerT1   byte = 0x01
	acr122BuzzerT2   byte = 0x02
	acr122BuzzerBoth byte = 0x03
)

// acr122Blink is one LED and buzzer sequence: the state control byte, the two
// halves of the blink cycle in units of 100 ms, the number of repetitions, and
// the buzzer link.
type acr122Blink struct {
	led    byte
	t1     byte
	t2     byte
	repeat byte
	buzzer byte
}

// command renders the blink as the bytes the reader expects.
func (b acr122Blink) command() []byte {
	return []byte{
		acr122CLA, acr122INS, acr122P1LEDBuzz, b.led, acr122Lc,
		b.t1, b.t2, b.repeat, b.buzzer,
	}
}

// The signals this package gives. Both set the blink masks but not the state
// masks, so the reader restores whatever the LED was showing before.
var (
	// acr122Success: one green blink with a beep over it, 200 ms in total.
	acr122Success = acr122Blink{
		led:    acr122BlinkGreenMask | acr122BlinkGreenOn,
		t1:     1,
		t2:     1,
		repeat: 1,
		buzzer: acr122BuzzerT1,
	}

	// acr122Failure: two red blinks with a beep over each, distinguishable
	// from the single green one at a glance.
	acr122Failure = acr122Blink{
		led:    acr122BlinkRedMask | acr122BlinkRedOn,
		t1:     1,
		t2:     1,
		repeat: 2,
		buzzer: acr122BuzzerT1,
	}
)

// hasACR122LEDBuzzer reports whether a reader answers the commands above,
// judged by the name PC/SC gives it ("ACS ACR122U PICC Interface 00 00" and
// the like). There is no capability to query: a reader that does not know the
// command refuses it exactly as one that never received it does.
func hasACR122LEDBuzzer(readerName string) bool {
	return strings.Contains(strings.ToUpper(readerName), "ACR122")
}

// checkACR122Response reads the reader's answer to a LED and buzzer command.
// The answer is 90 followed by the current LED states, so the second byte
// carries data rather than a status and only the first says whether the
// command was understood.
func checkACR122Response(resp []byte) error {
	if len(resp) < 2 {
		return fmt.Errorf("reader answered %d bytes, want at least 2", len(resp))
	}
	if sw1 := resp[len(resp)-2]; sw1 != 0x90 {
		return fmt.Errorf("reader refused the LED and buzzer command: SW1=%02X", sw1)
	}
	return nil
}
