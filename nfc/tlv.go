package nfc

// TLV types for NDEF
const (
	TLVNull        = 0x00 // Null TLV
	TLVLockCtrl    = 0x01 // Lock Control TLV
	TLVMemCtrl     = 0x02 // Memory Control TLV
	TLVNDEF        = 0x03 // NDEF Message TLV
	TLVProprietary = 0xFD // Proprietary TLV
	TLVTerminator  = 0xFE // Terminator TLV
)

// TLVEncode encodes data into TLV format
// For NDEF, use type = 0x03 (TLVNDEF)
// Returns: [Type][Length][Value][Terminator (0xFE)]
func TLVEncode(data []byte, tlvType byte) []byte {
	length := len(data)
	var result []byte

	// Type byte
	result = append(result, tlvType)

	// Length field
	if length < 0xFF {
		// Short format: single byte length
		result = append(result, byte(length))
	} else {
		// Long format: 0xFF followed by 2-byte big-endian length
		result = append(result, 0xFF)
		result = append(result, byte(length>>8))
		result = append(result, byte(length&0xFF))
	}

	// Value
	result = append(result, data...)

	// Terminator TLV
	result = append(result, TLVTerminator)

	return result
}

// TLVDecode decodes a TLV structure and returns the value and type
// It skips Null TLVs and stops at the first non-null TLV or Terminator
func TLVDecode(data []byte) (value []byte, tlvType byte) {
	offset := 0

	for offset < len(data) {
		if offset >= len(data) {
			return nil, 0
		}

		tlvType = data[offset]
		offset++

		// Handle special TLV types
		switch tlvType {
		case TLVNull:
			// Null TLV has no length or value, continue to next
			continue
		case TLVTerminator:
			// Terminator TLV has no length or value
			return nil, TLVTerminator
		}

		// Get length
		if offset >= len(data) {
			return nil, 0
		}

		fls, fvs := TLVRecordLength(data[offset-1:])
		if fls == 0 || fvs == 0 {
			return nil, 0
		}

		// Adjust for the type byte we already read
		lengthStart := offset
		valueStart := offset + (fvs - 1) // fvs is relative to type byte

		// Calculate actual length
		var length int
		if data[lengthStart] == 0xFF {
			// Long format
			if lengthStart+2 >= len(data) {
				return nil, 0
			}
			length = int(data[lengthStart+1])<<8 | int(data[lengthStart+2])
		} else {
			// Short format
			length = int(data[lengthStart])
		}

		// Extract value
		if valueStart+length > len(data) {
			return nil, 0
		}

		return data[valueStart : valueStart+length], tlvType
	}

	return nil, 0
}

// TLVRecordLength returns the field length start offset and field value start offset
// relative to the start of the TLV record (including type byte)
// fls: offset where length field starts (1 for type byte)
// fvs: offset where value starts
// Returns (0, 0) if the TLV is malformed
func TLVRecordLength(data []byte) (fls, fvs int) {
	if len(data) < 2 {
		return 0, 0
	}

	// data[0] is the type byte
	// data[1] is the first byte of length

	fls = 1 // Length field starts at offset 1 (after type byte)

	if data[1] == 0xFF {
		// Long format: 0xFF + 2 bytes
		if len(data) < 4 {
			return 0, 0
		}
		fvs = 4 // Value starts after type (1) + 0xFF (1) + length (2)
	} else {
		// Short format: single byte
		fvs = 2 // Value starts after type (1) + length (1)
	}

	return fls, fvs
}

// TLVGetLength extracts the length from a TLV record
// data should start at the type byte
func TLVGetLength(data []byte) int {
	if len(data) < 2 {
		return 0
	}

	if data[1] == 0xFF {
		// Long format
		if len(data) < 4 {
			return 0
		}
		return int(data[2])<<8 | int(data[3])
	}

	// Short format
	return int(data[1])
}

// TLVFindNDEF finds the NDEF Message TLV in a TLV block
// Returns the NDEF message data and true if found, nil and false otherwise
func TLVFindNDEF(data []byte) ([]byte, bool) {
	offset := 0

	for offset < len(data) {
		if offset >= len(data) {
			return nil, false
		}

		tlvType := data[offset]

		switch tlvType {
		case TLVNull:
			// Null TLV has no length, just skip
			offset++
			continue

		case TLVTerminator:
			// End of TLV block
			return nil, false

		case TLVNDEF:
			// Found NDEF Message TLV
			if offset+1 >= len(data) {
				return nil, false
			}

			fls, fvs := TLVRecordLength(data[offset:])
			if fls == 0 || fvs == 0 {
				return nil, false
			}

			length := TLVGetLength(data[offset:])
			valueStart := offset + fvs

			if valueStart+length > len(data) {
				return nil, false
			}

			return data[valueStart : valueStart+length], true

		default:
			// Skip unknown TLV
			if offset+1 >= len(data) {
				return nil, false
			}

			fls, fvs := TLVRecordLength(data[offset:])
			if fls == 0 || fvs == 0 {
				return nil, false
			}

			length := TLVGetLength(data[offset:])
			offset += fvs + length
		}
	}

	return nil, false
}

// ParseTLVBlock parses all TLVs in a block and returns a map of type -> value
// Useful for parsing Capability Container TLVs
func ParseTLVBlock(data []byte) map[byte][]byte {
	result := make(map[byte][]byte)
	offset := 0

	for offset < len(data) {
		if offset >= len(data) {
			break
		}

		tlvType := data[offset]

		switch tlvType {
		case TLVNull:
			offset++
			continue

		case TLVTerminator:
			return result

		default:
			if offset+1 >= len(data) {
				return result
			}

			fls, fvs := TLVRecordLength(data[offset:])
			if fls == 0 || fvs == 0 {
				return result
			}

			length := TLVGetLength(data[offset:])
			valueStart := offset + fvs

			if valueStart+length > len(data) {
				return result
			}

			result[tlvType] = data[valueStart : valueStart+length]
			offset = valueStart + length
		}
	}

	return result
}
