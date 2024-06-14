package mp3meta

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockReadSeeker is a mock implementation of io.ReadSeeker
type MockReadSeeker struct {
	mock.Mock
	data   []byte
	offset int64
}

// NewMockReadSeeker initializes a new MockReadSeeker with the given data
func NewMockReadSeeker(data []byte) *MockReadSeeker {
	return &MockReadSeeker{
		data: data,
	}
}

// Read reads up to len(p) bytes into p
func (m *MockReadSeeker) Read(p []byte) (int, error) {
	args := m.Called(p)
	if m.offset >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.offset:])
	m.offset += int64(n)
	return n, args.Error(1)
}

// Seek sets the offset for the next Read or Write to offset, interpreted according to whence
func (m *MockReadSeeker) Seek(offset int64, whence int) (int64, error) {
	args := m.Called(offset, whence)
	switch whence {
	case io.SeekStart:
		m.offset = offset
	case io.SeekCurrent:
		m.offset += offset
	case io.SeekEnd:
		m.offset = int64(len(m.data)) + offset
	}
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset > int64(len(m.data)) {
		m.offset = int64(len(m.data))
	}
	return m.offset, args.Error(1)
}

type MockWriter struct {
	mock.Mock
}

// Write writes len(p) bytes from p to the underlying data stream
func (m *MockWriter) Write(p []byte) (int, error) {
	args := m.Called(p)
	return args.Int(0), args.Error(1)
}

func compareImages(src1 [][][3]float32, src2 [][][3]float32) bool {
	dif := 0
	for i, dat1 := range src1 {
		for j := range dat1 {
			if len(src1[i][j]) != len(src2[i][j]) {
				dif++
			}
		}
	}
	return dif == 0
}

func image_2_array_at(src image.Image) [][][3]float32 {
	bounds := src.Bounds()
	width, height := bounds.Max.X, bounds.Max.Y
	iaa := make([][][3]float32, height)

	for y := 0; y < height; y++ {
		row := make([][3]float32, width)
		for x := 0; x < width; x++ {
			r, g, b, _ := src.At(x, y).RGBA()
			// A color's RGBA method returns values in the range [0, 65535].
			// Shifting by 8 reduces this to the range [0, 255].
			row[x] = [3]float32{float32(r >> 8), float32(g >> 8), float32(b >> 8)}
		}
		iaa[y] = row
	}

	return iaa
}

func TestReadMP3Tags(t *testing.T) {
	path, _ := filepath.Abs("./testdata/testdata-mp3.mp3")
	f, err := os.Open(path)
	assert.NoError(t, err)
	tag, err := ParseMP3(f)
	assert.NoError(t, err)
	assert.NotEmpty(t, tag.GetArtist())
	assert.NotEmpty(t, tag.GetAlbum())
	assert.NotEmpty(t, tag.GetTitle())

}

func TestReadMP3TagsError(t *testing.T) {
	path, _ := filepath.Abs("./testdata/testdata-mp3.mp3")
	f, err := os.Open(path)
	assert.NoError(t, err)
	d, err := io.ReadAll(f)
	assert.NoError(t, err)
	rs := NewMockReadSeeker(d)
	rs.On("Read", mock.Anything).Return(8, errors.New("bad read"))
	_, err = ParseMP3(rs)
	assert.Error(t, err)

}

