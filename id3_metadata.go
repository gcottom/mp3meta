package mp3meta

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"

	core "github.com/gcottom/audiometa-core/v2"
)

// OpenMetadata opens an MP3 with its canonical fields, custom fields, and
// preserving save operation. The reader remains owned by the caller.
func OpenMetadata(r io.ReadSeeker) (*core.Editor, error) {
	original, err := readRawID3(r)
	if err != nil {
		return nil, fmt.Errorf("read MP3 metadata: %w", err)
	}
	fields, _, custom := readMP3Metadata(original)
	sourceSize := original.end
	return core.NewEditor(fields, custom, func(w io.Writer, changes core.Changes) error {
		if changes.Empty() {
			return writeMP3Output(w, func(out io.Writer) error {
				return copyMP3Range(r, out, 0, sourceSize)
			})
		}
		current, err := readRawID3(r)
		if err != nil {
			return err
		}
		return current.saveMetadata(r, w, changes)
	}), nil
}

func (tag rawID3Tag) format() core.Format {
	switch tag.version {
	case 2:
		return core.MP3v22
	case 3:
		return core.MP3v23
	default:
		return core.MP3v24
	}
}

func (tag rawID3Tag) textID() string {
	if tag.version == 2 {
		return "TXX"
	}
	return "TXXX"
}

func (tag rawID3Tag) fallbackKey(field core.Field) string {
	return tag.textID() + ":" + core.FallbackName(field)
}

func (tag rawID3Tag) customKey(key string) string {
	return tag.textID() + ":" + core.CustomName(key)
}

type id3FieldReader struct {
	tag    rawID3Tag
	frames map[string][]int
}

func (tag rawID3Tag) reader() id3FieldReader {
	index := make(map[string][]int)
	for i, f := range tag.frames {
		key := strings.ToUpper(tag.frameKey(f))
		index[key] = append(index[key], i)
	}
	return id3FieldReader{tag: tag, frames: index}
}

func readMP3Metadata(tag rawID3Tag) (map[core.Field][]core.Value, []bool, map[string][]string) {
	fields := make(map[core.Field][]core.Value)
	custom := make(map[string][]string)
	known := make([]bool, len(tag.frames))
	reader := tag.reader()
	for _, def := range core.Fields() {
		if values := reader.project(def, known); len(values) != 0 {
			fields[def.ID] = values
		}
	}
	for i, frame := range tag.frames {
		id, description, _ := strings.Cut(tag.frameKey(frame), ":")
		if id != tag.textID() {
			continue
		}
		key, ok := core.ParseCustomName(description)
		if !ok {
			continue
		}
		text, ok := tag.describedText(frame, true)
		if !ok {
			continue
		}
		known[i] = true
		custom[key] = append(custom[key], text...)
	}
	return fields, known, custom
}

func (r id3FieldReader) project(def core.Definition, known []bool) []core.Value {
	var values []core.Value
	mapping := def.Mapping(r.tag.format())
	fallback := r.tag.fallbackKey(def.ID)
	keys := append([]string{mapping.Key}, mapping.Aliases...)
	if mapping.Key != fallback {
		keys = append(keys, fallback)
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		decoded, consumed := r.readField(def, key)
		if len(decoded) == 0 {
			continue
		}
		if known != nil {
			for _, index := range consumed {
				known[index] = true
			}
		}
		if len(values) == 0 || def.ID == core.Artwork && key == fallback && extendsNativePictures(values, decoded) {
			values = decoded
		}
	}
	if len(values) == 0 && len(r.tag.trailer) != 0 {
		if value, ok := readID3v1Field(r.tag.trailer, def.ID); ok {
			values = []core.Value{value}
		}
	}
	return values
}
func (r id3FieldReader) readField(def core.Definition, key string) ([]core.Value, []int) {
	var values []core.Value
	var consumed []int
	for _, index := range r.frames[strings.ToUpper(key)] {
		decoded := r.tag.decodeField(def, key, r.tag.frames[index])
		if len(decoded) == 0 {
			continue
		}
		consumed = append(consumed, index)
		values = append(values, decoded...)
	}
	if !def.Multiple && len(values) > 1 {
		values = values[:1]
	}
	return values, consumed
}

