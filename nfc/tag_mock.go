package nfc

import (
	"fmt"
	"sync"
)

// MockTag is a test implementation of Tag that simulates NFC tag behavior.
//
// MockTag allows testing tag operations without physical tags by providing
// configurable mock responses for read/write operations.
//
// Example:
//
//	tag := &MockTag{
//	    TagUID: "04A1B2C3",
//	    TagType: "MIFARE Classic 1K",
//	    Data: []byte{0x00, 0x01, 0x02},
//	}
//	data, _ := tag.ReadData()
type MockTag struct {
	// TagUID is the UID returned by UID()
	TagUID string

	// TagType is the type string returned by Type()
	TagType string

	// TagNumericType is the numeric type returned by NumericType()
	TagNumericType int

	// Data is the data returned by ReadData()
	Data []byte

	// ReadDataFunc allows custom ReadData behavior (e.g. simulating a verification
	// mismatch or a transient read error). If nil, ReadData returns Data or
	// ReadDataError. Called with the mock's lock held; must not re-enter the mock.
	ReadDataFunc func() ([]byte, error)

	// WriteDataFunc allows custom WriteData behavior (e.g. failing a fixed number
	// of times before succeeding). If it returns nil, the data is still stored so
	// a subsequent ReadData reflects the write. If nil, WriteData stores Data or
	// returns WriteDataError. Called with the mock's lock held.
	WriteDataFunc func([]byte) error

	// ReadDataError, if set, will be returned by ReadData()
	ReadDataError error

	// WriteDataError, if set, will be returned by WriteData()
	WriteDataError error

	// TransceiveFunc allows custom transceive behavior
	// If nil, returns TransceiveResponse or TransceiveError
	TransceiveFunc func([]byte) ([]byte, error)

	// TransceiveResponse is the default response for Transceive calls
	TransceiveResponse []byte

	// TransceiveError, if set, will be returned by Transceive()
	TransceiveError error

	// ConnectError, if set, will be returned by Connect()
	ConnectError error

	// DisconnectError, if set, will be returned by Disconnect()
	DisconnectError error

	// IsConnected tracks whether the tag is currently connected
	IsConnected bool

	// IsReadOnly tracks whether the tag is in read-only mode
	IsReadOnly bool

	// IsWritableFunc allows custom IsWritable behavior
	// If nil, returns !IsReadOnly and IsWritableError
	IsWritableFunc func() (bool, error)

	// IsWritableError, if set, will be returned by IsWritable()
	IsWritableError error

	// MakeReadOnlyFunc allows custom MakeReadOnly behavior
	// If nil, sets IsReadOnly to true or returns MakeReadOnlyError
	MakeReadOnlyFunc func() error

	// MakeReadOnlyError, if set, will be returned by MakeReadOnly()
	MakeReadOnlyError error

	// CanMakeReadOnlyFunc allows custom CanMakeReadOnly behavior
	// If nil, returns !IsReadOnly and CanMakeReadOnlyError
	CanMakeReadOnlyFunc func() (bool, error)

	// CanMakeReadOnlyError, if set, will be returned by CanMakeReadOnly()
	CanMakeReadOnlyError error

	// CallLog tracks all method calls for verification in tests
	CallLog []string

	// MockCapabilities allows overriding the default capabilities
	// If nil, capabilities are inferred from TagType
	MockCapabilities *TagCapabilities

	mu sync.Mutex
}

// NewMockTag creates a new MockTag with default values.
func NewMockTag(uid string) *MockTag {
	return &MockTag{
		TagUID:         uid,
		TagType:        "Mock Tag",
		TagNumericType: 0,
		Data:           []byte{},
		IsConnected:    false,
		IsReadOnly:     false,
		CallLog:        make([]string, 0),
	}
}

// UID returns the tag's UID.
func (m *MockTag) UID() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "UID")
	return m.TagUID
}

// Type returns the tag's type string.
func (m *MockTag) Type() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "Type")
	return m.TagType
}

// NumericType returns the tag's numeric type.
func (m *MockTag) NumericType() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "NumericType")
	return m.TagNumericType
}

// Capabilities returns the tag's capabilities.
// If MockCapabilities is set, returns that; otherwise infers from TagType.
func (m *MockTag) Capabilities() TagCapabilities {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "Capabilities")
	if m.MockCapabilities != nil {
		return *m.MockCapabilities
	}
	return InferTagCapabilities(m.TagType)
}

// ReadData simulates reading data from the tag.
func (m *MockTag) ReadData() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "ReadData")

	if !m.IsConnected {
		return nil, fmt.Errorf("tag not connected")
	}

	if m.ReadDataFunc != nil {
		return m.ReadDataFunc()
	}

	if m.ReadDataError != nil {
		return nil, m.ReadDataError
	}

	// Return a copy to prevent external modification
	dataCopy := make([]byte, len(m.Data))
	copy(dataCopy, m.Data)
	return dataCopy, nil
}

