package mp3meta

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"strings"
	"unicode/utf8"

	core "github.com/gcottom/audiometa-core/v2"
)

func (tag rawID3Tag) decodePicture(f rawID3Frame) (core.Picture, bool) {
	if len(f.body) < 5 || !validID3Encoding(tag.version, f.body[0]) {
		return core.Picture{}, false
	}
	body := f.body[1:]
	var kind string
	if f.id == "PIC" {
		kind = string(body[:3])
		body = body[3:]
		switch kind {
		case "-->":
		case "JPG":
			kind = "image/jpeg"
		case "PNG", "GIF", "BMP":
			kind = "image/" + strings.ToLower(kind)
		default:
			return core.Picture{}, false
		}
	} else {
		mime, rest, ok := bytes.Cut(body, []byte{0})
		if !ok {
			return core.Picture{}, false
		}
		kind, _ = decodeID3Text(mime, 0)
		body = rest
	}
	if len(body) == 0 || body[0] > 20 {
		return core.Picture{}, false
	}
	desc, data, found := takeEncodedText(body[1:], f.body[0])
	description, ok := decodeID3Text(desc, f.body[0])
	if !found || !ok || len(data) == 0 || kind != "-->" && !strings.HasPrefix(kind, "image/") {
		return core.Picture{}, false
	}
	if kind == "-->" {
		link, _ := decodeID3Text(bytes.TrimRight(data, "\x00"), 0)
		data = []byte(link)
	}
	return core.Picture{Role: uint32(body[0]), MIME: kind, Description: description, Data: data}, true
}

func picturesHaveDimensions(values []core.Value) bool {
	for _, value := range values {
		p, _ := value.Picture()
		if p.Width != 0 || p.Height != 0 || p.Depth != 0 || p.Colors != 0 {
			return true
		}
	}
	return false
}

// A fallback can enrich matching APIC/PIC images with attributes that have
// no native slots. It cannot replace conflicting native image content.
func extendsNativePictures(native, extra []core.Value) bool {
	if len(native) != len(extra) || !picturesHaveDimensions(extra) {
		return false
	}
	for i, base := range native {
		p, ok := base.Picture()
		q, more := extra[i].Picture()
		if !ok || !more || p.Role != q.Role || p.MIME != q.MIME ||
			p.Description != q.Description || !bytes.Equal(p.Data, q.Data) {
			return false
		}
	}
	return true
}
func (tag rawID3Tag) renderPictures(id string, values []core.Value) ([]rawID3Frame, bool, error) {
	frames := make([]rawID3Frame, 0, len(values))
	descriptions := make(map[string]bool)
	iconTypes := make(map[uint32]bool)
	for _, value := range values {
		p, ok := value.Picture()
		if !ok ||
			utf8.RuneCountInString(p.Description) > 64 || descriptions[p.Description] {
			return nil, false, nil
		}
		descriptions[p.Description] = true
		if p.Role == 1 || p.Role == 2 {
			if iconTypes[p.Role] {
				return nil, false, nil
			}
			iconTypes[p.Role] = true
		}
		if p.Role == 1 {
			cfg, err := png.DecodeConfig(bytes.NewReader(p.Data))
			if p.MIME != "image/png" || err != nil || cfg.Width != 32 || cfg.Height != 32 {
				return nil, false, nil
			}
		}
		data := p.Data
		if p.MIME == "-->" {
			data, ok = latin1Bytes(string(data))
			if !ok {
				return nil, false, nil
			}
		}
		encoding := selectID3Encoding(tag.version, p.Description)
		body := make([]byte, 1, 1+len(p.MIME)+1+1+2*len(p.Description)+2+len(data))
		body[0] = encoding
		if id == "PIC" {
			switch p.MIME {
			case "-->":
				body = append(body, "-->"...)
			case "image/jpeg":
				body = append(body, "JPG"...)
			case "image/png", "image/gif", "image/bmp":
				body = append(body, strings.ToUpper(p.MIME[len("image/"):])...)
			default:
				return nil, false, nil
			}
		} else {
			mime, ok := latin1Bytes(p.MIME)
			if !ok {
				return nil, false, nil
			}
			body = append(body, mime...)
			body = append(body, 0)
		}
		body = append(body, byte(p.Role))
		body = appendEncodedText(body, encoding, p.Description)
		body = append(body, textDelimiter(encoding)...)
		body = append(body, data...)
		frame, err := newRawID3Frame(tag.version, id, body)
		if err != nil {
			return nil, false, err
		}
		frames = append(frames, frame)
	}
	return frames, true, nil
}

func (tag rawID3Tag) decodeCredits(def core.Definition, f rawID3Frame) []core.Value {
	if len(f.body) == 0 || !validID3Encoding(tag.version, f.body[0]) {
		return nil
	}
	items, ok := decodeID3Strings(f.body[1:], f.body[0], true)
	if !ok {
		return nil
	}
	if len(items)%2 == 1 && items[len(items)-1] == "" {
		items = items[:len(items)-1]
	}
	if len(items)%2 != 0 {
		return nil
	}
	var values []core.Value
	for i := 0; i < len(items); i += 2 {
		v := core.CreditValue(core.Credit{Role: items[i], Name: items[i+1]})
		if core.Validate(def.ID, v) != nil {
			return nil
		}
		values = append(values, v)
	}
	return values
}

func (tag rawID3Tag) renderCredits(def core.Definition, id string, values []core.Value) ([]rawID3Frame, bool, error) {
	if def.ID != core.ProductionCredits && def.ID != core.PerformerCredits {
		return nil, false, nil
	}
	items := make([]string, 0, len(values)*2)
	for _, v := range values {
		credit, ok := v.Credit()
		if !ok {
			return nil, false, nil
		}
		items = append(items, credit.Role, credit.Name)
	}
	body, ok := encodeID3Strings(tag.version, items)
	if !ok {
		return nil, false, nil
	}
	if tag.version < 4 {
		body = append(body, textDelimiter(body[0])...)
	}
	f, err := newRawID3Frame(tag.version, id, body)
	return []rawID3Frame{f}, true, err
}

