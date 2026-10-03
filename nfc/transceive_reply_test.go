package nfc

import (
	"bytes"
	"testing"
	"time"
)

type scriptedTransport struct {
	replies [][]byte
	sent    [][]byte
}

func (s *scriptedTransport) Transceive(cmd []byte) ([]byte, error) {
	s.sent = append(s.sent, append([]byte(nil), cmd...))
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return reply, nil
}

func (s *scriptedTransport) IsCardPresent() bool { return true }

func rawReplies() [][]byte {
	return [][]byte{
		append(bytes.Repeat([]byte{0xAB}, 16), 0x91, 0xAF),
		{0x91, 0x00},
		{0x6A, 0x82},
		{0x01, 0x90, 0x00},
	}
}

func TestTransceive_ReturnsTheWholeReplyForEveryStatusWord(t *testing.T) {
	for name, kind := range map[string]DetectedTagType{
		"NTAG 424": DetectedNTAG424,
		"DESFire":  DetectedDESFireEV2,
		"Type 4":   DetectedISO14443_4,
	} {
		t.Run(name, func(t *testing.T) {
			transport := &scriptedTransport{replies: rawReplies()}
			tag := NewEmulatedTag(transport, "04A1B2C3D4E5F6", kind)
			transceiver, ok := tag.(TagTransceiver)
			if !ok {
				t.Fatal("tag does not transceive")
			}

			for _, want := range rawReplies() {
				got, err := transceiver.Transceive([]byte{0x90, 0x71, 0x00, 0x00, 0x00})
				if err != nil {
					t.Fatalf("Transceive: %v", err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("reply = % X, want % X", got, want)
				}
			}
		})
	}
}

func TestReaderTransceive_ReturnsTheWholeReply(t *testing.T) {
	transport := &scriptedTransport{replies: rawReplies()}
	tag := NewEmulatedTag(transport, "04A1B2C3D4E5F6", DetectedNTAG424)

	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}
	device := NewMockDevice()
	device.SetTags([]Tag{tag})
	manager.MockDevice = device

	reader, err := newDeviceReaderWithClock("mock:usb:001", manager, 5*time.Second, NewFakeClock(time.Now()))
	if err != nil {
		t.Fatalf("newDeviceReaderWithClock: %v", err)
	}
	t.Cleanup(reader.Close)

	for _, want := range rawReplies() {
		got, err := reader.TransceiveExpecting(t.Context(), []byte{0x90, 0x71, 0x00, 0x00, 0x00}, "04A1B2C3D4E5F6")
		if err != nil {
			t.Fatalf("TransceiveExpecting: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("reply = % X, want % X", got, want)
		}
	}
}
