package nfc

import (
	"fmt"
	"testing"
)

func TestMockTag_BasicOperations(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	tag.TagType = "MIFARE Classic 1K"
	tag.Data = []byte{0x01, 0x02, 0x03, 0x04}

	// Test UID()
	if uid := tag.UID(); uid != "04A1B2C3" {
		t.Errorf("Expected UID '04A1B2C3', got '%s'", uid)
	}

	// Test Type()
	if tagType := tag.Type(); tagType != "MIFARE Classic 1K" {
		t.Errorf("Expected type 'MIFARE Classic 1K', got '%s'", tagType)
	}

	// Test Connect()
	if err := tag.Connect(); err != nil {
		t.Errorf("Connect() failed: %v", err)
	}

	// Test ReadData()
	data, err := tag.ReadData()
	if err != nil {
		t.Errorf("ReadData() failed: %v", err)
	}

	if len(data) != 4 {
		t.Errorf("Expected data length 4, got %d", len(data))
	}

	// Test WriteData()
	newData := []byte{0x05, 0x06, 0x07, 0x08}
	if err := tag.WriteData(newData); err != nil {
		t.Errorf("WriteData() failed: %v", err)
	}

	// Verify data was written
	data, _ = tag.ReadData()
	if len(data) != 4 || data[0] != 0x05 {
		t.Errorf("Expected written data, got %v", data)
	}

	// Test Disconnect()
	if err := tag.Disconnect(); err != nil {
		t.Errorf("Disconnect() failed: %v", err)
	}
}

func TestMockTag_ConnectDisconnectErrors(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	expectedErr := fmt.Errorf("connection failed")
	tag.ConnectError = expectedErr

	if err := tag.Connect(); err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}

	// Reset connect error and connect successfully
	tag.ConnectError = nil
	if err := tag.Connect(); err != nil {
		t.Errorf("Connect() should succeed, got error: %v", err)
	}

	// Test disconnect error
	disconnectErr := fmt.Errorf("disconnect failed")
	tag.DisconnectError = disconnectErr

	if err := tag.Disconnect(); err != disconnectErr {
		t.Errorf("Expected error '%v', got '%v'", disconnectErr, err)
	}
}

func TestMockTag_ReadWriteErrors(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Test ReadDataError
	readErr := fmt.Errorf("read failed")
	tag.ReadDataError = readErr

	_, err := tag.ReadData()
	if err != readErr {
		t.Errorf("Expected error '%v', got '%v'", readErr, err)
	}

	// Test WriteDataError
	writeErr := fmt.Errorf("write failed")
	tag.WriteDataError = writeErr

	err = tag.WriteData([]byte{0x01})
	if err != writeErr {
		t.Errorf("Expected error '%v', got '%v'", writeErr, err)
	}
}

func TestMockTag_Transceive(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Test with TransceiveResponse
	tag.TransceiveResponse = []byte{0x90, 0x00}

	response, err := tag.Transceive([]byte{0x00, 0xA4})
	if err != nil {
		t.Errorf("Transceive() failed: %v", err)
	}

	if len(response) != 2 || response[0] != 0x90 {
		t.Errorf("Expected response [0x90 0x00], got %v", response)
	}
}

func TestMockTag_TransceiveCustomFunc(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Test with custom TransceiveFunc
	tag.TransceiveFunc = func(tx []byte) ([]byte, error) {
		// Return length of transmitted data as response
		return []byte{byte(len(tx))}, nil
	}

	response, err := tag.Transceive([]byte{0x01, 0x02, 0x03})
	if err != nil {
		t.Errorf("Transceive() failed: %v", err)
	}

	if len(response) != 1 || response[0] != 3 {
		t.Errorf("Expected response [3], got %v", response)
	}
}

func TestMockClassicTag_ReadWrite(t *testing.T) {
	tag := NewMockClassicTag("04A1B2C3")
	_ = tag.Connect()

	// Write data to sector 1, block 0
	writeData := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	key := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

	err := tag.Write(1, 0, writeData, key, 0x60)
	if err != nil {
		t.Errorf("Write() failed: %v", err)
	}

	// Read data back
	readData, err := tag.Read(1, 0, key, 0x60)
	if err != nil {
		t.Errorf("Read() failed: %v", err)
	}

	if len(readData) != 16 {
		t.Errorf("Expected 16 bytes, got %d", len(readData))
	}

	for i, b := range writeData {
		if readData[i] != b {
			t.Errorf("Expected byte %d at position %d, got %d", b, i, readData[i])
		}
	}
}