func (tag rawID3Tag) decodeSyncedLyrics(f rawID3Frame) []core.Value {
	if len(f.body) < 7 || f.body[4] != 2 || f.body[5] != 1 || !validID3Encoding(tag.version, f.body[0]) {
		return nil
	}
	encoding := f.body[0]
	desc, body, found := takeEncodedText(f.body[6:], encoding)
	description, ok := decodeID3Text(desc, encoding)
	if !found || !ok {
		return nil
	}
	value := core.SyncedLyrics{Language: string(f.body[1:4]), Description: description}
	for len(body) != 0 {
		raw, rest, found := takeEncodedText(body, encoding)
		text, ok := decodeID3Text(raw, encoding)
		if !found || !ok || len(rest) < 4 {
			return nil
		}
		value.Cues = append(value.Cues, core.LyricCue{TimeMS: int64(binary.BigEndian.Uint32(rest)), Text: text})
		body = rest[4:]
	}
	v := core.SyncedLyricsValue(value)
	if core.Validate(core.SynchronizedLyrics, v) != nil {
		return nil
	}
	return []core.Value{v}
}

func (tag rawID3Tag) renderSyncedLyrics(id string, values []core.Value) ([]rawID3Frame, bool, error) {
	frames := make([]rawID3Frame, 0, len(values))
	owners := make(map[string]bool)
	for _, value := range values {
		text, ok := value.SyncedLyrics()
		if !ok || len(text.Language) != 3 {
			return nil, false, nil
		}
		for _, c := range text.Language {
			if c < 'a' || c > 'z' {
				return nil, false, nil
			}
		}
		key := text.Language + ":" + text.Description
		if owners[key] {
			return nil, false, nil
		}
		owners[key] = true
		texts := make([]string, 1, len(text.Cues)+1)
		texts[0] = text.Description
		for _, cue := range text.Cues {
			if cue.TimeMS > int64(^uint32(0)) {
				return nil, false, nil
			}
			texts = append(texts, cue.Text)
		}
		encoding := selectID3Encoding(tag.version, texts...)
		body := append([]byte{encoding}, text.Language...)
		body = append(body, 2, 1)
		body = appendEncodedText(body, encoding, text.Description)
		body = append(body, textDelimiter(encoding)...)
		for _, cue := range text.Cues {
			body = appendEncodedText(body, encoding, cue.Text)
			body = append(body, textDelimiter(encoding)...)
			var timestamp [4]byte
			binary.BigEndian.PutUint32(timestamp[:], uint32(cue.TimeMS))
			body = append(body, timestamp[:]...)
		}
		frame, err := newRawID3Frame(tag.version, id, body)
		if err != nil {
			return nil, false, err
		}
		frames = append(frames, frame)
	}
	return frames, true, nil
}

func (tag rawID3Tag) decodeChapter(f rawID3Frame) []core.Value {
	name, b, ok := bytes.Cut(f.body, []byte{0})
	if !ok || len(b) < 16 || frameUsesOffsets(f) {
		return nil
	}
	id, _ := decodeID3Text(name, 0)
	c := core.Chapter{ID: id, StartMS: int64(binary.BigEndian.Uint32(b))}
	end := binary.BigEndian.Uint32(b[4:])
	if end != ^uint32(0) {
		c.EndMS = int64(end)
	}
	if len(b) > 16 {
		sub, err := parseRawID3Frames(tag.version, b[16:])
		if err != nil || len(sub) != 1 || sub[0].id != "TIT2" || sub[0].flags != 0 {
			return nil
		}
		title, _ := core.Lookup(core.Title)
		texts := tag.decodeField(title, "TIT2", sub[0])
		if len(texts) != 1 {
			return nil
		}
		c.Title, _ = texts[0].Text()
	}
	v := core.ChapterValue(c)
	if core.Validate(core.Chapters, v) != nil {
		return nil
	}
	return []core.Value{v}
}

func (tag rawID3Tag) renderChapters(values []core.Value) ([]rawID3Frame, bool, error) {
	frames := make([]rawID3Frame, 0, len(values))
	ids := make(map[string]bool)
	for _, value := range values {
		chapter, ok := value.Chapter()
		if !ok || chapter.StartMS > int64(^uint32(0)) || chapter.EndMS >= int64(^uint32(0)) || ids[chapter.ID] {
			return nil, false, nil
		}
		ids[chapter.ID] = true
		id, ok := latin1Bytes(chapter.ID)
		if !ok {
			return nil, false, nil
		}
		fields := [16]byte{}
		binary.BigEndian.PutUint32(fields[:4], uint32(chapter.StartMS))
		end := uint32(chapter.EndMS)
		if end == 0 {
			end = ^uint32(0)
		}
		binary.BigEndian.PutUint32(fields[4:8], end)
		binary.BigEndian.PutUint32(fields[8:12], ^uint32(0))
		binary.BigEndian.PutUint32(fields[12:16], ^uint32(0))
		body := append(id, 0)
		body = append(body, fields[:]...)
		if chapter.Title != "" {
			text, _ := encodeID3Strings(tag.version, []string{chapter.Title})
			title, err := newRawID3Frame(tag.version, "TIT2", text)
			if err != nil {
				return nil, false, err
			}
			body = append(body, title.raw...)
		}
		frame, err := newRawID3Frame(tag.version, "CHAP", body)
		if err != nil {
			return nil, false, err
		}
		frames = append(frames, frame)
	}
	return frames, true, nil
}
