package mp3meta

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	core "github.com/gcottom/audiometa-core/v2"
)

func (tag rawID3Tag) saveMetadata(r io.ReadSeeker, w io.Writer, changes core.Changes) error {
	if tag.blocked != "" {
		return fmt.Errorf("%w: %s", ErrUnsafeID3Edit, tag.blocked)
	}
	var known []bool
	checkUnknown := changes.ClearUnknown
	for _, f := range tag.frames {
		checkUnknown = checkUnknown || tag.discardOnTagAlter(f)
	}
	if checkUnknown {
		_, known, _ = readMP3Metadata(tag)
	}
	frames := make([]rawID3Frame, 0, len(tag.frames))
	for i, frame := range tag.frames {
		if known != nil && !known[i] {
			if changes.ClearUnknown {
				continue
			}
			if tag.discardOnTagAlter(frame) {
				return fmt.Errorf("%w: unknown %s requires discard when the tag changes", ErrUnsafeID3Edit, frame.id)
			}
		}
		frames = append(frames, frame)
	}
	trailer := append([]byte(nil), tag.trailer...)
	reader := tag.reader()
	for _, pair := range [][2]core.Field{
		{core.TrackNumber, core.TrackTotal},
		{core.DiscNumber, core.DiscTotal},
		{core.MovementNumber, core.MovementTotal},
	} {
		num, numChanged := changes.Fields[pair[0]]
		total, totalChanged := changes.Fields[pair[1]]
		if !numChanged && !totalChanged {
			continue
		}
		if !numChanged {
			d, _ := core.Lookup(pair[0])
			num = reader.project(d, nil)
		}
		if !totalChanged {
			d, _ := core.Lookup(pair[1])
			total = reader.project(d, nil)
		}
		var err error
		frames, err = tag.updatePair(frames, pair, num, total, numChanged, totalChanged, changes.ClearUnknown)
		if err != nil {
			return err
		}
		if numChanged && len(num) == 0 {
			clearID3v1Field(trailer, pair[0])
		}
	}
	for _, def := range core.Fields() {
		values, changed := changes.Fields[def.ID]
		if !changed || isPairField(def.ID) {
			continue
		}
		var err error
		frames, err = tag.updateField(frames, def, values, changes.ClearUnknown)
		if err != nil {
			return err
		}
		if len(values) == 0 {
			clearID3v1Field(trailer, def.ID)
		}
	}
	for _, key := range core.SortedCustomKeys(changes.Custom) {
		next, err := tag.renderPrivate(core.CustomName(key), changes.Custom[key])
		if err != nil {
			return err
		}
		frames, err = tag.replaceFrames(frames, []string{tag.customKey(key)}, next, changes.ClearUnknown, nil)
		if err != nil {
			return err
		}
	}
	return tag.writeChanged(r, w, frames, trailer)
}

func (tag rawID3Tag) discardOnTagAlter(f rawID3Frame) bool {
	if tag.version == 3 {
		return f.flags&0x8000 != 0
	}
	if tag.version == 4 {
		return f.flags&0x4000 != 0
	}
	return false
}

func isPairField(field core.Field) bool {
	switch field {
	case core.TrackNumber, core.TrackTotal, core.DiscNumber, core.DiscTotal, core.MovementNumber, core.MovementTotal:
		return true
	}
	return false
}

