package remotenfc

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/nfctest"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// TestRemoteTagsKeepTheContract runs tags held by a phone through the same
// contract the reader's tags face, in the states a device can put them in. A
// caller above this layer cannot tell the two backends apart, so neither
// should the checks.
func TestRemoteTagsKeepTheContract(t *testing.T) {
	tests := []struct {
		name   string
		caps   *protocol.TagCapabilities
		writer TagWriter
	}{
		{
			name: "no route back to the device",
			caps: &protocol.TagCapabilities{CanWrite: true, CanLock: true, CanTransceive: true},
		},
		{
			name:   "device that only reads",
			caps:   &protocol.TagCapabilities{},
			writer: &stubWriter{},
		},
		{
			name:   "device that writes and locks",
			caps:   &protocol.TagCapabilities{CanWrite: true, CanLock: true},
			writer: &stubWriter{canWrite: true, canLock: true},
		},
		{
			name:   "tag the device reports as read-only",
			caps:   &protocol.TagCapabilities{CanWrite: true, CanLock: true, IsReadOnly: true},
			writer: &stubWriter{canWrite: true, canLock: true},
		},
		{
			name: "nothing declared",
			caps: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nfctest.AssertTagContract(t, declaredTag(t, tt.caps, tt.writer))
		})
	}
}
