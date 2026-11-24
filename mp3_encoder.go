package mp3meta

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"io"
	"os"
	"reflect"

	mp3TagLib "github.com/bogem/id3v2/v2"
)

func SaveMP3(tag *MP3Tag, w io.Writer) error {
	r := tag.reader
	if _, err := r.Seek(0, 0); err != nil {
		return err
	}

	mp3Tag, layout, err := readMP3ID3(r)
	if err != nil {
		return err
	}
	for k, v := range mp3TextFrames {
		if k == "DiscNumberString" {
			textFrame := mp3TagLib.TextFrame{
				Encoding: mp3TagLib.EncodingUTF8,
				Text:     fmt.Sprintf("%d/%d", tag.DiscNumber, tag.DiscTotal),
			}
			mp3Tag.AddFrame(v, textFrame)
			continue
		}
		if k == "TrackNumberString" {
			textFrame := mp3TagLib.TextFrame{
				Encoding: mp3TagLib.EncodingUTF8,
				Text:     fmt.Sprintf("%d/%d", tag.TrackNumber, tag.TrackTotal),
			}
			mp3Tag.AddFrame(v, textFrame)
			continue
		}
		if reflect.ValueOf(*tag).FieldByName(k).IsZero() {
			mp3Tag.DeleteFrames(v)
			continue
		}
		textFrame := mp3TagLib.TextFrame{
			Encoding: mp3TagLib.EncodingUTF8,
			Text:     reflect.ValueOf(*tag).FieldByName(k).String(),
		}
		mp3Tag.AddFrame(v, textFrame)
	}
	mp3Tag.DeleteFrames("APIC")
	buf := new(bytes.Buffer)
	if tag.CoverArt != nil {
		if err := jpeg.Encode(buf, *tag.CoverArt, nil); err == nil {
			mp3Tag.AddAttachedPicture(mp3TagLib.PictureFrame{
				Encoding:    mp3TagLib.EncodingUTF8,
				MimeType:    "image/jpeg",
				PictureType: mp3TagLib.PTFrontCover,
				Description: "Front cover",
				Picture:     buf.Bytes(),
			})
		}
	}
	// bytes outside the declared ID3v2 header must be retained verbatim.
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
	write := func(out io.Writer) error {
		if _, err := mp3Tag.WriteTo(out); err != nil {
			return err
		}
		_, err := io.Copy(out, r)
		return err
	}
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