// WriteData simulates writing data to the tag.
func (m *MockTag) WriteData(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, fmt.Sprintf("WriteData(%d bytes)", len(data)))

	if !m.IsConnected {
		return fmt.Errorf("tag not connected")
	}

	if m.IsReadOnly {
		return fmt.Errorf("tag is read-only")
	}

	if m.WriteDataFunc != nil {
		if err := m.WriteDataFunc(data); err != nil {
			return err
		}
	} else if m.WriteDataError != nil {
		return m.WriteDataError
	}

	// Store a copy of the data so a subsequent ReadData reflects the write.
	m.Data = make([]byte, len(data))
	copy(m.Data, data)
	return nil
}

// Transceive simulates data exchange with the tag.
func (m *MockTag) Transceive(data []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, fmt.Sprintf("Transceive(%d bytes)", len(data)))

	if !m.IsConnected {
		return nil, fmt.Errorf("tag not connected")
	}

	if m.TransceiveFunc != nil {
		return m.TransceiveFunc(data)
	}

	if m.TransceiveError != nil {
		return nil, m.TransceiveError
	}

	return m.TransceiveResponse, nil
}

// Connect simulates connecting to the tag.
func (m *MockTag) Connect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "Connect")

	if m.IsConnected {
		return fmt.Errorf("tag already connected")
	}

	if m.ConnectError != nil {
		return m.ConnectError
	}

	m.IsConnected = true
	return nil
}

// Disconnect simulates disconnecting from the tag.
func (m *MockTag) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "Disconnect")

	if !m.IsConnected {
		return fmt.Errorf("tag not connected")
	}

	if m.DisconnectError != nil {
		return m.DisconnectError
	}

	m.IsConnected = false
	return nil
}

// GetCallLog returns a copy of the call log for verification.
func (m *MockTag) GetCallLog() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	logCopy := make([]string, len(m.CallLog))
	copy(logCopy, m.CallLog)
	return logCopy
}

// ClearCallLog clears the call log.
func (m *MockTag) ClearCallLog() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = make([]string, 0)
}

// IsWritable simulates checking if the tag is writable.
func (m *MockTag) IsWritable() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "IsWritable")

	if !m.IsConnected {
		return false, fmt.Errorf("tag not connected")
	}

	if m.IsWritableFunc != nil {
		return m.IsWritableFunc()
	}

	if m.IsWritableError != nil {
		return false, m.IsWritableError
	}

	return !m.IsReadOnly, nil
}

// CanMakeReadOnly simulates checking if the tag can be made read-only.
func (m *MockTag) CanMakeReadOnly() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "CanMakeReadOnly")

	if !m.IsConnected {
		return false, fmt.Errorf("tag not connected")
	}

	if m.CanMakeReadOnlyFunc != nil {
		return m.CanMakeReadOnlyFunc()
	}

	if m.CanMakeReadOnlyError != nil {
		return false, m.CanMakeReadOnlyError
	}

	// By default, can make read-only if not already read-only
	return !m.IsReadOnly, nil
}

// MakeReadOnly simulates making the tag read-only.
func (m *MockTag) MakeReadOnly() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "MakeReadOnly")

	if !m.IsConnected {
		return fmt.Errorf("tag not connected")
	}

	if m.MakeReadOnlyFunc != nil {
		return m.MakeReadOnlyFunc()
	}

	if m.MakeReadOnlyError != nil {
		return m.MakeReadOnlyError
	}

	m.IsReadOnly = true
	return nil
}

// MockClassicTag is a test implementation of ClassicTag for MIFARE Classic tags.
type MockClassicTag struct {
	*MockTag

	// BlockData stores data for each sector/block combination
	// Key format: "sector:block" (e.g., "1:0")
	BlockData map[string][]byte

	// ReadError, if set, will be returned by Read()
	ReadError error

	// WriteError, if set, will be returned by Write()
	WriteError error

	mu sync.Mutex
}

// NewMockClassicTag creates a new MockClassicTag with default values.
func NewMockClassicTag(uid string) *MockClassicTag {
	return &MockClassicTag{
		MockTag:   NewMockTag(uid),
		BlockData: make(map[string][]byte),
	}
}

// Read simulates reading a block from the tag.
func (m *MockClassicTag) Read(sector, block uint8, key []byte, keyType int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, fmt.Sprintf("Read(sector:%d, block:%d, keyType:0x%02X)", sector, block, keyType))

	if !m.IsConnected {
		return nil, fmt.Errorf("tag not connected")
	}

	if m.ReadError != nil {
		return nil, m.ReadError
	}

	blockKey := fmt.Sprintf("%d:%d", sector, block)
	data, exists := m.BlockData[blockKey]
	if !exists {
		// Return empty block if not set
		return make([]byte, 16), nil
	}

	// Return a copy
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	return dataCopy, nil
}

