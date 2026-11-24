package mp3meta

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func textDelimiter(encoding byte) []byte {
	if encoding == 1 || encoding == 2 {
		return []byte{0, 0}
	}
	return []byte{0}
}

func takeEncodedText(b []byte, encoding byte) ([]byte, []byte, bool) {
	delim := textDelimiter(encoding)
	for i := 0; i+len(delim) <= len(b); i += len(delim) {
		if bytes.Equal(b[i:i+len(delim)], delim) {
			return b[:i], b[i+len(delim):], true
		}
	}
	return b, nil, false
}

func decodeID3Text(b []byte, encoding byte) (string, bool) {
	switch encoding {
	case 0:
		if utf8.Valid(b) && bytes.IndexFunc(b, func(r rune) bool { return r >= 128 }) < 0 {
			return string(b), true
		}
		var s strings.Builder
		s.Grow(len(b))
		for _, ch := range b {
			s.WriteRune(rune(ch))
		}
		return s.String(), true
	case 1, 2:
		if len(b)%2 != 0 {
			return "", false
		}
		var order binary.ByteOrder = binary.BigEndian
		if encoding == 1 && len(b) >= 2 {
			switch {
			case b[0] == 255 && b[1] == 254:
				order = binary.LittleEndian
				b = b[2:]
			case b[0] == 254 && b[1] == 255:
				b = b[2:]
			default:
				return "", false
			}
		}
		units := make([]uint16, 0, len(b)/2)
		for len(b) != 0 {
			units = append(units, order.Uint16(b))
			b = b[2:]
		}
		for i := 0; i < len(units); i++ {
			if units[i] >= 0xdc00 && units[i] <= 0xdfff {
				return "", false
			}
			if units[i] >= 0xd800 && units[i] <= 0xdbff {
				if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return "", false
				}
				i++
			}
		}
		return string(utf16.Decode(units)), true
	case 3:
		return string(b), utf8.Valid(b)
	default:
		return "", false
	}
}

func decodeID3Strings(b []byte, encoding byte, list bool) ([]string, bool) {
	var stringsOut []string
	var previousBOM []byte
	for {
		first, rest, more := takeEncodedText(b, encoding)
		if encoding == 1 && len(first) >= 2 {
			if bytes.HasPrefix(first, []byte{255, 254}) || bytes.HasPrefix(first, []byte{254, 255}) {
				previousBOM = first[:2]
			} else if previousBOM != nil {
				first = append(append([]byte(nil), previousBOM...), first...)
			}
		}
		s, ok := decodeID3Text(first, encoding)
		if !ok {
			return nil, false
		}
		stringsOut = append(stringsOut, s)
		if !more || !list || len(rest) == 0 {
			return stringsOut, true
		}
		b = rest
	}
}

func validID3Encoding(version, encoding byte) bool {
	return encoding <= 1 || version == 4 && encoding <= 3
}

func selectID3Encoding(version byte, values ...string) byte {
	if version == 4 {
		return 3
	}
	for _, s := range values {
		if strings.IndexFunc(s, func(r rune) bool { return r > 255 }) >= 0 {
			return 1
		}
	}
	return 0
}

func latin1Bytes(s string) ([]byte, bool) {
	var out []byte
	for _, r := range s {
		if r > 255 {
			return nil, false
		}
		out = append(out, byte(r))
	}
	return out, true
}

func appendEncodedText(dst []byte, encoding byte, s string) []byte {
	switch encoding {
	case 0:
		for _, r := range s {
			dst = append(dst, byte(r))
		}
	case 1:
		dst = append(dst, 255, 254)
		for _, u := range utf16.Encode([]rune(s)) {
			dst = append(dst, byte(u), byte(u>>8))
		}
	default:
		dst = append(dst, s...)
	}
	return dst
}

func encodeID3Strings(version byte, values []string) ([]byte, bool) {
	for _, s := range values {
		if strings.ContainsRune(s, 0) || !utf8.ValidString(s) {
			return nil, false
		}
	}
	encoding := selectID3Encoding(version, values...)
	out := []byte{encoding}
	for i, s := range values {
		if i > 0 {
			out = append(out, textDelimiter(encoding)...)
		}
		out = appendEncodedText(out, encoding, s)
	}
	if version == 4 && len(values) > 0 {
		out = append(out, textDelimiter(encoding)...)
	}
	return out, true
}

const id3FallbackPrefix = "array1:"

func packID3Fallback(values []string) ([]string, error) {
	for _, value := range values {
		if !utf8.ValidString(value) {
			return nil, errors.New("ID3 fallback requires valid UTF-8")
		}
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	return []string{id3FallbackPrefix + base64.RawURLEncoding.EncodeToString(data)}, nil
}

func unpackID3Fallback(values []string) ([]string, bool) {
	if len(values) != 1 {
		return values, true
	}
	body := strings.TrimPrefix(values[0], id3FallbackPrefix)
	if body == values[0] {
		return values, true
	}
	buf, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, false
	}
	var decoded []string
	if err := json.Unmarshal(buf, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func id3DescriptionFrame(version byte, id, description string, values []string) (rawID3Frame, error) {
	text, ok := encodeID3Strings(version, append([]string{description}, values...))
	if !ok {
		return rawID3Frame{}, fmt.Errorf("invalid text in ID3 frame %s", id)
	}
	return newRawID3Frame(version, id, text)
}

func applyUTF16BOM(text, previous []byte) []byte {
	if len(text) < 2 || len(previous) < 2 ||
		bytes.HasPrefix(text, []byte{255, 254}) || bytes.HasPrefix(text, []byte{254, 255}) {
		return text
	}
	if bytes.HasPrefix(previous, []byte{255, 254}) || bytes.HasPrefix(previous, []byte{254, 255}) {
		return append(append([]byte(nil), previous[:2]...), text...)
	}
	return text
}
