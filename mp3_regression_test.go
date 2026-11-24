package mp3meta

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	mp3TagLib "github.com/bogem/id3v2/v2"
)

func encodedID3Size(b []byte) int {
	return int(b[6])<<21 | int(b[7])<<14 | int(b[8])<<7 | int(b[9])
}

func testMP3Header(t *testing.T, version byte, padding int, footer bool) []byte {
	t.Helper()
	tag := mp3TagLib.NewEmptyTag()
	tag.SetVersion(version)
	tag.SetTitle("Original")
	var buf bytes.Buffer
	if _, err := tag.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	size := encodedID3Size(b) + padding
	b[6], b[7], b[8], b[9] = byte(size>>21), byte(size>>14)&127, byte(size>>7)&127, byte(size)&127
	b = append(b, make([]byte, padding)...)
	if footer {
		b[5] |= 0x10
		end := append([]byte(nil), b[:10]...)
		copy(end, "3DI")
		b = append(b, end...)
	}
	return b
}

func TestSavePreservesBytesAfterDeclaredID3Span(t *testing.T) {
	fixture, err := os.ReadFile("testdata/testdata-mp3-nonEmpty.mp3")
	if err != nil {
		t.Fatal(err)
	}
	audio := fixture[10+encodedID3Size(fixture):]
	for _, tc := range []struct {
		name   string
		header []byte
	}{
		{"v23-padding", testMP3Header(t, 3, 519, false)},
		{"v24-padding", testMP3Header(t, 4, 519, false)},
		{"v24-footer", testMP3Header(t, 4, 0, true)},
		{"no-id3", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := append(append([]byte(nil), tc.header...), audio...)
			check := func(out []byte) {
				t.Helper()
				if len(out) < 10 || string(out[:3]) != "ID3" {
					t.Fatal("missing output ID3 header")
				}
				boundary := 10 + encodedID3Size(out)
				if boundary > len(out) || !bytes.Equal(out[boundary:], audio) {
					t.Fatalf("audio after header differs: output=%d tag=%d expected audio=%d", len(out), boundary, len(audio))
				}
				got, err := ParseMP3(bytes.NewReader(out))
				if err != nil || got.GetTitle() != "Changed title" {
					t.Fatalf("reopen: %v, %v", got, err)
				}
			}
			t.Run("buffer", func(t *testing.T) {
				tag, err := ParseMP3(bytes.NewReader(input))
				if err != nil {
					t.Fatal(err)
				}
				tag.SetTitle("Changed title")
				var out bytes.Buffer
				if err := tag.Save(&out); err != nil {
					t.Fatal(err)
				}
				check(out.Bytes())
			})
			for _, dest := range []struct {
				name string
				mode int
			}{
				{"same-file", os.O_RDWR}, {"same-file-readonly", os.O_RDONLY},
			} {
				t.Run(dest.name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "fixture.mp3")
					if err := os.WriteFile(path, input, 0600); err != nil {
						t.Fatal(err)
					}
					f, err := os.OpenFile(path, dest.mode, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					tag, err := ParseMP3(f)
					if err != nil {
						t.Fatal(err)
					}
					tag.SetTitle("Changed title")
					if err := tag.Save(f); err != nil {
						t.Fatal(err)
					}
					if _, err := f.Seek(0, io.SeekStart); err != nil {
						t.Fatal(err)
					}
					out, err := io.ReadAll(f)
					if err != nil {
						t.Fatal(err)
					}
					check(out)
				})
			}
		})
	}
}

func TestSaveRejectsTruncatedDeclaredTagBeforeWriting(t *testing.T) {
	header := testMP3Header(t, 4, 32, false)
	input := header[:len(header)-10]
	tag, err := ParseMP3(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	tag.SetTitle("Longer title")
	var out bytes.Buffer
	if err := tag.Save(&out); err != io.ErrUnexpectedEOF || out.Len() != 0 {
		t.Fatalf("got %v with %d output bytes", err, out.Len())
	}
}

func TestSaveRejectsMissingID3FooterBeforeWriting(t *testing.T) {
	input := testMP3Header(t, 4, 0, true)
	// Replace the required footer with representative bytes belonging to the
	// media stream; they must not be silently skipped as if they were a footer.
	copy(input[len(input)-10:], []byte{0xff, 0xfb, 0x94, 0, 1, 2, 3, 4, 5, 6})
	tag, err := ParseMP3(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tag.Save(&out); err == nil || out.Len() != 0 {
		t.Fatalf("got %v with %d output bytes", err, out.Len())
	}
}
