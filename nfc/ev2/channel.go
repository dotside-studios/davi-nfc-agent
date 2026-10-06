package ev2

// Channel is an authenticated exchange with a card, whichever cipher suite
// carries it: a [Session] under AES, or an [LRPSession] under LRP. A command
// builder takes a Channel and neither knows nor cares which it was given.
type Channel interface {
	// Command builds the APDU for one command. header is the part the card
	// reads in the clear even under CommFull; data is the part CommFull
	// encrypts.
	Command(ins byte, header, data []byte, mode CommMode) ([]byte, error)

	// Response verifies the card's answer to the command just sent and returns
	// its data, decrypted under CommFull.
	Response(response []byte, mode CommMode) ([]byte, error)

	// TI is the transaction identifier the card assigned.
	TI() []byte

	// Counter is how many commands this session has sent.
	Counter() uint16
}

// CardChannel is the card's half of a [Channel], for an emulator and the tests
// that prove both halves agree.
type CardChannel interface {
	// VerifyCommand checks a command built by the other side and returns its
	// header and data, decrypted under CommFull.
	VerifyCommand(apdu []byte, mode CommMode, headerLen int) (header, data []byte, err error)

	// Answer builds the response to the command just verified, and counts it.
	Answer(status byte, data []byte, mode CommMode) ([]byte, error)

	TI() []byte
	Counter() uint16
}

var (
	_ Channel     = (*Session)(nil)
	_ Channel     = (*LRPSession)(nil)
	_ CardChannel = (*Session)(nil)
	_ CardChannel = (*LRPSession)(nil)
)