// Write simulates writing a block to the tag.
func (m *MockClassicTag) Write(sector, block uint8, data []byte, key []byte, keyType int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, fmt.Sprintf("Write(sector:%d, block:%d, %d bytes, keyType:0x%02X)", sector, block, len(data), keyType))

	if !m.IsConnected {
		return fmt.Errorf("tag not connected")
	}

	if m.WriteError != nil {
		return m.WriteError
	}

	blockKey := fmt.Sprintf("%d:%d", sector, block)
	// Store a copy of the data
	m.BlockData[blockKey] = make([]byte, len(data))
	copy(m.BlockData[blockKey], data)
	return nil
}

// SetBlockData sets the data for a specific sector/block combination.
func (m *MockClassicTag) SetBlockData(sector, block uint8, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	blockKey := fmt.Sprintf("%d:%d", sector, block)
	m.BlockData[blockKey] = make([]byte, len(data))
	copy(m.BlockData[blockKey], data)
}

// GetBlockData retrieves the data for a specific sector/block combination.
func (m *MockClassicTag) GetBlockData(sector, block uint8) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	blockKey := fmt.Sprintf("%d:%d", sector, block)
	data, exists := m.BlockData[blockKey]
	if !exists {
		return nil, false
	}

	// Return a copy
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	return dataCopy, true
}

// MockISO14443Tag is a test implementation of ISO14443Tag for Type 4 tags.
type MockISO14443Tag struct {
	*MockTag
}

// NewMockISO14443Tag creates a new MockISO14443Tag with default values.
func NewMockISO14443Tag(uid string) *MockISO14443Tag {
	return &MockISO14443Tag{
		MockTag: NewMockTag(uid),
	}
}

// MockNtagTag is a test implementation of NtagTag for NTAG21x tags.
//
// MockNtagTag simulates page-based memory operations for NTAG213/215/216 tags.
//
// Example:
//
//	tag := NewMockNtagTag("04112233445566")
//	tag.Connect()
//	tag.WritePage(4, [4]byte{0x03, 0x04, 0xD1, 0x01})
//	data, _ := tag.ReadPage(4)
type MockNtagTag struct {
	*MockTag

	// PageData stores data for each page (0-134 for NTAG215)
	PageData map[byte][4]byte

	// ReadPageError, if set, will be returned by ReadPage()
	ReadPageError error

	// WritePageError, if set, will be returned by WritePage()
	WritePageError error

	// MaxPages defines the maximum page number (default 135 for NTAG215)
	MaxPages byte

	mu sync.Mutex
}

// NewMockNtagTag creates a new MockNtagTag with NTAG215 defaults.
func NewMockNtagTag(uid string) *MockNtagTag {
	tag := NewMockTag(uid)
	tag.TagType = CardTypeNtag215
	tag.TagNumericType = 100 // NTAG215 numeric type

	return &MockNtagTag{
		MockTag:  tag,
		PageData: make(map[byte][4]byte),
		MaxPages: 135, // NTAG215 has 135 pages
	}
}

// ReadPage simulates reading a 4-byte page from the NTAG tag.
func (m *MockNtagTag) ReadPage(page byte) ([4]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, fmt.Sprintf("ReadPage(%d)", page))

	if !m.IsConnected {
		return [4]byte{}, fmt.Errorf("tag not connected")
	}

	if page >= m.MaxPages {
		return [4]byte{}, fmt.Errorf("page %d out of range (max %d)", page, m.MaxPages-1)
	}

	if m.ReadPageError != nil {
		return [4]byte{}, m.ReadPageError
	}

	data, exists := m.PageData[page]
	if !exists {
		// Return empty page if not set
		return [4]byte{}, nil
	}

	return data, nil
}

// WritePage simulates writing a 4-byte page to the NTAG tag.
func (m *MockNtagTag) WritePage(page byte, data [4]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, fmt.Sprintf("WritePage(%d)", page))

	if !m.IsConnected {
		return fmt.Errorf("tag not connected")
	}

	if m.IsReadOnly {
		return fmt.Errorf("tag is read-only")
	}

	if page >= m.MaxPages {
		return fmt.Errorf("page %d out of range (max %d)", page, m.MaxPages-1)
	}

	// Pages 0-3 are typically read-only (UID, lock bytes, CC)
	if page < 4 {
		return fmt.Errorf("page %d is read-only (header area)", page)
	}

	if m.WritePageError != nil {
		return m.WritePageError
	}

	m.PageData[page] = data
	return nil
}

// SetPageData sets the data for a specific page (bypasses write protection for testing).
func (m *MockNtagTag) SetPageData(page byte, data [4]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.PageData[page] = data
}

// GetPageData retrieves the data for a specific page.
func (m *MockNtagTag) GetPageData(page byte) ([4]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, exists := m.PageData[page]
	return data, exists
}

// ClearPageData clears all page data.
func (m *MockNtagTag) ClearPageData() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.PageData = make(map[byte][4]byte)
}