func (tag rawID3Tag) updatePair(frames []rawID3Frame, pair [2]core.Field, num, total []core.Value, numChanged, totalChanged, clearUnknown bool) ([]rawID3Frame, error) {
	numberDef, _ := core.Lookup(pair[0])
	totalDef, _ := core.Lookup(pair[1])
	numberKey := numberDef.Mapping(tag.format()).Key
	totalKey := totalDef.Mapping(tag.format()).Key
	keys := make([]string, 0, 3)
	if numChanged {
		keys = append(keys, tag.fallbackKey(pair[0]))
	}
	if totalChanged {
		keys = append(keys, tag.fallbackKey(pair[1]))
	}
	var next []rawID3Frame
	if isPairKey(numberKey) && numberKey == totalKey {
		keys = append(keys, numberKey)
		var parts []string
		if !numChanged || !totalChanged {
			var err error
			parts, err = tag.existingPair(frames, numberKey)
			if err != nil {
				return nil, err
			}
		}
		if len(num) != 0 {
			paired, err := tag.makePair(numberKey, num[0], total, parts, numChanged, totalChanged)
			if err != nil {
				return nil, err
			}
			return tag.replaceFrames(frames, keys, paired, clearUnknown, nil)
		}
		if !totalChanged && len(total) != 0 {
			existing, found := tag.existingPairFallback(frames, totalDef)
			if found {
				if len(existing) == 0 || !existing[0].Equal(total[0]) {
					return nil, fmt.Errorf("%w: removing %s would change %s precedence", ErrUnsafeID3Edit, pair[0], pair[1])
				}
				return tag.replaceFrames(frames, keys, nil, clearUnknown, nil)
			}
			totalChanged = true // Move the untouched total out of the removed native pair.
		}
	}
	if numChanged {
		f, err := tag.renderFallback(pair[0], num)
		if err != nil {
			return nil, err
		}
		next = append(next, f...)
	}
	if totalChanged {
		f, err := tag.renderFallback(pair[1], total)
		if err != nil {
			return nil, err
		}
		next = append(next, f...)
	}
	return tag.replaceFrames(frames, keys, next, clearUnknown, nil)
}

func (tag rawID3Tag) existingPair(frames []rawID3Frame, key string) ([]string, error) {
	var parts []string
	for _, f := range frames {
		if f.id != key {
			continue
		}
		if len(parts) != 0 || f.flags != 0 || len(f.body) == 0 || !validID3Encoding(tag.version, f.body[0]) {
			return nil, fmt.Errorf("%w: %s has duplicate or opaque paired values", ErrUnsafeID3Edit, key)
		}
		text, ok := decodeID3Strings(f.body[1:], f.body[0], tag.version == 4)
		if !ok || len(text) != 1 {
			return nil, fmt.Errorf("%w: %s has invalid paired values", ErrUnsafeID3Edit, key)
		}
		parts = strings.Split(text[0], "/")
		if len(parts) > 2 {
			return nil, fmt.Errorf("%w: %s has invalid paired values", ErrUnsafeID3Edit, key)
		}
		for _, s := range parts {
			if _, err := strconv.ParseInt(s, 10, 64); err != nil || !isASCIIDigits(s) {
				return nil, fmt.Errorf("%w: %s has invalid paired values", ErrUnsafeID3Edit, key)
			}
		}
	}
	return parts, nil
}

func (tag rawID3Tag) existingPairFallback(frames []rawID3Frame, def core.Definition) ([]core.Value, bool) {
	name := tag.fallbackKey(def.ID)
	found := false
	for _, f := range frames {
		if strings.EqualFold(tag.frameKey(f), name) {
			found = true
			if values := tag.decodeField(def, name, f); len(values) > 0 {
				return values, true
			}
		}
	}
	return nil, found
}

func (tag rawID3Tag) makePair(key string, num core.Value, total []core.Value, parts []string, numChanged, totalChanged bool) ([]rawID3Frame, error) {
	number, _ := num.Int()
	text := strconv.FormatInt(number, 10)
	if !numChanged && len(parts) > 0 {
		text = parts[0]
	}
	if len(total) != 0 {
		n, _ := total[0].Int()
		count := strconv.FormatInt(n, 10)
		if !totalChanged && len(parts) > 1 {
			count = parts[1]
		}
		text += "/" + count
	}
	body, _ := encodeID3Strings(tag.version, []string{text})
	f, err := newRawID3Frame(tag.version, key, body)
	return []rawID3Frame{f}, err
}

