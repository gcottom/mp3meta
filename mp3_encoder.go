package mp3meta

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"reflect"
	"sort"

	mp3TagLib "github.com/bogem/id3v2/v2"
	core "github.com/gcottom/audiometa-core"
)

func SaveMP3(tag *MP3Tag, w io.Writer) error {
	if tag == nil || tag.reader == nil || w == nil {
		return fmt.Errorf("MP3 save requires a source and destination")
	}
	r := tag.reader
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return err
	}

	source, layout, err := readMP3ID3(r)
	if err != nil {
		return err
	}
	// Validate the unchanged source boundary before choosing a rewrite.
	if layout.audioStart != 0 {
		end, err := r.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}
		if layout.audioStart > end {
			return io.ErrUnexpectedEOF
		}
	}
	if layout.footer != nil {
		if _, err := r.Seek(layout.audioStart-int64(len(layout.footer)), io.SeekStart); err != nil {
			return err
		}
		var actual [10]byte
		if _, err := io.ReadFull(r, actual[:]); err != nil {
			return err
		}
		if actual != *layout.footer {
			return fmt.Errorf("invalid ID3v2.4 footer")
		}
	}
	if _, err := r.Seek(layout.audioStart, io.SeekStart); err != nil {
		return err
	}
	edits, err := legacyMP3Edits(tag, source)
	if err != nil {
		return err
	}
	artChanged := legacyArtChanged(tag, source)
	if len(edits) == 0 && !artChanged {
		return writeMP3Output(w, func(out io.Writer) error {
			if _, err := r.Seek(0, io.SeekStart); err != nil {
				return err
			}
			_, err := io.Copy(out, r)
			return err
		})
	}

	raw, err := readRawID3(r)
	if err != nil {
		return err
	}
	if raw.blocked != "" {
		return fmt.Errorf("%w: %s", ErrUnsafeID3Edit, raw.blocked)
	}
	frames := raw.frames
	for _, frame := range frames {
		if raw.discardOnTagAlter(frame) {
			return fmt.Errorf("%w: prior %s frame requires tag-alter handling", ErrUnsafeID3Edit, frame.id)
		}
	}
	for _, edit := range edits {
		if isPairKey(edit.id) {
			if _, err := raw.existingPair(frames, edit.id); err != nil {
				return err
			}
		}
		var next []rawID3Frame
		if edit.text != "" {
			body, ok := encodeID3Strings(raw.version, []string{edit.text})
			if !ok {
				return fmt.Errorf("invalid text in ID3 frame %s", edit.id)
			}
			frame, err := newRawID3Frame(raw.version, edit.id, body)
			if err != nil {
				return err
			}
			next = []rawID3Frame{frame}
		}
		frames, err = raw.replaceFrames(frames, []string{edit.id}, next, false, nil)
		if err != nil {
			return err
		}
	}
	if artChanged {
		frames, err = raw.replaceLegacyArt(frames, tag.CoverArt, tag.clearCover)
		if err != nil {
			return err
		}
	}
	// The legacy writer normalizes footer-bearing tags to leading-tag-only
	// layout. Keep its audio boundary at the old location where space allows.
	if layout.footer != nil {
		raw.footer = [10]byte{}
		raw.header[5] &^= 0x10
		if raw.bodyLen <= maxID3BodySize-10 {
			raw.bodyLen += 10
		}
	}
	return raw.writeChanged(r, w, frames, raw.trailer)
}

type legacyMP3Edit struct {
	id   string
	text string
}

func legacyMP3Edits(tag *MP3Tag, source *mp3TagLib.Tag) ([]legacyMP3Edit, error) {
	var edits []legacyMP3Edit
	for field, id := range mp3TextFrames {
		current := source.GetTextFrame(id).Text
		var desired string
		switch field {
		case "DiscNumberString", "TrackNumberString":
			number, total, err := legacyMP3Pair(current)
			if err != nil {
				return nil, err
			}
			n, count := tag.TrackNumber, tag.TrackTotal
			if field == "DiscNumberString" {
				n, count = tag.DiscNumber, tag.DiscTotal
			}
			if n == number && count == total {
				continue
			}
			if n != 0 || count != 0 {
				desired = fmt.Sprintf("%d/%d", n, count)
			}
		default:
			desired = reflect.ValueOf(*tag).FieldByName(field).String()
			if current == desired {
				continue
			}
		}
		edits = append(edits, legacyMP3Edit{id: id, text: desired})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].id < edits[j].id })
	return edits, nil
}

