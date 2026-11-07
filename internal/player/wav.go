package player

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sync"
)

// wavStream decodes a WAV download for package audio: PCM as it is, whole
// numbers of 8 to 32 bits or floating point, in frames all the same size. So a
// seek goes straight to the byte of the frame it's asked for.
type wavStream struct {
	dl     *download
	format wavFormat
	start  int64 // the first frame's byte
	frames int64 // the length, or -1 if unknown
	pos    int64
	r      *downloadReader
	buf    []byte
	spec   *spectrum
	drop   func() // lets the download go, as the stream closes
	once   sync.Once
}

// wavFormat is what a WAV's fmt chunk says of its frames.
type wavFormat struct {
	rate, channels, bits, align int
	float                       bool
}

func openWAVStream(dl *download) (*wavStream, error) {
	f, start, size, err := wavHeader(dl)
	if err != nil {
		return nil, err
	}
	s := &wavStream{dl: dl, format: f, start: start, frames: -1, drop: dl.Close}
	if size > 0 {
		s.frames = size / int64(f.align)
	} else if dl.size > 0 {
		s.frames = (dl.size - start) / int64(f.align)
	}
	s.r = &downloadReader{d: dl, pos: start}
	s.spec = newSpectrum(f.rate)
	return s, nil
}

// wavHeader reads a WAV's chunks up to its data: what its frames are, where
// they start, and how many bytes of them there are, or 0 if it doesn't say, as
// one written as it's sent may not.
func wavHeader(dl *download) (f wavFormat, start, size int64, err error) {
	if dl.waitFor(12) < 12 {
		return f, 0, 0, io.ErrUnexpectedEOF
	}
	h := dl.peek(0, 12)
	if string(h[:4]) != "RIFF" && string(h[:4]) != "RF64" || string(h[8:12]) != "WAVE" {
		return f, 0, 0, errors.New("wav: not a WAV file")
	}
	var ds64 int64 // the data's size, in an RF64's ds64 chunk
	off := int64(12)
	haveFormat := false
	for {
		if dl.waitFor(off+8) < off+8 {
			return f, 0, 0, io.ErrUnexpectedEOF
		}
		c := dl.peek(off, 8)
		id, n := string(c[:4]), int64(binary.LittleEndian.Uint32(c[4:]))
		off += 8
		switch id {
		case "fmt ":
			if dl.waitFor(off+n) < off+n {
				return f, 0, 0, io.ErrUnexpectedEOF
			}
			if f, err = parseWAVFormat(dl.peek(off, int(min(n, 64)))); err != nil {
				return f, 0, 0, err
			}
			haveFormat = true
		case "ds64":
			if b := dl.peek(off, 16); len(b) == 16 {
				ds64 = int64(binary.LittleEndian.Uint64(b[8:]))
			}
		case "data":
			if !haveFormat {
				return f, 0, 0, errors.New("wav: no format before the data")
			}
			if n == 0xffffffff {
				n = ds64
			}
			return f, off, n, nil
		}
		off += n + n&1 // chunks are padded to an even length
	}
}

const (
	wavPCM        = 1
	wavFloat      = 3
	wavExtensible = 0xfffe
)

func parseWAVFormat(b []byte) (wavFormat, error) {
	if len(b) < 16 {
		return wavFormat{}, errors.New("wav: short format")
	}
	tag := binary.LittleEndian.Uint16(b)
	f := wavFormat{
		channels: int(binary.LittleEndian.Uint16(b[2:])),
		rate:     int(binary.LittleEndian.Uint32(b[4:])),
		align:    int(binary.LittleEndian.Uint16(b[12:])),
		bits:     int(binary.LittleEndian.Uint16(b[14:])),
	}
	if tag == wavExtensible && len(b) >= 26 {
		tag = binary.LittleEndian.Uint16(b[24:]) // the sub-format GUID's first two bytes
	}
	switch {
	case tag == wavPCM && f.bits >= 8 && f.bits <= 32:
	case tag == wavFloat && (f.bits == 32 || f.bits == 64):
		f.float = true
	default:
		return f, errors.New("wav: only PCM and floating point are supported")
	}
	// Bits are stored in whole bytes, high-aligned.
	if f.channels < 1 || f.rate < 1 || f.align < f.channels*((f.bits+7)/8) {
		return f, errors.New("wav: bad format")
	}
	return f, nil
}

func (s *wavStream) SampleRate() int { return s.format.rate }
func (s *wavStream) Channels() int   { return 2 }
func (s *wavStream) Len() int64      { return s.frames }

// Read gives stereo: a mono file in both ears, and the front left and right of
// one with more, which WAV puts first.
func (s *wavStream) Read(dst []float32) (int, error) {
	frames := len(dst) / 2
	if s.frames >= 0 {
		frames = int(min(int64(frames), s.frames-s.pos))
	}
	if frames <= 0 {
		return 0, io.EOF
	}
	align := s.format.align
	if cap(s.buf) < frames*align {
		s.buf = make([]byte, frames*align)
	}
	got, err := io.ReadFull(s.r, s.buf[:frames*align])
	got /= align
	width := (s.format.bits + 7) / 8
	right := 0
	if s.format.channels > 1 {
		right = width
	}
	for i := range got {
		frame := s.buf[i*align:]
		dst[i*2] = s.sample(frame)
		dst[i*2+1] = s.sample(frame[right:])
	}
	if got > 0 {
		s.spec.feed(s.pos, dst[:got*2])
		s.pos += int64(got)
		return got * 2, nil
	}
	if err == io.ErrUnexpectedEOF || err == nil {
		err = io.EOF
	}
	return 0, err
}

// sample reads the sample b starts with.
func (s *wavStream) sample(b []byte) float32 {
	switch {
	case s.format.float && s.format.bits == 64:
		return float32(math.Float64frombits(binary.LittleEndian.Uint64(b)))
	case s.format.float:
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	}
	switch (s.format.bits + 7) / 8 {
	case 1: // the only one unsigned
		return float32(int(b[0])-128) / (1 << 7)
	case 2:
		return float32(int16(binary.LittleEndian.Uint16(b))) / (1 << 15)
	case 3:
		return float32(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)) / (1 << 31)
	default:
		return float32(int32(binary.LittleEndian.Uint32(b))) / (1 << 31)
	}
}

func (s *wavStream) SetPosition(frame int64) error {
	frame = max(frame, 0)
	if s.frames >= 0 {
		frame = min(frame, s.frames)
	}
	s.pos = frame
	s.r = &downloadReader{d: s.dl, pos: s.start + frame*int64(s.format.align)}
	return nil
}

func (s *wavStream) Close() error {
	s.once.Do(s.drop)
	return nil
}

func (s *wavStream) source() (*download, *spectrum) { return s.dl, s.spec }
