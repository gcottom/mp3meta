package mp3meta

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"reflect"
	"regexp"
	"strconv"

	mp3TagLib "github.com/bogem/id3v2/v2"
)

func ParseMP3(r io.ReadSeeker) (*MP3Tag, error) {
	resultTag := MP3Tag{}
	resultTag.reader = r
	tag, _, err := readMP3ID3(r)
	if err != nil {
		return nil, fmt.Errorf("error parsing mp3: %w", err)
	}
	rtPtr := reflect.ValueOf(&resultTag).Elem()
	for k, v := range mp3TextFrames {
		framer := tag.GetTextFrame(v)
		if framer.Text == "" {
			continue
		}
		rtPtr.FieldByName(k).SetString(framer.Text)
	}
	if pictures := tag.GetFrames("APIC"); len(pictures) > 0 {
		pic := pictures[0].(mp3TagLib.PictureFrame)
		if img, _, err := image.Decode(bytes.NewReader(pic.Picture)); err == nil {
			resultTag.CoverArt = &img
			resultTag.coverSource = &img
		}
	}
	resultTag.DiscNumber, resultTag.DiscTotal, err = legacyMP3Pair(resultTag.DiscNumberString)
	if err != nil {
		return nil, err
	}
	resultTag.TrackNumber, resultTag.TrackTotal, err = legacyMP3Pair(resultTag.TrackNumberString)
	if err != nil {
		return nil, err
	}
	return &resultTag, nil
}

var legacyMP3NumberDelimiter = regexp.MustCompile("[^0-9]+")

func legacyMP3Pair(text string) (int, int, error) {
	if text == "" {
		return 0, 0, nil
	}
	parts := legacyMP3NumberDelimiter.Split(text, -1)
	var total int
	var err error
	if len(parts) == 2 {
		total, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, err
		}
	}
	number, err := strconv.Atoi(parts[0])
	return number, total, err
}
