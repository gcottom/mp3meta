package mp3meta

import (
	"bytes"

	core "github.com/gcottom/audiometa-core"
)

func readID3v1Field(tag []byte, field core.Field) (core.Value, bool) {
	if field == core.TrackNumber {
		if tag[125] == 0 && tag[126] != 0 {
			return core.IntValue(int64(tag[126])), true
		}
		return core.Value{}, false
	}
	if field == core.Genre {
		if int(tag[127]) < len(id3v1Genres) {
			return core.TextValue(id3v1Genres[tag[127]]), true
		}
		return core.Value{}, false
	}
	b := id3v1TextField(tag, field)
	s, _ := decodeID3Text(bytes.TrimRight(b, " \x00"), 0)
	if s != "" {
		value, err := core.Decode(field, s)
		return value, err == nil
	}
	return core.Value{}, false
}

func id3v1TextField(tag []byte, field core.Field) []byte {
	switch field {
	case core.Title:
		return tag[3:33]
	case core.Artist:
		return tag[33:63]
	case core.Album:
		return tag[63:93]
	case core.RecordingDate:
		return tag[93:97]
	case core.Comments:
		if tag[125] == 0 && tag[126] != 0 {
			return tag[97:125]
		}
		return tag[97:127]
	}
	return nil
}

func clearID3v1Field(tag []byte, field core.Field) {
	if len(tag) == 0 {
		return
	}
	switch field {
	case core.TrackNumber:
		if tag[125] == 0 {
			tag[126] = 0
		}
	case core.Genre:
		tag[127] = 255
	default:
		b := id3v1TextField(tag, field)
		for i := range b {
			b[i] = 0
		}
	}
}

// The original ID3v1 genre table.
var id3v1Genres = [...]string{
	"Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge", "Hip-Hop",
	"Jazz", "Metal", "New Age", "Oldies", "Other", "Pop", "R&B", "Rap",
	"Reggae", "Rock", "Techno", "Industrial", "Alternative", "Ska", "Death Metal", "Pranks",
	"Soundtrack", "Euro-Techno", "Ambient", "Trip-Hop", "Vocal", "Jazz+Funk", "Fusion", "Trance",
	"Classical", "Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel", "Noise",
	"AlternRock", "Bass", "Soul", "Punk", "Space", "Meditative", "Instrumental Pop", "Instrumental Rock",
	"Ethnic", "Gothic", "Darkwave", "Techno-Industrial", "Electronic", "Pop-Folk", "Eurodance", "Dream",
	"Southern Rock", "Comedy", "Cult", "Gangsta", "Top 40", "Christian Rap", "Pop/Funk", "Jungle",
	"Native American", "Cabaret", "New Wave", "Psychedelic", "Rave", "Showtunes", "Trailer", "Lo-Fi",
	"Tribal", "Acid Punk", "Acid Jazz", "Polka", "Retro", "Musical", "Rock & Roll", "Hard Rock",
}
