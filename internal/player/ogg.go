package player

import (
	"errors"
	"io"
	"sync"

	"github.com/jfreymuth/oggvorbis"
)

// oggStream decodes an Ogg Vorbis download for package audio. Finding the
// length takes the file's last page, and a seek takes pages all over it. So
// one still coming in starts playing right away, with the length its source
// says, and only waits for the rest of the file if it's asked to seek.
type oggStream struct {
	dl       *download
	r        *oggvorbis.Reader
	seekable bool
	frames   int64 // the length, or 0 if unknown
	channels int
	pos      int64
	buf      []float32
	spec     *spectrum
	drop     func() // lets the download go, as the stream closes
	once     sync.Once
}

func openOggStream(dl *download, durationMs int64) (*oggStream, error) {
	s := &oggStream{dl: dl, drop: dl.Close}
	dl.mu.Lock()
	whole := dl.done || dl.onDisk
	dl.mu.Unlock()
	var err error
	if whole {
		s.r, err = oggvorbis.NewReader(&downloadSeeker{downloadReader{d: dl}})
		s.seekable = true
	} else {
		// As a plain reader, so it doesn't look for the end first.
		s.r, err = oggvorbis.NewReader(&downloadReader{d: dl})
	}
	if err != nil {
		return nil, err
	}
	s.channels = s.r.Channels()
	if s.channels < 1 {
		return nil, errors.New("ogg: no channels")
	}
	s.frames = s.r.Length()
	if s.frames <= 0 {
		s.frames = durationMs * int64(s.r.SampleRate()) / 1000
	}
	s.spec = newSpectrum(s.r.SampleRate())
	return s, nil
}

func (s *oggStream) SampleRate() int { return s.r.SampleRate() }
func (s *oggStream) Channels() int   { return 2 }

func (s *oggStream) Len() int64 {
	if s.frames <= 0 {
		return -1
	}
	return s.frames
}

// Read gives stereo, whatever the file has: a mono file in both ears, and the
// front left and right of one with more, which Vorbis orders differently only
// past those for up to two channels; 3 and up have the centre between them.
func (s *oggStream) Read(dst []float32) (int, error) {
	frames := len(dst) / 2
	if frames == 0 {
		return 0, nil
	}
	want := frames * s.channels
	if cap(s.buf) < want {
		s.buf = make([]float32, want)
	}
	n, err := s.r.Read(s.buf[:want])
	got := n / s.channels
	right := 1
	switch {
	case s.channels == 1:
		right = 0
	case s.channels > 2:
		right = 2
	}
	for i := range got {
		frame := s.buf[i*s.channels : (i+1)*s.channels]
		dst[i*2], dst[i*2+1] = frame[0], frame[right]
	}
	if got > 0 {
		s.spec.feed(s.pos, dst[:got*2])
		s.pos += int64(got)
		return got * 2, nil
	}
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
	}
	return 0, err
}

// SetPosition goes to the frame, starting over with a reader that can seek if
// this one can't. That waits for the rest of the download.
func (s *oggStream) SetPosition(frame int64) error {
	if !s.seekable {
		s.dl.waitFor(1 << 62)
		r, err := oggvorbis.NewReader(&downloadSeeker{downloadReader{d: s.dl}})
		if err != nil {
			return err
		}
		s.r, s.seekable = r, true
		if n := r.Length(); n > 0 {
			s.frames = n
		}
	}
	if err := s.r.SetPosition(frame); err != nil {
		return err
	}
	s.pos = frame
	return nil
}

func (s *oggStream) Close() error {
	s.once.Do(s.drop)
	return nil
}

func (s *oggStream) source() (*download, *spectrum) { return s.dl, s.spec }

// downloadSeeker is a downloadReader that can also go anywhere in the song. The
// end takes the whole of it, if the size isn't known.
type downloadSeeker struct{ downloadReader }

func (r *downloadSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += r.pos
	case io.SeekEnd:
		size := r.d.waitFor(1 << 62)
		offset += size
	}
	if offset < 0 {
		return 0, errors.New("ogg: seek before the start")
	}
	r.pos = offset
	return offset, nil
}
