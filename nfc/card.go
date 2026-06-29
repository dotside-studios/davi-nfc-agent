package nfc

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Card represents a detected NFC card with its metadata and provides
// io.Reader and io.Writer interfaces for reading and writing NDEF data.
//
// Example usage:
//
//	card, err := reader.Scan()
//	if err != nil {
//		log.Fatal(err)
//	}
//
//	// Read data
//	data, _ := io.ReadAll(card)
//	fmt.Printf("Card UID: %s, Type: %s, Data: %s\n", card.UID, card.Type, data)
//
//	// Write data
//	card.Reset()
//	io.WriteString(card, "Hello NFC!")
//	card.Close()
type Card struct {
	// Metadata about the card
	UID          string    `json:"uid"`                    // Unique identifier of the card
	Type         string    `json:"type"`                   // Human-readable type (e.g., "MIFARE Classic 1K", "Type4")
	Technology   string    `json:"technology"`             // Technology family (e.g., "ISO14443A", "ISO14443B")
	ScannedAt    time.Time `json:"scanned_at"`             // When the card was detected
	LastAccessed time.Time `json:"last_accessed"`          // Last read/write operation time
	MessageData  Message   `json:"message_data,omitempty"` // Cached message data, if any

	// Internal state for io.Reader
	tag        Tag    // The underlying tag implementation
	readBuffer []byte // Cached data from the tag
	readOffset int    // Current read position
	hasRead    bool   // Whether data has been loaded from tag

	// Internal state for io.Writer
	writeBuffer []byte // Buffer for data to be written
}

// NewCard creates an Card from a Tag.
// This is an internal constructor used by the reader/manager.
func NewCard(tag Tag) *Card {
	now := time.Now()
	return &Card{
		UID:          tag.UID(),
		Type:         tag.Type(),
		Technology:   inferTechnology(tag.Type()),
		ScannedAt:    now,
		LastAccessed: now,
		tag:          tag,
		hasRead:      false,
		writeBuffer:  make([]byte, 0, 256),
	}
}

// Capabilities reports what operations the card's tag supports (memory,
// writability, lock/password support, read-only state). When the underlying
// tag is available it is queried directly; otherwise capabilities are inferred
// from the tag type string.
func (c *Card) Capabilities() TagCapabilities {
	if c.tag != nil {
		return GetTagCapabilities(c.tag)
	}
	return InferTagCapabilities(c.Type)
}

// inferTechnology determines the NFC technology from the tag type string.
func inferTechnology(tagType string) string {
	// Simple heuristic based on tag type string
	switch {
	case strings.Contains(tagType, "MIFARE"):
		return "ISO14443A"
	case strings.Contains(tagType, "Type4"):
		return "ISO14443A/B"
	case strings.Contains(tagType, "DESFire"):
		return "ISO14443A"
	default:
		return "Unknown"
	}
}

// Read implements io.Reader. Reads NDEF message data from the card.
// The first call to Read() fetches the entire NDEF message from the card.
// Subsequent calls stream from the cached data.
//
// Example:
//
//	data, err := io.ReadAll(card)
//	if err != nil {
//		log.Fatal(err)
//	}
func (c *Card) Read(p []byte) (n int, err error) {
	// Lazy load: fetch data on first read
	if !c.hasRead {
		c.LastAccessed = time.Now()
		c.readBuffer, err = c.tag.ReadData()
		if err != nil {
			return 0, fmt.Errorf("failed to read from card %s: %w", c.UID, err)
		}
		c.hasRead = true
		c.readOffset = 0
	}

	// EOF if we've read everything
	if c.readOffset >= len(c.readBuffer) {
		return 0, io.EOF
	}

	// Copy data to the provided buffer
	n = copy(p, c.readBuffer[c.readOffset:])
	c.readOffset += n
	return n, nil
}

// Write implements io.Writer. Buffers data to be written to the card.
// The actual write to the card happens on Close() or Flush().
//
// Example:
//
//	n, err := card.Write([]byte("Hello World"))
//	// or
//	io.WriteString(card, "Hello World")
func (c *Card) Write(p []byte) (n int, err error) {
	c.writeBuffer = append(c.writeBuffer, p...)
	return len(p), nil
}

// Flush writes the buffered data to the card immediately without closing.
// The buffer is cleared after a successful write.
func (c *Card) Flush() error {
	if len(c.writeBuffer) == 0 {
		return nil // Nothing to write
	}

	c.LastAccessed = time.Now()
	if err := c.tag.WriteData(c.writeBuffer); err != nil {
		return fmt.Errorf("failed to write to card %s: %w", c.UID, err)
	}

	c.writeBuffer = c.writeBuffer[:0] // Clear buffer but keep capacity
	return nil
}

// Close implements io.Closer. Writes any buffered data to the card.
func (c *Card) Close() error {
	return c.Flush()
}

// Reset clears the read cache, allowing fresh data to be read from the card.
// Useful if you want to re-read after writing or if the card's data may have changed.
func (c *Card) Reset() {
	c.hasRead = false
	c.readBuffer = nil
	c.readOffset = 0
}

// String returns a string representation of the card metadata.
func (c *Card) String() string {
	return fmt.Sprintf("Card{UID: %s, Type: %s, Tech: %s, ScannedAt: %s}",
		c.UID, c.Type, c.Technology, c.ScannedAt.Format(time.RFC3339))
}

// ReadMessage reads and decodes a message from the card.
// It attempts to parse as NDEF first, falling back to TextMessage (raw bytes) if that fails.
//
// Example:
//
//	msg, err := card.ReadMessage()
//	switch m := msg.(type) {
//	case *nfc.NDEFMessage:
//	    text, _ := m.GetText()
//	case *nfc.TextMessage:
//	    raw := m.Data
//	}
func (c *Card) ReadMessage() (Message, error) {
	// If we already have a cached message, return it
	if c.MessageData != nil {
		return c.MessageData, nil
	}

	// Read raw data from card
	data, err := io.ReadAll(c)
	if err != nil {
		return nil, err
	}

	// Try to parse as NDEF first
	if msg, err := DecodeNDEF(data); err == nil {
		c.MessageData = msg // Cache the parsed message
		return msg, nil
	}

	// Fallback: return raw bytes as TextMessage
	textMsg := NewTextMessage(data)
	c.MessageData = textMsg // Cache the raw message
	return textMsg, nil
}

// WriteMessage encodes and writes a message to the card.
//
// Example:
//
//	msg := nfc.NewTextMessage("Hello!", "en")
//	err := card.WriteMessage(msg)
func (c *Card) WriteMessage(msg Message) error {
	data, err := msg.Encode()
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}

	_, err = c.Write(data)
	if err != nil {
		return err
	}

	return c.Close()
}

// GetUnderlyingTag returns the underlying Tag for advanced operations.
// Use this only when you need tag-specific functionality not available through
// the standard io.Reader/Writer interface.
//
// Example for MIFARE Classic specific operations:
//
//	if classicTag, ok := card.GetUnderlyingTag().(ClassicTag); ok {
//		data, err := classicTag.Read(1, 0, key, keyType)
//	}
func (c *Card) GetUnderlyingTag() Tag {
	return c.tag
}
