package mp3meta

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	core "github.com/gcottom/audiometa-core/v2"
	"github.com/gcottom/audiometa-core/v2/contracttest"
)

func rawFixture(t testing.TB, version byte, audio []byte, frames ...rawID3Frame) []byte {
	t.Helper()
	header := [10]byte{'I', 'D', '3', version}
	var size int
	for _, f := range frames {
		size += len(f.raw)
	}
	storeSynchsafe(header[6:], size)
	data := append([]byte(nil), header[:]...)
	for _, f := range frames {
		data = append(data, f.raw...)
	}
	return append(data, audio...)
}

func fixtureFrame(t testing.TB, version byte, id string, body []byte) rawID3Frame {
	t.Helper()
	f, err := newRawID3Frame(version, id, body)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func fixtureAudio(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/testdata-mp3-nonEmpty.mp3")
	if err != nil {
		t.Fatal(err)
	}
	boundary := 10 + encodedID3Size(data)
	return data[boundary:]
}

func TestCanonicalID3Contract(t *testing.T) {
	audio := fixtureAudio(t)
	// A legacy trailer in the source audio would mask removal measurements.
	if len(audio) >= 128 && string(audio[len(audio)-128:len(audio)-125]) == "TAG" {
		audio = audio[:len(audio)-128]
	}
	for _, version := range []byte{2, 3, 4} {
		t.Run("v2."+strconv.Itoa(int(version)), func(t *testing.T) {
			contracttest.Run(t, OpenMetadata, rawFixture(t, version, audio))
		})
	}
}

func TestCanonicalID3NativeFramesAndIntegrity(t *testing.T) {
	const version = 4
	audio := fixtureAudio(t)
	unknown := fixtureFrame(t, version, "XPRV", []byte{255, 0, 127, 200})
	unknown.flags = 0x2000
	unknown.raw[8] = 0x20
	second := fixtureFrame(t, version, "XPRV", []byte{0, 1, 255})
	input := rawFixture(t, version, audio, unknown, second)

	editor := contracttest.OpenFixture(t, OpenMetadata, input)
	picture := core.Picture{Role: 3, MIME: "image/png", Description: "Cover 井", Data: []byte{1, 2, 3}}
	lyrics := core.SyncedLyrics{Language: "eng", Description: "LY", Cues: []core.LyricCue{{TimeMS: 1, Text: "音"}}}
	chapter := core.Chapter{ID: "first", Title: "Opening", StartMS: 0, EndMS: 101}
	values := map[core.Field][]core.Value{
		core.Title:               {core.TextValue("café 井")},
		core.Artist:              {core.TextValue("Artist 1"), core.TextValue("Artist 2")},
		core.Mood:                {core.TextValue("静かな")},
		core.TrackNumber:         {core.IntValue(2)},
		core.TrackTotal:          {core.IntValue(8)},
		core.PlayCount:           {core.IntValue(500)},
		core.RecordingDate:       {core.DateValue("2024-01")},
		core.ReleaseDate:         {core.DateValue("2024-02")},
		core.ReplayGainTrackGain: {core.DecimalValue(-4.75)},
		core.Artwork:             {core.PictureValue(picture)},
		core.SynchronizedLyrics:  {core.SyncedLyricsValue(lyrics)},
		core.Chapters:            {core.ChapterValue(chapter)},
		core.PerformerCredits:    {core.CreditValue(core.Credit{Role: "synth", Name: "Josef"})},
	}
	for field, v := range values {
		if err := editor.SetField(field, v...); err != nil {
			t.Fatal(err)
		}
	}
	output := contracttest.SaveFixture(t, editor)
	tag, err := readRawID3(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output[tag.start:], audio) {
		t.Fatal("compressed media or existing trailer changed")
	}
	if !bytes.Equal(tag.frames[0].raw, unknown.raw) || !bytes.Equal(tag.frames[1].raw, second.raw) {
		t.Fatal("ordered unknown frame headers or payloads changed")
	}
	ids := make(map[string]bool)
	for _, frame := range tag.frames {
		ids[tag.frameKey(frame)] = true
	}
	for _, name := range []string{"TIT2", "TPE1", "TMOO", "TRCK", "PCNT", "TDRC", "TDRL", "TXXX:REPLAYGAIN_TRACK_GAIN", "APIC", "SYLT", "CHAP", "TMCL"} {
		if !ids[name] {
			t.Errorf("native or conventional wire entry %s was absent from %v", name, ids)
		}
	}
	reopened := contracttest.OpenFixture(t, OpenMetadata, output)
	for field, want := range values {
		got, _ := reopened.Get(field)
		contracttest.AssertValues(t, got, want)
	}
	if err := reopened.SetField(core.Title, core.TextValue("Edit only title")); err != nil {
		t.Fatal(err)
	}
	output = contracttest.SaveFixture(t, reopened)
	edited, err := readRawID3(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range tag.frames {
		if f.id != "TIT2" && !bytes.Equal(f.raw, edited.frames[i].raw) {
			t.Fatalf("unrelated %s frame changed", f.id)
		}
	}
}

func TestCanonicalID3v1RemovalAndPairPreservation(t *testing.T) {
	audio := fixtureAudio(t)
	if len(audio) >= 128 && string(audio[len(audio)-128:len(audio)-125]) == "TAG" {
		audio = audio[:len(audio)-128]
	}
	trailer := make([]byte, 128)
	copy(trailer, "TAG")
	copy(trailer[3:33], "Earlier title")
	copy(trailer[33:63], "Legacy artist")
	copy(trailer[97:125], "Legacy comment")
	trailer[126] = 3
	trailer[127] = 17
	input := append(append([]byte(nil), audio...), trailer...)
	editor := contracttest.OpenFixture(t, OpenMetadata, input)
	title, _ := editor.Get(core.Title)
	contracttest.AssertValues(t, title, []core.Value{core.TextValue("Earlier title")})
	track, _ := editor.Get(core.TrackNumber)
	contracttest.AssertValues(t, track, []core.Value{core.IntValue(3)})
	if err := editor.SetField(core.TrackTotal, core.IntValue(11)); err != nil {
		t.Fatal(err)
	}
	if err := editor.RemoveField(core.Title); err != nil {
		t.Fatal(err)
	}
	editor = contracttest.OpenFixture(t, OpenMetadata, contracttest.SaveFixture(t, editor))
	if _, ok := editor.Get(core.Title); ok {
		t.Fatal("ID3v1 title reappeared")
	}
	if err := editor.RemoveField(core.TrackNumber); err != nil {
		t.Fatal(err)
	}
	editor = contracttest.OpenFixture(t, OpenMetadata, contracttest.SaveFixture(t, editor))
	if _, ok := editor.Get(core.TrackNumber); ok {
		t.Fatal("track number reappeared")
	}
	total, _ := editor.Get(core.TrackTotal)
	contracttest.AssertValues(t, total, []core.Value{core.IntValue(11)})
	comment, _ := editor.Get(core.Comments)
	contracttest.AssertValues(t, comment, []core.Value{core.TextValue("Legacy comment")})
}

func TestCanonicalID3UnsafeRewriteKeepsOutputUntouched(t *testing.T) {
	audio := fixtureAudio(t)
	const version = 4
	prv := fixtureFrame(t, version, "XPRV", []byte{1, 2, 3})
	prv.raw[8] = 0x40
	input := rawFixture(t, version, audio, prv)
	editor := contracttest.OpenFixture(t, OpenMetadata, input)
	if err := editor.SetField(core.Title, core.TextValue("New title")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := editor.Save(&out); !errors.Is(err, ErrUnsafeID3Edit) || out.Len() != 0 {
		t.Fatalf("unsafe rewrite got %v with %d bytes written", err, out.Len())
	}
	editor.ClearAll(false)
	if err := editor.Save(&out); err != nil {
		t.Fatal(err)
	}
	tag, err := readRawID3(bytes.NewReader(out.Bytes()))
	if err != nil || len(tag.frames) != 0 || !bytes.Equal(out.Bytes()[tag.start:], audio) {
		t.Fatalf("explicit removal got frames %d, %v", len(tag.frames), err)
	}
}

func TestCanonicalID3SameFileRepeatedSave(t *testing.T) {
	input := rawFixture(t, 4, fixtureAudio(t))
	path := filepath.Join(t.TempDir(), "same.mp3")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	editor, err := OpenMetadata(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := editor.SetField(core.Title, core.TextValue("Changed")); err != nil {
		t.Fatal(err)
	}
	if err := editor.Save(file); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := editor.Save(file); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(second, first) {
		t.Fatalf("nondeterministic same-file result: %v", err)
	}
}

func FuzzCanonicalID3(f *testing.F) {
	f.Add([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"))
	f.Add([]byte("ID3\x02\x00\x00\x00\x00\x00\x07TT2\x00\x00\x01\x00"))
	f.Add([]byte("ID3\x04\x00\x10\x00\x00\x00\x00" + "3DI\x04\x00\x10\x00\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		editor, err := OpenMetadata(bytes.NewReader(b))
		if err != nil {
			return
		}
		var out bytes.Buffer
		if err := editor.Save(&out); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), b) {
			t.Fatal("unchanged save modified input")
		}
		if err := editor.SetField(core.Title, core.TextValue("fuzz")); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		if err := editor.Save(&out); err != nil {
			if out.Len() != 0 {
				t.Fatalf("preflight rejection wrote %d bytes", out.Len())
			}
			return
		}
		reopened, err := OpenMetadata(bytes.NewReader(out.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		v, _ := reopened.GetField(core.Title)
		title, ok := v.Text()
		if !ok || title != "fuzz" {
			t.Fatalf("edited title = %q", title)
		}
	})
}

func BenchmarkCanonicalID3Rewrite(b *testing.B) {
	for _, n := range []int{32 << 10, 4 << 20, 40 << 20} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			input := rawFixture(b, 4, bytes.Repeat([]byte{0xfa}, n))
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				editor := contracttest.OpenFixture(b, OpenMetadata, input)
				if err := editor.SetField(core.Title, core.TextValue("Updated title")); err != nil {
					b.Fatal(err)
				}
				if err := editor.Save(io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestLegacyTitleEditKeepsArtworkAndOpaqueFrames(t *testing.T) {
	const version = 4
	audio := fixtureAudio(t)
	raw := rawID3Tag{version: version}
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 7, 9))); err != nil {
		t.Fatal(err)
	}
	picture := func(role uint32, description string) core.Value {
		return core.PictureValue(core.Picture{Role: role, MIME: "image/png", Description: description, Data: imageData.Bytes()})
	}
	frames, ok, err := raw.renderPictures("APIC", []core.Value{picture(3, "Front"), picture(4, "Back")})
	if err != nil || !ok {
		t.Fatalf("cannot prepare art: %v", err)
	}
	unknown := fixtureFrame(t, version, "XPRV", []byte{1, 2, 255})
	unknown.raw[8] = 0x20
	frames = append(frames, unknown)
	title, _ := encodeID3Strings(version, []string{"Original"})
	frames = append(frames, fixtureFrame(t, version, "TIT2", title))
	input := rawFixture(t, version, audio, frames...)
	legacy, err := ParseMP3(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.GetCoverArt() == nil {
		t.Fatal("PNG fixture missing from legacy tag")
	}
	legacy.SetTitle("New title")
	var out bytes.Buffer
	if err := legacy.Save(&out); err != nil {
		t.Fatal(err)
	}
	got, err := readRawID3(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(frames)-1; i++ {
		if !bytes.Equal(got.frames[i].raw, frames[i].raw) {
			t.Fatalf("legacy save rewrote unrelated frame %s", frames[i].id)
		}
	}
	if !bytes.Equal(out.Bytes()[got.start:], audio) {
		t.Fatal("legacy edit changed audio bytes")
	}
}

func TestCanonicalID3TerminatedListAndExplicitEmpty(t *testing.T) {
	const version = 4
	audio := fixtureAudio(t)
	input := rawFixture(t, version, audio, fixtureFrame(t, version, "TPE1", []byte("\x03One\x00Two\x00")))
	tag := contracttest.OpenFixture(t, OpenMetadata, input)
	got, _ := tag.Get(core.Artist)
	contracttest.AssertValues(t, got, []core.Value{core.TextValue("One"), core.TextValue("Two")})

	explicit := []core.Value{core.TextValue("One"), core.TextValue("")}
	if err := tag.SetField(core.Artist, explicit...); err != nil {
		t.Fatal(err)
	}
	tag = contracttest.OpenFixture(t, OpenMetadata, contracttest.SaveFixture(t, tag))
	got, _ = tag.Get(core.Artist)
	contracttest.AssertValues(t, got, explicit)
}

func BenchmarkCanonicalID3PreservingLargeArtwork(b *testing.B) {
	for _, n := range []int{1 << 20, 8 << 20} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			body := append([]byte("\x03image/jpeg\x00\x03front\x00"), bytes.Repeat([]byte{0xaf}, n)...)
			input := rawFixture(b, 4, fixtureAudio(b), fixtureFrame(b, 4, "APIC", body))
			editor := contracttest.OpenFixture(b, OpenMetadata, input)
			if err := editor.SetField(core.Title, core.TextValue("Updated title")); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(input)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := editor.Save(io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestCanonicalID3PairRejectsUnrepresentableUntouchedValues(t *testing.T) {
	const version = 4
	audio := fixtureAudio(t)
	track := func(s string) rawID3Frame {
		data, _ := encodeID3Strings(version, []string{s})
		return fixtureFrame(t, version, "TRCK", data)
	}
	for _, tc := range []struct {
		name   string
		frames []rawID3Frame
	}{
		{"conflicting duplicates", []rawID3Frame{track("2/9"), track("4/11")}},
		{"opaque total", []rawID3Frame{track("2/opaque")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := rawFixture(t, version, audio, tc.frames...)
			editor := contracttest.OpenFixture(t, OpenMetadata, input)
			if err := editor.SetField(core.TrackNumber, core.IntValue(3)); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := editor.Save(&out); !errors.Is(err, ErrUnsafeID3Edit) || out.Len() != 0 {
				t.Fatalf("changed paired slot with %v and wrote %d bytes", err, out.Len())
			}
			if err := editor.SetField(core.TrackTotal, core.IntValue(17)); err != nil {
				t.Fatal(err)
			}
			got := contracttest.OpenFixture(t, OpenMetadata, contracttest.SaveFixture(t, editor))
			total, _ := got.GetField(core.TrackTotal)
			if value, ok := total.Int(); !ok || value != 17 {
				t.Fatalf("explicit paired replacement read total %v", total)
			}
		})
	}
	input := rawFixture(t, version, audio, track("2/9"), track("4/11"))
	legacy, err := ParseMP3(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	legacy.SetTrackNumber(3)
	var out bytes.Buffer
	if err := legacy.Save(&out); !errors.Is(err, ErrUnsafeID3Edit) || out.Len() != 0 {
		t.Fatalf("legacy changed paired slot with %v and wrote %d bytes", err, out.Len())
	}
}

func TestCanonicalID3PairPreservesAlternateUntouchedValue(t *testing.T) {
	const version = 4
	audio := fixtureAudio(t)
	primary, _ := encodeID3Strings(version, []string{"02/009"})
	alternate, err := id3DescriptionFrame(version, "TXXX", core.FallbackName(core.TrackTotal), []string{"11"})
	if err != nil {
		t.Fatal(err)
	}
	input := rawFixture(t, version, audio, fixtureFrame(t, version, "TRCK", primary), alternate)
	editor := contracttest.OpenFixture(t, OpenMetadata, input)
	if err := editor.SetField(core.TrackNumber, core.IntValue(3)); err != nil {
		t.Fatal(err)
	}
	output := contracttest.SaveFixture(t, editor)
	raw, err := readRawID3(bytes.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	if actual, _ := decodeID3Strings(raw.frames[0].body[1:], 3, true); len(actual) != 1 || actual[0] != "3/009" {
		t.Fatalf("unchanged total representation changed: %q", actual)
	}
	if !bytes.Equal(raw.frames[1].raw, alternate.raw) {
		t.Fatal("conflicting alternate total was discarded")
	}
	if err := editor.RemoveField(core.TrackNumber); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := editor.Save(&out); !errors.Is(err, ErrUnsafeID3Edit) || out.Len() != 0 {
		t.Fatalf("removing number would hide prior total: %v with %d output bytes", err, out.Len())
	}
}