func (tag rawID3Tag) frameKey(f rawID3Frame) string {
	if f.flags&0xff != 0 {
		return f.id
	}
	switch f.id {
	case "TXXX", "TXX", "WXXX", "WXX":
		if len(f.body) > 0 && validID3Encoding(tag.version, f.body[0]) {
			raw, _, more := takeEncodedText(f.body[1:], f.body[0])
			if desc, ok := decodeID3Text(raw, f.body[0]); more && ok {
				return f.id + ":" + desc
			}
		}
	case "UFID", "UFI":
		owner, _, ok := bytes.Cut(f.body, []byte{0})
		if ok && len(owner) != 0 {
			return f.id + ":" + string(owner)
		}
	case "POPM", "POP":
		owner, _, ok := bytes.Cut(f.body, []byte{0})
		if ok && string(owner) != id3RatingOwner {
			return f.id + ":" + string(owner)
		}
	case "COMM", "COM", "USLT", "ULT", "USER":
		if _, ok := tag.localizedText(f, f.id == "USER"); !ok {
			return f.id + ":other"
		}
	}
	return f.id
}

func (tag rawID3Tag) describedText(f rawID3Frame, fallback bool) ([]string, bool) {
	if f.flags&0xff != 0 || len(f.body) == 0 || !validID3Encoding(tag.version, f.body[0]) {
		return nil, false
	}
	values, ok := decodeID3Strings(f.body[1:], f.body[0], true)
	if !ok || len(values) == 0 {
		return nil, false
	}
	values = values[1:]
	if len(values) == 0 {
		values = []string{""}
	}
	if fallback {
		return unpackID3Fallback(values)
	}
	return values, true
}

func (tag rawID3Tag) localizedText(f rawID3Frame, noDescription bool) ([]string, bool) {
	if len(f.body) < 4 || !validID3Encoding(tag.version, f.body[0]) {
		return nil, false
	}
	switch strings.ToLower(string(f.body[1:4])) {
	case "eng", "und", "xxx":
	default:
		return nil, false
	}
	text := f.body[4:]
	if !noDescription {
		desc, rest, more := takeEncodedText(text, f.body[0])
		decoded, ok := decodeID3Text(desc, f.body[0])
		if !ok || !more || decoded != "" {
			return nil, false
		}
		text = rest
		if f.body[0] == 1 {
			text = applyUTF16BOM(text, desc)
		}
	}
	return decodeID3Strings(text, f.body[0], tag.version == 4 && (f.id == "COMM" || f.id == "COM"))
}