func (tag rawID3Tag) updateField(frames []rawID3Frame, def core.Definition, values []core.Value, clearUnknown bool) ([]rawID3Frame, error) {
	mapping := def.Mapping(tag.format())
	keys := []string{mapping.Key, tag.fallbackKey(def.ID)}
	if def.ID == core.RecordingDate && tag.version < 4 {
		if tag.version == 2 {
			keys = append(keys, "TDA", "TIM")
		} else {
			keys = append(keys, "TDAT", "TIME")
		}
	}
	if def.ID == core.Chapters {
		for _, f := range frames {
			if f.id == "CTOC" {
				return nil, fmt.Errorf("%w: chapters are referenced by an ID3 table of contents", ErrUnsafeID3Edit)
			}
		}
	}
	preserve := func(f rawID3Frame) bool {
		switch def.Kind {
		case core.KindPicture:
			if f.id == "APIC" || f.id == "PIC" {
				_, ok := tag.decodePicture(f)
				return f.flags == 0 && !ok
			}
			return false
		case core.KindChapter, core.KindSyncedLyrics, core.KindCredit:
			return f.flags == 0 && len(tag.decodeField(def, tag.frameKey(f), f)) == 0
		default:
			return false
		}
	}
	if len(values) == 0 {
		keys = append(keys, mapping.Aliases...)
		return tag.replaceFrames(frames, keys, nil, clearUnknown, preserve)
	}
	var (
		next   []rawID3Frame
		native bool
		err    error
	)
	if def.ID == core.RecordingDate && tag.version < 4 {
		next, native, err = tag.renderLegacyRecording(mapping.Key, values[0])
	} else {
		next, native, err = tag.renderPlain(def, mapping.Key, values)
	}
	if err != nil {
		return nil, err
	}
	if native {
		for _, f := range frames {
			if tag.frameKey(f) == mapping.Key && preserve(f) {
				native = false
				break
			}
		}
	}
	if !native {
		for _, alias := range mapping.Aliases {
			for _, f := range frames {
				if strings.EqualFold(tag.frameKey(f), alias) && len(tag.decodeField(def, alias, f)) != 0 {
					return nil, fmt.Errorf("%w: field %s has an alias that would hide its fallback", ErrUnsafeID3Edit, def.ID)
				}
			}
		}
		next, err = tag.renderFallback(def.ID, values)
		if err != nil {
			return nil, err
		}
	}
	if native && def.ID == core.Artwork && picturesHaveDimensions(values) {
		extra, err := tag.renderFallback(def.ID, values)
		if err != nil {
			return nil, err
		}
		next = append(next, extra...)
	}
	if native && def.ID == core.Rating && len(next) == 1 {
		for _, frame := range frames {
			if tag.frameKey(frame) != mapping.Key {
				continue
			}
			_, ratingCounter, ok := bytes.Cut(frame.body, []byte{0})
			if ok && len(ratingCounter) > 1 {
				next[0], err = newRawID3Frame(tag.version, next[0].id, append(next[0].body, ratingCounter[1:]...))
				if err != nil {
					return nil, err
				}
				break
			}
		}
	}
	return tag.replaceFrames(frames, keys, next, clearUnknown, preserve)
}

func (tag rawID3Tag) renderLegacyRecording(key string, value core.Value) ([]rawID3Frame, bool, error) {
	date, _ := value.Date()
	text := string(date)
	if len(text) != 4 && len(text) != 10 && len(text) != 16 {
		return nil, false, nil
	}
	ids := []string{key, "TDAT", "TIME"}
	if tag.version == 2 {
		ids = []string{key, "TDA", "TIM"}
	}
	texts := []string{text[:4]}
	if len(text) >= 10 {
		texts = append(texts, text[8:10]+text[5:7])
	}
	if len(text) == 16 {
		texts = append(texts, text[11:13]+text[14:16])
	}
	var frames []rawID3Frame
	for i, s := range texts {
		data, _ := encodeID3Strings(tag.version, []string{s})
		frame, err := newRawID3Frame(tag.version, ids[i], data)
		if err != nil {
			return nil, false, err
		}
		frames = append(frames, frame)
	}
	return frames, true, nil
}

func (tag rawID3Tag) replaceFrames(frames []rawID3Frame, keys []string, next []rawID3Frame, clearUnknown bool, preserve func(rawID3Frame) bool) ([]rawID3Frame, error) {
	result := make([]rawID3Frame, 0, len(frames)+len(next))
	inserted := false
	for _, frame := range frames {
		key := tag.frameKey(frame)
		match := false
		for _, target := range keys {
			if target != "" && strings.EqualFold(target, key) {
				match = true
				break
			}
		}
		if !match || preserve != nil && preserve(frame) {
			result = append(result, frame)
			continue
		}
		if frame.flags != 0 && !clearUnknown {
			return nil, fmt.Errorf("%w: target %s has preservation or format flags", ErrUnsafeID3Edit, frame.id)
		}
		if !inserted {
			result = append(result, next...)
			inserted = true
		}
	}
	if !inserted {
		result = append(result, next...)
	}
	return result, nil
}
