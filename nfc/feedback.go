package nfc

// Signal is what a reader reports about the operation that just finished.
// It says what happened, not how to show it: the device driver decides
// whether that means a light, a beep, or nothing.
type Signal int

const (
	// SignalSuccess marks a tag read or written as asked.
	SignalSuccess Signal = iota

	// SignalFailure marks an operation that reached the tag and did not
	// complete, such as a refused write or a lock the tag would not take.
	SignalFailure
)

// String returns the signal name, for logs.
func (s Signal) String() string {
	switch s {
	case SignalSuccess:
		return "success"
	case SignalFailure:
		return "failure"
	default:
		return "unknown"
	}
}

// FeedbackDevice is optionally implemented by devices whose reader carries an
// indicator LED or a buzzer. Use a type assertion to find out:
//
//	if fb, ok := device.(FeedbackDevice); ok {
//	    _ = fb.Signal(SignalSuccess)
//	}
//
// A reader without that hardware, or whose PC/SC stack will not carry the
// commands driving it, reports NewNotSupportedError. That describes the
// reader, not the operation being signalled.
type FeedbackDevice interface {
	Signal(s Signal) error
}