func (tag rawID3Tag) decodeField(def core.Definition, key string, f rawID3Frame) []core.Value {
	if f.flags&0xff != 0 {
		return nil
	}
	if f.id == "APIC" || f.id == "PIC" {
		if p, ok := tag.decodePicture(f); ok {
			return []core.Value{core.PictureValue(p)}
		}
		return nil
	}
	if f.id == "PCNT" || f.id == "CNT" {
		if value, ok := decodeCounter(f.body); ok {
			return []core.Value{core.IntValue(value)}
		}
		return nil
	}
	if f.id == "POPM" || f.id == "POP" {
		_, b, ok := bytes.Cut(f.body, []byte{0})
		if ok && len(b) >= 1 {
			return []core.Value{core.IntValue((int64(b[0])*100 + 127) / 255)}
		}
		return nil
	}
	if f.id == "PCST" {
		if len(f.body) == 0 || len(f.body) == 4 {
			return []core.Value{core.BoolValue(true)}
		}
		return nil
	}
	if f.id == "TIPL" || f.id == "TMCL" || f.id == "IPLS" || f.id == "IPL" {
		return tag.decodeCredits(def, f)
	}
	if f.id == "SYLT" || f.id == "SLT" {
		return tag.decodeSyncedLyrics(f)
	}
	if f.id == "CHAP" {
		return tag.decodeChapter(f)
	}
	var texts []string
	switch f.id {
	case "TXXX", "TXX":
		texts, _ = tag.describedText(f, key == tag.fallbackKey(def.ID))
	case "WXXX", "WXX":
		if len(f.body) > 0 {
			_, b, _ := takeEncodedText(f.body[1:], f.body[0])
			if text, ok := decodeID3Text(bytes.TrimRight(b, "\x00"), 0); ok {
				texts = []string{text}
			}
		}
	case "UFID", "UFI":
		if _, b, ok := bytes.Cut(f.body, []byte{0}); ok && len(b) <= 64 {
			texts = []string{string(b)}
		}
	case "COMM", "COM", "USLT", "ULT", "USER":
		texts, _ = tag.localizedText(f, f.id == "USER")
	default:
		switch {
		case strings.HasPrefix(f.id, "T") || f.id == "MVNM" || f.id == "MVN" || f.id == "GRP1" || isPairKey(f.id):
			if len(f.body) > 0 && validID3Encoding(tag.version, f.body[0]) {
				texts, _ = decodeID3Strings(f.body[1:], f.body[0], tag.version == 4)
				if len(texts) == 1 && tag.version < 4 && legacySlashList(f.id) {
					texts = strings.Split(texts[0], "/")
				}
			}
		case strings.HasPrefix(f.id, "W"):
			if text, ok := decodeID3Text(bytes.TrimRight(f.body, "\x00"), 0); ok {
				texts = []string{text}
			}
		default:
			return nil
		}
	}
	if len(texts) != 0 && isPairKey(f.id) {
		index := 0
		if def.ID == core.TrackTotal || def.ID == core.DiscTotal || def.ID == core.MovementTotal {
			index = 1
		}
		items := strings.Split(texts[0], "/")
		if len(items) > 2 || index >= len(items) {
			return nil
		}
		texts = []string{items[index]}
	}
	if len(texts) == 0 {
		return nil
	}
	var values []core.Value
	for _, text := range texts {
		if def.ID == core.Genre && key != tag.fallbackKey(def.ID) {
			text = decodeID3Genre(text)
		}
		if def.ID == core.RecordingDate && (f.id == "TYER" || f.id == "TYE") && len(text) == 4 {
			text = tag.legacyRecordingDate(text)
		}
		if key != tag.fallbackKey(def.ID) {
			switch def.ID {
			case core.ReplayGainTrackGain, core.ReplayGainAlbumGain, core.ReplayGainReferenceLoudness:
				text = stripReplayGainUnit(text)
			case core.ISRC:
				text = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(text), "-", ""))
			}
		}
		if value, err := core.Decode(def.ID, text); err == nil {
			values = append(values, value)
		}
	}
	return values
}

func stripReplayGainUnit(text string) string {
	text = strings.TrimSpace(text)
	for _, suffix := range []string{" db spl", " db"} {
		if strings.HasSuffix(strings.ToLower(text), suffix) {
			return strings.TrimSpace(text[:len(text)-len(suffix)])
		}
	}
	return text
}
func (tag rawID3Tag) legacyRecordingDate(year string) string {
	var dayMonth, hourMinute string
	for _, f := range tag.frames {
		if len(f.body) != 5 || f.flags&0xff != 0 || f.body[0] != 0 && f.body[0] != 3 {
			continue
		}
		switch f.id {
		case "TDAT", "TDA":
			if dayMonth == "" {
				dayMonth = string(f.body[1:])
			}
		case "TIME", "TIM":
			if hourMinute == "" {
				hourMinute = string(f.body[1:])
			}
		}
	}
	if dayMonth == "" {
		return year
	}
	date := year + "-" + dayMonth[2:] + "-" + dayMonth[:2]
	if _, err := core.Decode(core.RecordingDate, date); err != nil {
		return year
	}
	if hourMinute != "" {
		full := date + "T" + hourMinute[:2] + ":" + hourMinute[2:]
		if _, err := core.Decode(core.RecordingDate, full); err == nil {
			return full
		}
	}
	return date
}

func legacySlashList(id string) bool {
	switch id {
	case "TPE1", "TP1", "TCOM", "TCM", "TEXT", "TXT", "TOPE", "TOA", "TOLY", "TOL":
		return true
	}
	return false
}

func isPairKey(id string) bool {
	switch id {
	case "TRCK", "TRK", "TPOS", "TPA", "MVIN", "MVI":
		return true
	}
	return false
}

