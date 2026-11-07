package player

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"

	"github.com/egoist/mygo/audio/aac"
	aacpcm "github.com/tphakala/go-aac/pcm"
)

// adtsStream decodes an .aac, AAC in ADTS frames, each with a header saying
// how long it is, finding them as it plays: a whole file's all up front, for
// its length, and one coming in as they come, with the length its source
// says. MyGo's decoder finds them all as it opens, which waits for all of a
// stream to come in.
type adtsStream struct {
	dl     *download
	fd     *aacpcm.FrameDecoder
	rate   int
	offs   []int64
	sizes  []int
	more   func() bool // finds the next frame; nil once all are found
	frames int64       // the length, or -1 if unknown
	next   int         // the packet decoded next
	pos    int64       // the frame Read returns next
	skip   int64       // frames to drop before it
	packet []byte
	pcm    []byte
	pend   []float32
	spec   *spectrum
	drop   func() // lets the download go, as the stream closes
	once   sync.Once
}

var adtsRates = [...]int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

// adtsHeader reads an ADTS frame's header: its length in bytes, and its
// sample rate.
func adtsHeader(h []byte) (length, rate int, ok bool) {
	if len(h) < 7 || h[0] != 0xff || h[1]&0xf6 != 0xf0 {
		return 0, 0, false
	}
	i := int(h[2] >> 2 & 15)
	length = int(h[3]&3)<<11 | int(h[4])<<3 | int(h[5]>>5)
	if i >= len(adtsRates) || length < 7 {
		return 0, 0, false
	}
	return length, adtsRates[i], true
}

func openADTSStream(dl *download, start, durationMs int64) (*adtsStream, error) {
	dl.waitFor(start + 7)
	_, rate, ok := adtsHeader(dl.peek(start, 7))
	if !ok {
		return nil, errors.New("aac: not an ADTS stream")
	}
	s := &adtsStream{dl: dl, fd: aacpcm.NewADTSDecoder(), rate: rate, frames: -1, drop: dl.Close}
	w := window{dl: dl}
	off := start
	s.more = func() bool {
		if dl.waitFor(off+7) < off+7 {
			return false
		}
		length, _, ok := adtsHeader(w.at(off, 7))
		if !ok {
			return false
		}
		s.offs = append(s.offs, off)
		s.sizes = append(s.sizes, length)
		off += int64(length)
		return true
	}
	dl.mu.Lock()
	whole := dl.done || dl.onDisk
	dl.mu.Unlock()
	if whole {
		for s.findMore() {
		}
	} else if durationMs > 0 {
		s.frames = durationMs * int64(rate) / 1000
	}
	s.spec = newSpectrum(rate)
	return s, nil
}

func (s *adtsStream) SampleRate() int { return s.rate }
func (s *adtsStream) Channels() int   { return 2 }
func (s *adtsStream) Len() int64      { return s.frames }

// findMore finds the next frame, if there's one left to find. Once all are
// found, the length is known.
func (s *adtsStream) findMore() bool {
	if s.more == nil {
		return false
	}
	if s.more() {
		return true
	}
	s.more = nil
	s.frames = int64(len(s.offs)) * aac.FrameLength
	return false
}

func (s *adtsStream) Read(dst []float32) (int, error) {
	for len(s.pend) == 0 {
		for s.next >= len(s.offs) {
			if !s.findMore() {
				return 0, io.EOF
			}
		}
		var err error
		s.packet, err = readPacket(s.dl, s.offs[s.next], s.sizes[s.next], s.packet)
		if err != nil {
			if err == io.ErrUnexpectedEOF {
				err = io.EOF
			}
			return 0, err
		}
		s.next++
		pcm, n, err := s.fd.DecodeFrame(s.pcm[:0], s.packet)
		if err != nil {
			continue // a frame gone bad: on to the next
		}
		s.pcm = pcm
		ch := s.fd.Channels()
		right := 0
		if ch > 1 {
			right = 2
		}
		out := s.pend[:0]
		for i := range n {
			f := pcm[i*ch*2:]
			out = append(out,
				float32(int16(binary.LittleEndian.Uint16(f)))/(1<<15),
				float32(int16(binary.LittleEndian.Uint16(f[right:])))/(1<<15))
		}
		drop := min(s.skip, int64(len(out)/2))
		s.skip -= drop
		s.pend = out[drop*2:]
	}
	n := copy(dst[:len(dst)/2*2], s.pend)
	s.pend = s.pend[n:]
	s.spec.feed(s.pos, dst[:n])
	s.pos += int64(n / 2)
	return n, nil
}

// SetPosition goes to the frame holding frame, from the one before, whose
// sound overlaps it, and drops what comes before frame.
func (s *adtsStream) SetPosition(frame int64) error {
	frame = max(frame, 0)
	if s.frames >= 0 {
		frame = min(frame, s.frames)
	}
	i := int(frame / aac.FrameLength)
	for i >= len(s.offs) && s.findMore() {
	}
	start := max(0, i-1)
	s.fd.Reset()
	s.next, s.pos = start, frame
	s.skip = frame - int64(start)*aac.FrameLength
	s.pend = nil
	return nil
}

func (s *adtsStream) Close() error {
	s.once.Do(s.drop)
	return nil
}

func (s *adtsStream) source() (*download, *spectrum) { return s.dl, s.spec }

// readPacket reads the bytes of a packet into buf, waiting for them.
func readPacket(dl *download, off int64, size int, buf []byte) ([]byte, error) {
	if cap(buf) < size {
		buf = make([]byte, size)
	}
	buf = buf[:size]
	if _, err := (&downloadFile{dl: dl}).ReadAt(buf, off); err != nil {
		return nil, err
	}
	return buf, nil
}
