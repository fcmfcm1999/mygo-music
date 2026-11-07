package player

import (
	"errors"
	"io"
	"math"
	"sync"

	"github.com/egoist/mygo/audio"
	_ "github.com/egoist/mygo/audio/aac" // AAC in MP4
	"github.com/egoist/mygo/audio/flac"
	"github.com/egoist/mygo/audio/mp4"
)

// downloadFile reads a download as a file, for MyGo's decoders: what's
// asked of it that isn't in yet, it waits for. A download whose size isn't
// known says it is as big as can be, so that nothing waits for all of it to
// come in just to know.
type downloadFile struct {
	dl  *download
	pos int64
}

func (f *downloadFile) ReadAt(b []byte, off int64) (int, error) {
	f.dl.waitFor(off + int64(len(b)))
	f.dl.mu.Lock()
	defer f.dl.mu.Unlock()
	n, err := f.dl.readAt(b, off)
	if n < len(b) && err == nil {
		err = f.dl.err
		if err == nil {
			err = io.ErrUnexpectedEOF // cut short
		}
	}
	return n, err
}

func (f *downloadFile) Read(b []byte) (int, error) {
	r := downloadReader{d: f.dl, pos: f.pos}
	n, err := r.Read(b)
	f.pos = r.pos
	return n, err
}

func (f *downloadFile) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		off += f.pos
	case io.SeekEnd:
		f.dl.mu.Lock()
		size := f.dl.size
		if f.dl.done || f.dl.onDisk {
			size = f.dl.n
		}
		f.dl.mu.Unlock()
		if size <= 0 {
			size = math.MaxInt64
		}
		off += size
	}
	if off < 0 {
		return 0, errors.New("player: seek before the start")
	}
	f.pos = off
	return off, nil
}

// decodedStream decodes a download with one of MyGo's decoders, and measures
// the spectrum of what it decodes, for the visualizers.
type decodedStream struct {
	audio.Decoder
	dl   *download
	pos  int64 // the frame Read returns next
	spec *spectrum
	drop func() // lets the download go, as the stream closes
	once sync.Once
}

// openDecoded opens dl with open, one of MyGo's decoders, which decode to
// stereo.
func openDecoded(dl *download, open func(io.ReadSeeker) (audio.Decoder, error)) (*decodedStream, error) {
	d, err := open(&downloadFile{dl: dl})
	if err != nil {
		return nil, err
	}
	if d.Channels() != 2 {
		return nil, errors.New("player: not stereo")
	}
	return &decodedStream{Decoder: d, dl: dl, spec: newSpectrum(d.SampleRate()), drop: dl.Close}, nil
}

// openMP4Stream opens an .m4a, of ALAC, FLAC or AAC. One cut into fragments,
// as a stream for DASH is, plays as its fragments come, its length from its
// index.
func openMP4Stream(dl *download) (*decodedStream, error) { return openDecoded(dl, mp4.NewDecoder) }

// openFLACStream opens a FLAC file: the lossless and Hi-Res streams. A seek
// lands on the very sample.
func openFLACStream(dl *download) (*decodedStream, error) { return openDecoded(dl, flac.NewDecoder) }

func (s *decodedStream) Read(dst []float32) (int, error) {
	n, err := s.Decoder.Read(dst)
	s.spec.feed(s.pos, dst[:n])
	s.pos += int64(n / 2)
	return n, err
}

func (s *decodedStream) SetPosition(frame int64) error {
	s.pos = max(frame, 0)
	if n := s.Len(); n >= 0 {
		s.pos = min(s.pos, n)
	}
	return s.Decoder.SetPosition(frame)
}

func (s *decodedStream) Close() error {
	s.once.Do(s.drop)
	return nil
}

func (s *decodedStream) source() (*download, *spectrum) { return s.dl, s.spec }