func decodeCounter(b []byte) (int64, bool) {
	if len(b) < 4 {
		return 0, false
	}
	for len(b) > 8 && b[0] == 0 {
		b = b[1:]
	}
	if len(b) > 8 {
		return 0, false
	}
	var data [8]byte
	copy(data[len(data)-len(b):], b)
	count := int64(binary.BigEndian.Uint64(data[:]))
	return count, count >= 0
}

const id3RatingOwner = "rating@audiometa.invalid"

func (tag rawID3Tag) renderPlain(def core.Definition, key string, values []core.Value) ([]rawID3Frame, bool, error) {
	id, desc, _ := strings.Cut(key, ":")
	if key == "" || key == tag.fallbackKey(def.ID) {
		return nil, false, nil
	}
	if id == "APIC" || id == "PIC" {
		return tag.renderPictures(id, values)
	}
	if id == "TIPL" || id == "TMCL" || id == "IPLS" || id == "IPL" {
		return tag.renderCredits(def, id, values)
	}
	if id == "SYLT" || id == "SLT" {
		return tag.renderSyncedLyrics(id, values)
	}
	if id == "CHAP" {
		return tag.renderChapters(values)
	}
	texts := make([]string, 0, len(values))
	for _, value := range values {
		s, err := core.Encode(def.ID, value)
		if err != nil {
			return nil, false, err
		}
		if strings.ContainsRune(s, 0) {
			return nil, false, nil
		}
		switch def.ID {
		case core.ReplayGainTrackGain, core.ReplayGainAlbumGain, core.ReplayGainReferenceLoudness:
			s += " dB"
		}
		texts = append(texts, s)
	}
	frames := make([]rawID3Frame, 0, 1)
	add := func(body []byte) error {
		f, err := newRawID3Frame(tag.version, id, body)
		if err == nil {
			frames = append(frames, f)
		}
		return err
	}
	switch id {
	case "PCNT", "CNT":
		count, ok := values[0].Int()
		if !ok || count < 0 {
			return nil, false, nil
		}
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], uint64(count))
		first := 0
		for first < 4 && raw[first] == 0 {
			first++
		}
		err := add(raw[first:])
		return frames, true, err
	case "PCST":
		podcast, _ := values[0].Bool()
		if !podcast {
			return nil, false, nil
		}
		err := add([]byte{0, 0, 0, 0})
		return frames, true, err
	case "POPM", "POP":
		rating, _ := values[0].Int()
		body := append([]byte(id3RatingOwner), 0, byte((rating*255+50)/100))
		err := add(body)
		return frames, true, err
	case "UFID", "UFI":
		if len(texts) != 1 || len(texts[0]) > 64 {
			return nil, false, nil
		}
		err := add(append(append([]byte(desc), 0), texts[0]...))
		return frames, true, err
	case "TXXX", "TXX":
		if tag.version < 4 && len(texts) > 1 {
			return nil, false, nil
		}
		frame, err := id3DescriptionFrame(tag.version, id, desc, texts)
		return []rawID3Frame{frame}, true, err
	case "WXXX", "WXX":
		if len(texts) != 1 {
			return nil, false, nil
		}
		url, ok := latin1Bytes(texts[0])
		if !ok {
			return nil, false, nil
		}
		encoding := selectID3Encoding(tag.version, desc)
		body := appendEncodedText([]byte{encoding}, encoding, desc)
		body = append(body, textDelimiter(encoding)...)
		err := add(append(body, url...))
		return frames, true, err
	case "COMM", "COM", "USLT", "ULT", "USER":
		list := tag.version == 4 && (id == "COMM" || id == "COM")
		if len(texts) > 1 && !list {
			return nil, false, nil
		}
		encoding := selectID3Encoding(tag.version, texts...)
		body := append([]byte{encoding}, "eng"...)
		if id != "USER" {
			body = appendEncodedText(body, encoding, "")
			body = append(body, textDelimiter(encoding)...)
		}
		for i, text := range texts {
			if i != 0 {
				body = append(body, textDelimiter(encoding)...)
			}
			body = appendEncodedText(body, encoding, text)
		}
		if list {
			body = append(body, textDelimiter(encoding)...)
		}
		err := add(body)
		return frames, true, err
	}
	if strings.HasPrefix(id, "W") {
		if len(texts) != 1 && def.ID != core.OfficialArtistURL {
			return nil, false, nil
		}
		for _, text := range texts {
			b, ok := latin1Bytes(text)
			if !ok {
				return nil, false, nil
			}
			if err := add(b); err != nil {
				return nil, false, err
			}
		}
		return frames, true, nil
	}
	if !strings.HasPrefix(id, "T") && id != "MVNM" && id != "MVN" && id != "GRP1" {
		return nil, false, nil
	}
	if !canWriteNativeText(def.ID, id, texts) {
		return nil, false, nil
	}
	if tag.version < 4 && len(texts) > 1 {
		if !legacySlashList(id) {
			return nil, false, nil
		}
		for _, text := range texts {
			if strings.Contains(text, "/") {
				return nil, false, nil
			}
		}
		texts = []string{strings.Join(texts, "/")}
	} else if tag.version < 4 && legacySlashList(id) && strings.Contains(texts[0], "/") {
		return nil, false, nil
	}
	if def.ID == core.Compilation {
		state, _ := values[0].Bool()
		texts = []string{"0"}
		if state {
			texts[0] = "1"
		}
	}
	body, ok := encodeID3Strings(tag.version, texts)
	if !ok {
		return nil, false, nil
	}
	err := add(body)
	return frames, true, err
}

