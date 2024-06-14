package mp3meta

import (
	"bytes"
	"errors"
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
	tag, err := mp3TagLib.ParseReader(r, mp3TagLib.Options{Parse: true})
	if err != nil {
		return nil, errors.New("error parsing mp3")
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
		}
	}
	re := regexp.MustCompile("[^0-9]+")

	// Split the string based on the regular expression

	if resultTag.DiscNumberString != "" {
		result := re.Split(resultTag.DiscNumberString, -1)
		if len(result) == 2 {
			resultTag.DiscTotal, err = strconv.Atoi(result[1])
			if err != nil {
				return nil, err
			}
		}
		resultTag.DiscNumber, err = strconv.Atoi(result[0])
		if err != nil {
			return nil, err
		}
	}
	if resultTag.TrackNumberString != "" {
		result := re.Split(resultTag.TrackNumberString, -1)
		if len(result) == 2 {
			resultTag.TrackTotal, err = strconv.Atoi(result[1])
			if err != nil {
				return nil, err
			}
		}
		resultTag.TrackNumber, err = strconv.Atoi(result[0])
		if err != nil {
			return nil, err
		}
	}
	return &resultTag, nil
}
