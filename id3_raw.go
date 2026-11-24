package mp3meta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const maxID3BodySize = 1<<28 - 1

// ErrUnsafeID3Edit indicates that rewriting the tag could discard or invalidate
// metadata whose storage this adapter cannot safely modify.
var ErrUnsafeID3Edit = errors.New("unsafe ID3 edit")

type rawID3Frame struct {
	id    string
	flags uint16
	raw   []byte
	body  []byte
}

type rawID3Tag struct {
	header  [10]byte
	footer  [10]byte
	version byte
	start   int64
	end     int64
	bodyLen int
	frames  []rawID3Frame
	trailer []byte
	blocked string
}

func readRawID3(r io.ReadSeeker) (rawID3Tag, error) {
	tag := rawID3Tag{version: 4}
	if r == nil {
		return tag, errors.New("nil MP3 input")
	}
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return tag, err
	}
	tag.end = end
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return tag, err
	}
	n, err := io.ReadFull(r, tag.header[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return tag, err
	}
	if n >= 3 && string(tag.header[:3]) == "ID3" {
		if n < len(tag.header) {
			return tag, io.ErrUnexpectedEOF
		}
		tag.version = tag.header[3]
		tag.bodyLen, err = readSynchsafe(tag.header[6:])
		if err != nil {
			return tag, err
		}
		tag.start = int64(len(tag.header) + tag.bodyLen)
		hasFooter := tag.version == 4 && tag.header[5]&0x10 != 0
		if hasFooter {
			tag.start += int64(len(tag.footer))
		}
		if tag.start > tag.end {
			return tag, io.ErrUnexpectedEOF
		}
		flags := tag.header[5]
		if tag.version < 2 || tag.version > 4 || tag.header[4] != 0 || flags&^0x10 != 0 || (flags != 0 && !hasFooter) {
			tag.blocked = "unsupported ID3 revision or tag-level flags"
		}
		if tag.blocked == "" {
			body := make([]byte, tag.bodyLen)
			if _, err := io.ReadFull(r, body); err != nil {
				return tag, err
			}
			tag.frames, err = parseRawID3Frames(tag.version, body)
			if err != nil {
				return tag, err
			}
		}
		if hasFooter {
			if _, err := r.Seek(tag.start-int64(len(tag.footer)), io.SeekStart); err != nil {
				return tag, err
			}
			if _, err := io.ReadFull(r, tag.footer[:]); err != nil {
				return tag, err
			}
			if string(tag.footer[:3]) != "3DI" || !bytes.Equal(tag.footer[3:], tag.header[3:]) {
				return tag, errors.New("invalid ID3v2.4 footer")
			}
		}
	}
	if tag.start == 0 {
		tag.header = [10]byte{}
	}
	if tag.end-tag.start >= 128 {
		if _, err := r.Seek(tag.end-128, io.SeekStart); err != nil {
			return tag, err
		}
		trailer := make([]byte, 128)
		if _, err := io.ReadFull(r, trailer); err != nil {
			return tag, err
		}
		if string(trailer[:3]) == "TAG" {
			tag.trailer = trailer
		}
	}
	mediaEnd := tag.end - int64(len(tag.trailer))
	if mediaEnd-tag.start >= 10 {
		if _, err := r.Seek(mediaEnd-10, io.SeekStart); err != nil {
			return tag, err
		}
		var footer [10]byte
		if _, err := io.ReadFull(r, footer[:]); err != nil {
			return tag, err
		}
		if string(footer[:3]) == "3DI" && footer[3] == 4 && footer[5]&0x10 != 0 {
			if size, err := readSynchsafe(footer[6:]); err == nil && mediaEnd-int64(size)-20 >= tag.start {
				tag.blocked = "appended ID3v2.4 tag"
			}
		}
	}
	return tag, nil
}

func parseRawID3Frames(version byte, body []byte) ([]rawID3Frame, error) {
	var frames []rawID3Frame
	idLen, headerLen := 4, 10
	if version == 2 {
		idLen, headerLen = 3, 6
	}
	for len(body) > 0 && body[0] != 0 {
		if len(body) < headerLen {
			return nil, io.ErrUnexpectedEOF
		}
		id := string(body[:idLen])
		for _, c := range id {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return nil, fmt.Errorf("invalid ID3 frame identifier %q", id)
			}
		}
		var size int
		var flags uint16
		switch version {
		case 2:
			size = int(body[3])<<16 | int(body[4])<<8 | int(body[5])
		case 3:
			n := binary.BigEndian.Uint32(body[4:8])
			if n > maxID3BodySize {
				return nil, fmt.Errorf("ID3 frame %s exceeds tag size", id)
			}
			size = int(n)
			flags = binary.BigEndian.Uint16(body[8:10])
		case 4:
			var err error
			size, err = readSynchsafe(body[4:8])
			if err != nil {
				return nil, fmt.Errorf("ID3 frame %s: %w", id, err)
			}
			flags = binary.BigEndian.Uint16(body[8:10])
		}
		if size > len(body)-headerLen {
			return nil, fmt.Errorf("ID3 frame %s: %w", id, io.ErrUnexpectedEOF)
		}
		raw := body[:headerLen+size]
		frames = append(frames, rawID3Frame{id: id, flags: flags, raw: raw, body: raw[headerLen:]})
		body = body[headerLen+size:]
	}
	for _, b := range body {
		if b != 0 {
			return nil, errors.New("nonzero bytes in ID3 padding")
		}
	}
	return frames, nil
}