func TestWriteMP3Errors(t *testing.T) {
	t.Run("seek error 1", func(t *testing.T) {
		path, _ := filepath.Abs("./testdata/testdata-mp3.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		tag, err := ParseMP3(f)
		assert.NoError(t, err)
		assert.NotEmpty(t, tag.GetArtist())
		assert.NotEmpty(t, tag.GetAlbum())
		assert.NotEmpty(t, tag.GetTitle())
		tag.ClearAllTags()
		tag.SetAlbum("album1")
		_, err = f.Seek(0, io.SeekStart)
		assert.NoError(t, err)
		d, err := io.ReadAll(f)
		assert.NoError(t, err)
		rs := NewMockReadSeeker(d)
		tag.reader = rs
		rs.On("Seek", int64(0), 0).Return(0, errors.New("bad seek"))
		m := new(MockWriter)
		err = SaveMP3(tag, m)
		assert.Error(t, err)
	})
	t.Run("read error 1", func(t *testing.T) {
		path, _ := filepath.Abs("./testdata/testdata-mp3.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		tag, err := ParseMP3(f)
		assert.NoError(t, err)
		assert.NotEmpty(t, tag.GetArtist())
		assert.NotEmpty(t, tag.GetAlbum())
		assert.NotEmpty(t, tag.GetTitle())
		tag.ClearAllTags()
		tag.SetAlbum("album1")
		_, err = f.Seek(0, io.SeekStart)
		assert.NoError(t, err)
		d, err := io.ReadAll(f)
		assert.NoError(t, err)
		rs := NewMockReadSeeker(d)
		tag.reader = rs
		rs.On("Seek", int64(0), 0).Return(0, nil)
		rs.On("Read", mock.Anything).Return(0, errors.New("bad read"))
		m := new(MockWriter)
		err = SaveMP3(tag, m)
		assert.Error(t, err)
	})
	t.Run("seek error 2", func(t *testing.T) {
		path, _ := filepath.Abs("./testdata/testdata-mp3.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		tag, err := ParseMP3(f)
		assert.NoError(t, err)
		assert.NotEmpty(t, tag.GetArtist())
		assert.NotEmpty(t, tag.GetAlbum())
		assert.NotEmpty(t, tag.GetTitle())
		tag.ClearAllTags()
		tag.SetAlbum("album1")
		_, err = f.Seek(0, io.SeekStart)
		assert.NoError(t, err)
		d, err := io.ReadAll(f)
		assert.NoError(t, err)
		rs := NewMockReadSeeker(d)
		tag.reader = rs
		rs.On("Seek", int64(0), 0).Return(0, nil).Once()
		rs.On("Read", mock.Anything).Return(0, nil)
		rs.On("Seek", int64(0), 0).Return(0, errors.New("bad seek")).Once()
		m := new(MockWriter)
		m.On("Write", mock.Anything).Return(0, nil)
		err = SaveMP3(tag, m)
		assert.Error(t, err)
	})
	t.Run("write error 1", func(t *testing.T) {
		path, _ := filepath.Abs("./testdata/testdata-mp3.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		tag, err := ParseMP3(f)
		assert.NoError(t, err)
		assert.NotEmpty(t, tag.GetArtist())
		assert.NotEmpty(t, tag.GetAlbum())
		assert.NotEmpty(t, tag.GetTitle())
		tag.ClearAllTags()
		tag.SetAlbum("album1")
		m := new(MockWriter)
		m.On("Write", mock.Anything).Return(0, errors.New("bad write"))
		err = SaveMP3(tag, m)
		assert.Error(t, err)
	})
	t.Run("write error 2", func(t *testing.T) {
		path, _ := filepath.Abs("./testdata/testdata-mp3.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		tag, err := ParseMP3(f)
		assert.NoError(t, err)
		assert.NotEmpty(t, tag.GetArtist())
		assert.NotEmpty(t, tag.GetAlbum())
		assert.NotEmpty(t, tag.GetTitle())
		tag.ClearAllTags()
		tag.SetAlbum("album1")
		m := new(MockWriter)
		m.On("Write", mock.Anything).Return(64, nil).Once()
		m.On("Write", mock.Anything).Return(0, errors.New("bad write")).Once()
		err = SaveMP3(tag, m)
		assert.Error(t, err)
	})

}

func TestMP3(t *testing.T) {
	t.Run("TestWriteEmptyTagsMP3-buffers", func(t *testing.T) {
		path, _ := filepath.Abs("testdata/testdata-mp3-nonEmpty.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		b, err := io.ReadAll(f)
		assert.NoError(t, err)
		r := bytes.NewReader(b)
		tag, err := ParseMP3(r)
		assert.NoError(t, err)
		tag.ClearAllTags()
		buffy := new(bytes.Buffer)
		err = tag.Save(buffy)
		assert.NoError(t, err)
		r = bytes.NewReader(buffy.Bytes())
		tag, err = ParseMP3(r)
		assert.NoError(t, err)
		assert.Empty(t, tag.GetArtist())
		assert.Empty(t, tag.GetAlbum())
		assert.Empty(t, tag.GetTitle())

	})

	t.Run("TestWriteEmptyTagsMP3-file", func(t *testing.T) {
		err := os.Mkdir("testdata/temp", 0755)
		assert.NoError(t, err)
		of, err := os.ReadFile("testdata/testdata-mp3-nonEmpty.mp3")
		assert.NoError(t, err)
		err = os.WriteFile("testdata/temp/testdata-mp3-nonEmpty.mp3", of, 0755)
		assert.NoError(t, err)
		path, _ := filepath.Abs("testdata/temp/testdata-mp3-nonEmpty.mp3")
		f, err := os.OpenFile(path, os.O_RDONLY, 0755)
		assert.NoError(t, err)
		defer f.Close()
		tag, err := ParseMP3(f)
		assert.NoError(t, err)
		tag.ClearAllTags()
		err = tag.Save(f)
		assert.NoError(t, err)
		_, err = f.Seek(0, io.SeekStart)
		assert.NoError(t, err)
		tag, err = ParseMP3(f)
		assert.NoError(t, err)
		f.Close()
		err = os.RemoveAll("testdata/temp")
		assert.NoError(t, err)
		assert.Empty(t, tag.GetArtist())
		assert.Empty(t, tag.GetAlbum())
		assert.Empty(t, tag.GetTitle())
	})

	t.Run("TestWriteTagsMP3FromEmpty-buffers", func(t *testing.T) {
		path, _ := filepath.Abs("testdata/testdata-mp3-nonEmpty.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		defer f.Close()
		b, err := io.ReadAll(f)
		assert.NoError(t, err)
		r := bytes.NewReader(b)

		tag, err := ParseMP3(r)
		assert.NoError(t, err)
		tag.ClearAllTags()

		buffy := new(bytes.Buffer)
		err = tag.Save(buffy)
		assert.NoError(t, err)
		r = bytes.NewReader(buffy.Bytes())
		tag, err = ParseMP3(r)
		assert.NoError(t, err)
		tag.SetArtist("TestArtist1")
		tag.SetAlbumArtist("AlbumArtist1")
		tag.SetAlbum("TestAlbum1")
		tag.SetBPM(115)
		tag.SetComposer("Composer Man")
		tag.SetCopyright("No Stealing")
		tag.SetDate("1019")
		tag.SetDiscNumber(1)
		tag.SetDiscTotal(2)
		tag.SetEncoder("Encoooooder")
		tag.SetGenre("Dark Wave")
		tag.SetISRC("12345678")
		tag.SetLanguage("English")
		tag.SetLength("30000")
		tag.SetLyricist("Lyric Man")
		tag.SetPublisher("Mister Publisher")
		tag.SetSubTitle("The Reckoning")
		tag.SetTrackNumber(7)
		tag.SetTrackTotal(14)
		tag.SetYear(2008)
		tag.SetTitle("TestTitle1")

		p, err := filepath.Abs("./testdata/testdata-img-1.jpg")
		assert.NoError(t, err)
		jp, err := os.Open(p)
		assert.NoError(t, err)
		j, err := jpeg.Decode(jp)
		assert.NoError(t, err)
		tag.SetCoverArt(&j)
		assert.NoError(t, err)

		buffy = new(bytes.Buffer)
		err = tag.Save(buffy)
		assert.NoError(t, err)
		r = bytes.NewReader(buffy.Bytes())
		tag, err = ParseMP3(r)
		assert.NoError(t, err)
		assert.Equal(t, tag.GetArtist(), "TestArtist1")
		assert.Equal(t, tag.GetAlbumArtist(), "AlbumArtist1")
		assert.Equal(t, tag.GetAlbum(), "TestAlbum1")
		assert.Equal(t, tag.GetBPM(), 115)
		assert.Equal(t, tag.GetComposer(), "Composer Man")
		assert.Equal(t, tag.GetDate(), "1019")
		assert.Equal(t, tag.GetDiscNumber(), 1)
		assert.Equal(t, tag.GetDiscTotal(), 2)
		assert.Equal(t, tag.GetEncoder(), "Encoooooder")
		assert.Equal(t, tag.GetGenre(), "Dark Wave")
		assert.Equal(t, tag.GetISRC(), "12345678")
		assert.Equal(t, tag.GetLanguage(), "English")
		assert.Equal(t, tag.GetLength(), "30000")
		assert.Equal(t, tag.GetLyricist(), "Lyric Man")
		assert.Equal(t, tag.GetPublisher(), "Mister Publisher")
		assert.Equal(t, tag.GetSubTitle(), "The Reckoning")
		assert.Equal(t, tag.GetTrackNumber(), 7)
		assert.Equal(t, tag.GetTrackTotal(), 14)
		assert.Equal(t, tag.GetYear(), 2008)
		assert.Equal(t, tag.GetTitle(), "TestTitle1")

		picFile, err := os.Open(p)
		assert.NoError(t, err)
		picData, _, err := image.Decode(picFile)
		assert.NoError(t, err)
		img1data := image_2_array_at(picData)
		img2data := image_2_array_at(*tag.GetCoverArt())

		assert.True(t, compareImages(img1data, img2data))
	})

	t.Run("TestWriteTagsMP3FromEmpty-file", func(t *testing.T) {
		err := os.Mkdir("testdata/temp", 0755)
		assert.NoError(t, err)
		of, err := os.ReadFile("testdata/testdata-mp3-nonEmpty.mp3")
		assert.NoError(t, err)
		err = os.WriteFile("testdata/temp/testdata-mp3-nonEmpty.mp3", of, 0755)
		assert.NoError(t, err)
		path, _ := filepath.Abs("testdata/temp/testdata-mp3-nonEmpty.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		defer f.Close()

		tag, err := ParseMP3(f)
		assert.NoError(t, err)
		tag.SetArtist("TestArtist1")
		tag.SetTitle("TestTitle1")
		tag.SetAlbum("TestAlbum1")
		p, err := filepath.Abs("./testdata/testdata-img-1.jpg")
		assert.NoError(t, err)
		jp, err := os.Open(p)
		assert.NoError(t, err)
		j, err := jpeg.Decode(jp)
		assert.NoError(t, err)
		tag.SetCoverArt(&j)
		assert.NoError(t, err)
		err = tag.Save(f)
		assert.NoError(t, err)

		_, err = f.Seek(0, io.SeekStart)
		assert.NoError(t, err)
		tag, err = ParseMP3(f)
		assert.NoError(t, err)
		err = os.RemoveAll("testdata/temp")
		assert.NoError(t, err)
		assert.Equal(t, tag.GetArtist(), "TestArtist1")
		assert.Equal(t, tag.GetAlbum(), "TestAlbum1")
		assert.Equal(t, tag.GetTitle(), "TestTitle1")

		picFile, err := os.Open(p)
		assert.NoError(t, err)
		picData, _, err := image.Decode(picFile)
		assert.NoError(t, err)
		img1data := image_2_array_at(picData)
		img2data := image_2_array_at(*tag.GetCoverArt())

		assert.True(t, compareImages(img1data, img2data))

	})

	t.Run("TestUpdateTagsMP3-buffers", func(t *testing.T) {
		path, _ := filepath.Abs("testdata/testdata-mp3-nonEmpty.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		defer f.Close()
		b, err := io.ReadAll(f)
		assert.NoError(t, err)
		r := bytes.NewReader(b)

		tag, err := ParseMP3(r)
		assert.NoError(t, err)
		tag.ClearAllTags()

		buffy := new(bytes.Buffer)
		err = tag.Save(buffy)
		assert.NoError(t, err)
		r = bytes.NewReader(buffy.Bytes())
		tag, err = ParseMP3(r)
		assert.NoError(t, err)
		tag.SetArtist("TestArtist1")
		tag.SetTitle("TestTitle1")
		tag.SetAlbum("TestAlbum1")
		p, err := filepath.Abs("./testdata/testdata-img-1.jpg")
		assert.NoError(t, err)
		jp, err := os.Open(p)
		assert.NoError(t, err)
		j, err := jpeg.Decode(jp)
		assert.NoError(t, err)
		tag.SetCoverArt(&j)
		assert.NoError(t, err)

		buffy = new(bytes.Buffer)
		err = tag.Save(buffy)
		assert.NoError(t, err)
		r = bytes.NewReader(buffy.Bytes())
		tag, err = ParseMP3(r)
		assert.NoError(t, err)
		assert.Equal(t, tag.GetArtist(), "TestArtist1")
		assert.Equal(t, tag.GetAlbum(), "TestAlbum1")
		assert.Equal(t, tag.GetTitle(), "TestTitle1")

		tag.SetArtist("TestArtist2")

		buffy = new(bytes.Buffer)
		err = tag.Save(buffy)
		assert.NoError(t, err)

		r = bytes.NewReader(buffy.Bytes())
		tag, err = ParseMP3(r)
		assert.NoError(t, err)
		assert.Equal(t, tag.GetArtist(), "TestArtist2")
		assert.Equal(t, tag.GetAlbum(), "TestAlbum1")
		assert.Equal(t, tag.GetTitle(), "TestTitle1")
		picFile, err := os.Open(p)
		assert.NoError(t, err)
		picData, _, err := image.Decode(picFile)
		assert.NoError(t, err)
		img1data := image_2_array_at(picData)
		img2data := image_2_array_at(*tag.GetCoverArt())

		assert.True(t, compareImages(img1data, img2data))
	})

	t.Run("TestUpdateTagsMP3-file", func(t *testing.T) {
		err := os.Mkdir("testdata/temp", 0755)
		assert.NoError(t, err)
		of, err := os.ReadFile("testdata/testdata-mp3-nonEmpty.mp3")
		assert.NoError(t, err)
		err = os.WriteFile("testdata/temp/testdata-mp3-nonEmpty.mp3", of, 0755)
		assert.NoError(t, err)
		path, _ := filepath.Abs("testdata/temp/testdata-mp3-nonEmpty.mp3")
		f, err := os.Open(path)
		assert.NoError(t, err)
		defer f.Close()

		tag, err := ParseMP3(f)
		assert.NoError(t, err)
		tag.SetArtist("TestArtist1")
		tag.SetTitle("TestTitle1")
		tag.SetAlbum("TestAlbum1")
		p, err := filepath.Abs("./testdata/testdata-img-1.jpg")
		assert.NoError(t, err)
		jp, err := os.Open(p)
		assert.NoError(t, err)
		j, err := jpeg.Decode(jp)
		assert.NoError(t, err)
		tag.SetCoverArt(&j)
		assert.NoError(t, err)
		err = tag.Save(f)
		assert.NoError(t, err)

		_, err = f.Seek(0, io.SeekStart)
		assert.NoError(t, err)
		tag, err = ParseMP3(f)
		assert.NoError(t, err)
		assert.Equal(t, tag.GetArtist(), "TestArtist1")
		assert.Equal(t, tag.GetAlbum(), "TestAlbum1")
		assert.Equal(t, tag.GetTitle(), "TestTitle1")

		tag.SetArtist("TestArtist2")
		err = tag.Save(f)
		assert.NoError(t, err)

		_, err = f.Seek(0, io.SeekStart)
		assert.NoError(t, err)
		tag, err = ParseMP3(f)
		assert.NoError(t, err)
		f.Close()
		err = os.RemoveAll("testdata/temp")
		assert.NoError(t, err)
		assert.Equal(t, tag.GetArtist(), "TestArtist2")
		assert.Equal(t, tag.GetAlbum(), "TestAlbum1")
		assert.Equal(t, tag.GetTitle(), "TestTitle1")
		picFile, err := os.Open(p)
		assert.NoError(t, err)
		picData, _, err := image.Decode(picFile)
		assert.NoError(t, err)
		img1data := image_2_array_at(picData)
		img2data := image_2_array_at(*tag.GetCoverArt())

		assert.True(t, compareImages(img1data, img2data))
	})
}