func canWriteNativeText(field core.Field, id string, texts []string) bool {
	switch field {
	case core.Copyright, core.InitialProducer:
		return len(texts[0]) >= 5 && isASCIIDigits(texts[0][:4]) && texts[0][4] == ' '
	case core.RecordingDate, core.OriginalReleaseDate, core.ReleaseDate, core.EncodingDate, core.TaggingDate:
		if id == "TYER" || id == "TYE" || id == "TORY" || id == "TOR" {
			return len(texts[0]) == 4
		}
		if id == "TDRC" || id == "TDOR" || id == "TDRL" || id == "TDEN" || id == "TDTG" {
			return len(texts[0]) <= 19 && !strings.ContainsAny(texts[0][4:], "+Z")
		}
	case core.Genre:
		for _, value := range texts {
			if value != decodeID3Genre(value) {
				return false
			}
		}
	case core.Language:
		for _, value := range texts {
			if len(value) != 3 {
				return false
			}
			for _, c := range value {
				if c < 'a' || c > 'z' {
					return false
				}
			}
		}
	case core.MusicalKey:
		value := texts[0]
		if value == "o" {
			return true
		}
		if len(value) == 0 || value[0] < 'A' || value[0] > 'G' {
			return false
		}
		suffix := strings.TrimSuffix(value[1:], "m")
		return suffix == "" || suffix == "b" || suffix == "#"
	}
	return true
}

func isASCIIDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (tag rawID3Tag) renderFallback(field core.Field, values []core.Value) ([]rawID3Frame, error) {
	var texts []string
	for _, value := range values {
		text, err := core.Encode(field, value)
		if err != nil {
			return nil, err
		}
		texts = append(texts, text)
	}
	return tag.renderPrivate(core.FallbackName(field), texts)
}

func (tag rawID3Tag) renderPrivate(name string, texts []string) ([]rawID3Frame, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if tag.version < 4 && len(texts) > 1 || len(texts) == 1 && strings.HasPrefix(texts[0], id3FallbackPrefix) {
		var err error
		texts, err = packID3Fallback(texts)
		if err != nil {
			return nil, err
		}
	}
	frame, err := id3DescriptionFrame(tag.version, tag.textID(), name, texts)
	return []rawID3Frame{frame}, err
}
func decodeID3Genre(text string) string {
	number := text
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		number = text[1 : len(text)-1]
	}
	if isASCIIDigits(number) {
		if n, err := strconv.Atoi(number); err == nil && n >= 0 && n < len(id3v1Genres) {
			return id3v1Genres[n]
		}
	}
	switch text {
	case "RX", "(RX)":
		return "Remix"
	case "CR", "(CR)":
		return "Cover"
	}
	return text
}
