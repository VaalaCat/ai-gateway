package jsontext

import "unicode/utf8"

// ValidEncoding reports whether payload is UTF-8 JSON text whose string
// escapes contain only valid Unicode scalar values. JSON structure remains the
// caller's decoder responsibility.
func ValidEncoding(payload []byte) bool {
	if !utf8.Valid(payload) {
		return false
	}
	inString := false
	for index := 0; index < len(payload); index++ {
		value := payload[index]
		if !inString {
			if value == '"' {
				inString = true
			}
			continue
		}
		switch value {
		case '"':
			inString = false
		case '\\':
			index++
			if index >= len(payload) {
				return false
			}
			escape := payload[index]
			if escape != 'u' {
				if !validSimpleEscape(escape) {
					return false
				}
				continue
			}
			code, end, ok := readUnicodeEscape(payload, index)
			if !ok || code >= 0xdc00 && code <= 0xdfff {
				return false
			}
			index = end
			if code < 0xd800 || code > 0xdbff {
				continue
			}
			if index+2 >= len(payload) || payload[index+1] != '\\' || payload[index+2] != 'u' {
				return false
			}
			low, lowEnd, ok := readUnicodeEscape(payload, index+2)
			if !ok || low < 0xdc00 || low > 0xdfff {
				return false
			}
			index = lowEnd
		default:
			if value < 0x20 {
				return false
			}
		}
	}
	return !inString
}

func validSimpleEscape(value byte) bool {
	switch value {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		return true
	default:
		return false
	}
}

func readUnicodeEscape(payload []byte, uIndex int) (uint16, int, bool) {
	if uIndex+4 >= len(payload) || payload[uIndex] != 'u' {
		return 0, uIndex, false
	}
	var code uint16
	for index := uIndex + 1; index <= uIndex+4; index++ {
		digit, ok := hexDigit(payload[index])
		if !ok {
			return 0, uIndex, false
		}
		code = code<<4 | uint16(digit)
	}
	return code, uIndex + 4, true
}

func hexDigit(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