func TestMockClassicTag_SetGetBlockData(t *testing.T) {
	tag := NewMockClassicTag("04A1B2C3")

	// Set block data directly
	blockData := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	tag.SetBlockData(2, 1, blockData)

	// Get block data
	retrievedData, exists := tag.GetBlockData(2, 1)
	if !exists {
		t.Error("Block data should exist")
	}

	if len(retrievedData) != len(blockData) {
		t.Errorf("Expected data length %d, got %d", len(blockData), len(retrievedData))
	}

	for i, b := range blockData {
		if retrievedData[i] != b {
			t.Errorf("Expected byte %d at position %d, got %d", b, i, retrievedData[i])
		}
	}
}

func TestMockTag_CallLog(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	tag.ClearCallLog()

	tag.UID()
	tag.Type()
	_ = tag.Connect()
	_, _ = tag.ReadData()
	_ = tag.WriteData([]byte{0x01})
	_ = tag.Disconnect()

	callLog := tag.GetCallLog()
	expectedCalls := []string{
		"UID",
		"Type",
		"Connect",
		"ReadData",
		"WriteData(1 bytes)",
		"Disconnect",
	}

	if len(callLog) != len(expectedCalls) {
		t.Errorf("Expected %d calls, got %d", len(expectedCalls), len(callLog))
	}

	for i, call := range expectedCalls {
		if i >= len(callLog) {
			break
		}
		if callLog[i] != call {
			t.Errorf("Expected call %d to be '%s', got '%s'", i, call, callLog[i])
		}
	}
}

func TestMockTag_IsWritable(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Test default behavior - should be writable
	writable, err := tag.IsWritable()
	if err != nil {
		t.Errorf("IsWritable() failed: %v", err)
	}
	if !writable {
		t.Error("Expected tag to be writable by default")
	}

	// Test after making read-only
	tag.IsReadOnly = true
	writable, err = tag.IsWritable()
	if err != nil {
		t.Errorf("IsWritable() failed: %v", err)
	}
	if writable {
		t.Error("Expected tag to be read-only")
	}

	// Test with custom IsWritableFunc
	tag.IsReadOnly = false
	tag.IsWritableFunc = func() (bool, error) {
		return false, fmt.Errorf("custom error")
	}
	writable, err = tag.IsWritable()
	if err == nil {
		t.Error("Expected custom error from IsWritableFunc")
	}
	if writable {
		t.Error("Expected IsWritableFunc to return false")
	}
}

func TestMockTag_MakeReadOnly(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Verify tag is writable initially
	writable, _ := tag.IsWritable()
	if !writable {
		t.Error("Expected tag to be writable initially")
	}

	// Make tag read-only
	err := tag.MakeReadOnly()
	if err != nil {
		t.Errorf("MakeReadOnly() failed: %v", err)
	}

	// Verify tag is now read-only
	writable, _ = tag.IsWritable()
	if writable {
		t.Error("Expected tag to be read-only after MakeReadOnly()")
	}

	// Test that writing fails after making read-only
	err = tag.WriteData([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Error("Expected WriteData() to fail on read-only tag")
	}
}

func TestMockTag_MakeReadOnlyWithError(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Set custom error for MakeReadOnly
	expectedErr := fmt.Errorf("make read-only failed")
	tag.MakeReadOnlyError = expectedErr

	err := tag.MakeReadOnly()
	if err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}

	// Tag should still be writable since MakeReadOnly failed
	writable, _ := tag.IsWritable()
	if !writable {
		t.Error("Expected tag to still be writable after failed MakeReadOnly()")
	}
}

func TestMockTag_WriteProtectionWorkflow(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Write some data
	originalData := []byte{0x01, 0x02, 0x03, 0x04}
	err := tag.WriteData(originalData)
	if err != nil {
		t.Errorf("WriteData() failed: %v", err)
	}

	// Verify data was written
	readData, _ := tag.ReadData()
	if len(readData) != len(originalData) {
		t.Errorf("Expected data length %d, got %d", len(originalData), len(readData))
	}

	// Check that tag is writable
	writable, _ := tag.IsWritable()
	if !writable {
		t.Error("Expected tag to be writable")
	}

	// Make tag read-only
	err = tag.MakeReadOnly()
	if err != nil {
		t.Errorf("MakeReadOnly() failed: %v", err)
	}

	// Verify tag is read-only
	writable, _ = tag.IsWritable()
	if writable {
		t.Error("Expected tag to be read-only")
	}

	// Try to write again - should fail
	err = tag.WriteData([]byte{0x05, 0x06, 0x07, 0x08})
	if err == nil {
		t.Error("Expected WriteData() to fail on read-only tag")
	}

	// Verify original data is still intact
	readData, _ = tag.ReadData()
	if len(readData) != len(originalData) {
		t.Errorf("Expected data length %d, got %d", len(originalData), len(readData))
	}
	for i, b := range originalData {
		if readData[i] != b {
			t.Errorf("Expected byte %d at position %d, got %d", b, i, readData[i])
		}
	}
}

