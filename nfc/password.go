package nfc

import "fmt"

// PasswordOptions configures how password protection is applied to a tag.
//
// These fields define the contract the hardware implementation will honor.
// Password protection is currently gated off pending validation on real
// hardware (see SetCardPassword), so they are not yet acted upon.
type PasswordOptions struct {
	// Pack is the 2-byte password acknowledge (PACK) returned by the tag on a
	// successful authentication. If empty, the implementation chooses a default.
	Pack []byte

	// ProtectRead, when true, requires the password for reads as well as
	// writes. When false (the default), only writes are password-protected.
	ProtectRead bool

	// StartPage is the first tag page protected by the password (NTAG AUTH0).
	// Implementations enforce a floor so the tag's own configuration pages can
	// never be locked out of reach, which would brick the tag.
	StartPage int
}

// PasswordResult describes the outcome of a password operation.
type PasswordResult struct {
	// UID of the tag the operation targeted.
	UID string `json:"uid"`
	// TagType is the human-readable tag type string.
	TagType string `json:"tagType"`
	// Protected reports whether the tag is password-protected after the
	// operation (true after a successful set, false after a successful remove).
	Protected bool `json:"protected"`
}

// SetCardPassword configures password protection on the presented tag.
//
// NOTE: password protection is NOT yet enabled in this build. The per-tag
// capability is reported (TagCapabilities.SupportsPassword) and this API
// contract is fixed, but the destructive configuration-page writes (PWD, PACK,
// AUTH0, ACCESS) are intentionally gated off pending validation on real
// hardware — a wrong AUTH0/ACCESS configuration can permanently lock a tag.
// This method currently returns a not-supported error for all tags.
func (r *NFCReader) SetCardPassword(password []byte, opts PasswordOptions) (*PasswordResult, error) {
	return r.passwordOperation("SetPassword", func(*Card) error {
		// Implementation pending hardware validation.
		return NewNotSupportedError("password protection (not yet enabled; pending hardware validation)")
	})
}

// RemoveCardPassword clears password protection from the presented tag.
//
// Like SetCardPassword, this is gated off pending hardware validation and
// currently returns a not-supported error.
func (r *NFCReader) RemoveCardPassword(password []byte) (*PasswordResult, error) {
	return r.passwordOperation("RemovePassword", func(*Card) error {
		// Implementation pending hardware validation.
		return NewNotSupportedError("password protection (not yet enabled; pending hardware validation)")
	})
}

// passwordOperation acquires the presented tag, verifies the tag type supports
// password protection, and runs op. It mirrors the write/lock acquisition path
// so password operations are serialized with other tag operations.
func (r *NFCReader) passwordOperation(op string, fn func(*Card) error) (*PasswordResult, error) {
	var result *PasswordResult
	err := r.withTagOperation(func() error {
		card, err := r.prepareCardForWrite()
		if err != nil {
			return err
		}

		defer func() {
			r.statusMux.Lock()
			r.isWriting = false
			r.statusMux.Unlock()
		}()

		if !GetTagCapabilities(card.tag).SupportsPassword {
			return NewNotSupportedError(fmt.Sprintf("%s (tag type %s does not support password protection)", op, card.Type))
		}

		if err := fn(card); err != nil {
			return fmt.Errorf("%s failed for UID %s (Type: %s): %w", op, card.UID, card.Type, err)
		}

		result = &PasswordResult{UID: card.UID, TagType: card.Type, Protected: op == "SetPassword"}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