func readSynchsafe(b []byte) (int, error) {
	var n int
	for _, item := range b {
		if item&0x80 != 0 {
			return 0, errors.New("invalid synchsafe ID3 size")
		}
		n = n<<7 | int(item)
	}
	return n, nil
}

func storeSynchsafe(b []byte, n int) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i] = byte(n & 127)
		n >>= 7
	}
}

func newRawID3Frame(version byte, id string, body []byte) (rawID3Frame, error) {
	headerLen, idLen, sizeLimit := 10, 4, maxID3BodySize
	if version == 2 {
		headerLen, idLen, sizeLimit = 6, 3, 1<<24-1
	}
	if len(id) != idLen || len(body) > sizeLimit {
		return rawID3Frame{}, fmt.Errorf("invalid ID3v2.%d frame %q or size", version, id)
	}
	raw := make([]byte, headerLen+len(body))
	copy(raw, id)
	switch version {
	case 2:
		raw[3], raw[4], raw[5] = byte(len(body)>>16), byte(len(body)>>8), byte(len(body))
	case 3:
		binary.BigEndian.PutUint32(raw[4:8], uint32(len(body)))
	case 4:
		storeSynchsafe(raw[4:8], len(body))
	}
	copy(raw[headerLen:], body)
	return rawID3Frame{id: id, raw: raw, body: raw[headerLen:]}, nil
}

func (tag rawID3Tag) writeChanged(r io.ReadSeeker, w io.Writer, frames []rawID3Frame, trailer []byte) error {
	var used int
	for _, frame := range frames {
		if len(frame.raw) > maxID3BodySize-used {
			return errors.New("ID3 frame data exceeds maximum tag size")
		}
		used += len(frame.raw)
	}
	bodyLen := used
	if tag.bodyLen > maxID3BodySize {
		return errors.New("ID3 padding exceeds maximum tag size")
	}
	if tag.bodyLen > bodyLen {
		bodyLen = tag.bodyLen
	}
	headerLen := 10
	if len(frames) == 0 && tag.start == 0 {
		headerLen = 0
	}
	footerLen := 0
	if string(tag.footer[:3]) == "3DI" {
		footerLen = 10
		bodyLen = used
	}
	if int64(headerLen+bodyLen+footerLen) != tag.start {
		for _, frame := range frames {
			if frameUsesOffsets(frame) {
				return fmt.Errorf("%w: changing tag size with %s offsets", ErrUnsafeID3Edit, frame.id)
			}
		}
	}
	return writeMP3Output(w, func(out io.Writer) error {
		if headerLen != 0 {
			header := tag.header
			copy(header[:3], "ID3")
			header[3] = tag.version
			storeSynchsafe(header[6:], bodyLen)
			if err := writeMP3Bytes(out, header[:]); err != nil {
				return err
			}
			for _, frame := range frames {
				if err := writeMP3Bytes(out, frame.raw); err != nil {
					return err
				}
			}
			var zeros [8192]byte
			for remaining := bodyLen - used; remaining > 0; {
				size := remaining
				if size > len(zeros) {
					size = len(zeros)
				}
				if err := writeMP3Bytes(out, zeros[:size]); err != nil {
					return err
				}
				remaining -= size
			}
			if footerLen != 0 {
				copy(header[:3], "3DI")
				if err := writeMP3Bytes(out, header[:]); err != nil {
					return err
				}
			}
		}
		if err := copyMP3Range(r, out, tag.start, tag.end-tag.start-int64(len(tag.trailer))); err != nil {
			return err
		}
		return writeMP3Bytes(out, trailer)
	})
}

func writeMP3Bytes(w io.Writer, b []byte) error {
	if len(b) == 0 {
		return nil
	}
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}

func copyMP3Range(r io.ReadSeeker, w io.Writer, start, length int64) error {
	if _, err := r.Seek(start, io.SeekStart); err != nil {
		return err
	}
	_, err := io.CopyN(w, r, length)
	return err
}

func frameUsesOffsets(f rawID3Frame) bool {
	switch f.id {
	case "ASPI", "SEEK", "MLLT", "MLL":
		return true
	case "CHAP":
		if f.flags != 0 {
			return true
		}
		_, b, ok := bytes.Cut(f.body, []byte{0})
		return !ok || len(b) < 16 || binary.BigEndian.Uint32(b[8:12]) != ^uint32(0) || binary.BigEndian.Uint32(b[12:16]) != ^uint32(0)
	}
	return false
}