func TestMockTag_IsWritableWhenNotConnected(t *testing.T) {
	tag := NewMockTag("04A1B2C3")

	// Try to check if writable without connecting
	_, err := tag.IsWritable()
	if err == nil {
		t.Error("Expected IsWritable() to fail when not connected")
	}
}

func TestMockTag_MakeReadOnlyWhenNotConnected(t *testing.T) {
	tag := NewMockTag("04A1B2C3")

	// Try to make read-only without connecting
	err := tag.MakeReadOnly()
	if err == nil {
		t.Error("Expected MakeReadOnly() to fail when not connected")
	}
}

func TestMockTag_CanMakeReadOnly(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Test default behavior - should be able to make read-only
	canMake, err := tag.CanMakeReadOnly()
	if err != nil {
		t.Errorf("CanMakeReadOnly() failed: %v", err)
	}
	if !canMake {
		t.Error("Expected tag to be able to make read-only by default")
	}

	// Make tag read-only
	_ = tag.MakeReadOnly()

	// Test after making read-only - should NOT be able to make read-only again
	canMake, err = tag.CanMakeReadOnly()
	if err != nil {
		t.Errorf("CanMakeReadOnly() failed: %v", err)
	}
	if canMake {
		t.Error("Expected tag to NOT be able to make read-only when already read-only")
	}

	// Test with custom CanMakeReadOnlyFunc
	tag.IsReadOnly = false
	tag.CanMakeReadOnlyFunc = func() (bool, error) {
		return false, fmt.Errorf("custom error")
	}
	canMake, err = tag.CanMakeReadOnly()
	if err == nil {
		t.Error("Expected custom error from CanMakeReadOnlyFunc")
	}
	if canMake {
		t.Error("Expected CanMakeReadOnlyFunc to return false")
	}
}

func TestMockTag_CanMakeReadOnlyWhenNotConnected(t *testing.T) {
	tag := NewMockTag("04A1B2C3")

	// Try to check if can make read-only without connecting
	_, err := tag.CanMakeReadOnly()
	if err == nil {
		t.Error("Expected CanMakeReadOnly() to fail when not connected")
	}
}

func TestMockTag_CanMakeReadOnlyWithError(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// Set custom error for CanMakeReadOnly
	expectedErr := fmt.Errorf("cannot check read-only capability")
	tag.CanMakeReadOnlyError = expectedErr

	canMake, err := tag.CanMakeReadOnly()
	if err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}
	if canMake {
		t.Error("Expected CanMakeReadOnly() to return false when error is set")
	}
}

func TestMockTag_CompleteReadOnlyWorkflow(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	_ = tag.Connect()

	// 1. Check if tag is writable
	writable, err := tag.IsWritable()
	if err != nil || !writable {
		t.Error("Expected tag to be writable initially")
	}

	// 2. Check if we can make it read-only
	canMake, err := tag.CanMakeReadOnly()
	if err != nil {
		t.Errorf("CanMakeReadOnly() failed: %v", err)
	}
	if !canMake {
		t.Error("Expected to be able to make tag read-only")
	}

	// 3. Make tag read-only
	err = tag.MakeReadOnly()
	if err != nil {
		t.Errorf("MakeReadOnly() failed: %v", err)
	}

	// 4. Verify tag is no longer writable
	writable, err = tag.IsWritable()
	if err != nil || writable {
		t.Error("Expected tag to be read-only after MakeReadOnly()")
	}

	// 5. Verify we can't make it read-only again (it's already read-only)
	canMake, err = tag.CanMakeReadOnly()
	if err != nil {
		t.Errorf("CanMakeReadOnly() failed: %v", err)
	}
	if canMake {
		t.Error("Expected NOT to be able to make read-only when already read-only")
	}

	// 6. Verify write operations fail
	err = tag.WriteData([]byte{0x01, 0x02, 0x03})
	if err == nil {
		t.Error("Expected WriteData() to fail on read-only tag")
	}
}