func legacyArtChanged(tag *MP3Tag, source *mp3TagLib.Tag) bool {
	if tag.coverDirty || tag.CoverArt != tag.coverSource {
		return true
	}
	if tag.CoverArt != nil {
		if pictures := source.GetFrames("APIC"); len(pictures) != 0 {
			if picture, ok := pictures[0].(mp3TagLib.PictureFrame); ok {
				original, _, err := image.Decode(bytes.NewReader(picture.Picture))
				if err == nil {
					return !reflect.DeepEqual(original, *tag.CoverArt)
				}
			}
		}
	}
	return false
}
func (tag rawID3Tag) replaceLegacyArt(frames []rawID3Frame, cover *image.Image, clearAll bool) ([]rawID3Frame, error) {
	target := -1
	description := "Front cover"
	for i, f := range frames {
		if f.id == "APIC" {
			if p, ok := tag.decodePicture(f); ok {
				description = p.Description
			}
			target = i
			break
		}
	}
	var next []rawID3Frame
	if cover != nil {
		if *cover == nil {
			return nil, fmt.Errorf("nil MP3 cover image")
		}
		var b bytes.Buffer
		if err := jpeg.Encode(&b, *cover, nil); err != nil {
			return nil, err
		}
		picture := core.Picture{Role: 3, MIME: "image/jpeg", Description: description, Data: b.Bytes()}
		var representable bool
		var err error
		next, representable, err = tag.renderPictures("APIC", []core.Value{core.PictureValue(picture)})
		if err != nil {
			return nil, err
		}
		if !representable {
			return nil, fmt.Errorf("%w: MP3 cover cannot be represented in APIC", ErrUnsafeID3Edit)
		}
	}
	if clearAll {
		return tag.replaceFrames(frames, []string{"APIC"}, next, false, nil)
	}
	if target < 0 {
		return append(append([]rawID3Frame(nil), frames...), next...), nil
	}
	if frames[target].flags != 0 {
		return nil, fmt.Errorf("%w: target APIC has preservation or format flags", ErrUnsafeID3Edit)
	}
	result := make([]rawID3Frame, 0, len(frames)+len(next))
	result = append(result, frames[:target]...)
	result = append(result, next...)
	return append(result, frames[target+1:]...), nil
}

func writeMP3Output(w io.Writer, write func(io.Writer) error) error {
	if f, ok := w.(*os.File); ok {
		// The reader and writer may be the same file, including distinct
		// handles. Stage the full rewrite on disk before touching the target.
		tmp, err := os.CreateTemp("", "mp3meta-*.mp3")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		if err := write(tmp); err != nil {
			return err
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if err := f.Truncate(0); err == nil {
			_, err = io.Copy(f, tmp)
			return err
		}
		// Preserve the historical Save(os.Open(path)) workflow. Only reopen
		// the same underlying file; never truncate a replaced path.
		original, err := f.Stat()
		if err != nil {
			return err
		}
		target, err := os.OpenFile(f.Name(), os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		defer target.Close()
		current, err := target.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(original, current) {
			return fmt.Errorf("MP3 output path changed before save")
		}
		if err := target.Truncate(0); err != nil {
			return err
		}
		if _, err := io.Copy(target, tmp); err != nil {
			return err
		}
		if err := target.Close(); err != nil {
			return err
		}
		_, err = f.Seek(0, io.SeekEnd)
		return err
	}
	return write(w)
}

type id3Layout struct {
	audioStart int64
	footer     *[10]byte
}

// readMP3ID3 parses the frames and returns the declared on-disk ID3v2
// extent. Tag.Size describes the re-encoded frames, not the input padding.
func readMP3ID3(r io.Reader) (*mp3TagLib.Tag, id3Layout, error) {
	var header [10]byte
	n, err := io.ReadFull(r, header[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, id3Layout{}, err
	}
	var layout id3Layout
	if n >= 3 && string(header[:3]) == "ID3" {
		if n < len(header) {
			return nil, id3Layout{}, io.ErrUnexpectedEOF
		}
		for _, b := range header[6:] {
			if b&0x80 != 0 {
				return nil, id3Layout{}, fmt.Errorf("invalid ID3v2 tag size")
			}
			layout.audioStart = layout.audioStart<<7 | int64(b)
		}
		layout.audioStart += int64(len(header))
		// ID3v2.4's optional footer is not included in the header size.
		if header[3] == 4 && header[5]&0x10 != 0 {
			layout.audioStart += int64(len(header))
			footer := header
			copy(footer[:3], "3DI")
			layout.footer = &footer
		}
	}
	tag, err := mp3TagLib.ParseReader(io.MultiReader(bytes.NewReader(header[:n]), r), mp3TagLib.Options{Parse: true})
	return tag, layout, err
}
