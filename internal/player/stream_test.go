package player

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"mygo-music/internal/music"
)

func TestFrameSize(t *testing.T) {
	// MPEG-1 layer III, 320 kbps, 44.1 kHz, no padding.
	if n := frameSize([]byte{0xff, 0xfb, 0xe0, 0x44}); n != 1044 {
		t.Errorf("frame size %d", n)
	}
	if n := frameSize([]byte{0xff, 0x00, 0xe0, 0x44}); n != 0 {
		t.Errorf("no frame: %d", n)
	}
}

// decodeAll decodes the whole of a song from its start.
func decodeAll(t *testing.T, b []byte) []float32 {
	t.Helper()
	s, err := openStream(downloaded(b), 600000)
	if err != nil {
		t.Fatal(err)
	}
	var out []float32
	buf := make([]float32, 4096)
	for {
		n, err := s.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil || n == 0 {
			return out
		}
	}
}

// maxDiff returns how far apart a and b are at most, over the length of
// the shorter.
func maxDiff(a, b []float32) float64 {
	d := 0.0
	for i := range min(len(a), len(b)) {
		d = math.Max(d, math.Abs(float64(a[i]-b[i])))
	}
	return d
}

// A seek into the middle of a 128 kbps MP3, whose frames borrow bits from
// two frames back, plays the music there, not a burst of noise.
func TestMP3SeekIsClean(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	ref := decodeAll(t, b)
	dl := downloaded(b)
	s, err := openMP3Stream(dl, int64(len(ref)/2)*1000/44100)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 4096)
	for i := 1; i < 40; i++ {
		frame := s.frames * int64(i) / 41
		if err := s.SetPosition(frame); err != nil {
			t.Fatal(err)
		}
		n, _ := s.Read(buf)
		// Where the decoder started, counted in MP3 frames from the first,
		// and what it played from there on.
		var at int64
		if from := frame - preroll; from > 0 {
			off := s.start + int64(float64(dl.size-s.start)*float64(from)/float64(s.frames))
			off = findFrame(dl, off)
			k := int64(0)
			for p := s.start; p < off; p += int64(frameSize(dl.peek(p, 4))) {
				k++
			}
			at = (k*1152 + preroll) * 2
		} else {
			at = frame * 2
		}
		if d := maxDiff(buf[:n], ref[at:]); d > 1e-4 {
			t.Errorf("seek to frame %d: off by up to %.3f", frame, d)
		}
	}
}

// A FLAC plays as its samples are, and a seek lands on the very sample.
func TestFLACSeek(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.flac")
	if err != nil {
		t.Fatal(err)
	}
	ref := decodeAll(t, b)
	if len(ref) != 5*44100*2 {
		t.Fatalf("decoded %d samples", len(ref))
	}
	s, err := openStream(downloaded(b), 5000)
	if err != nil {
		t.Fatal(err)
	}
	if s.SampleRate() != 44100 || s.Len() != 5*44100 {
		t.Fatalf("rate %d, length %d", s.SampleRate(), s.Len())
	}
	buf := make([]float32, 4096)
	for _, frame := range []int64{0, 1, 4095, 4096, 50000, 123457, 200000} {
		if err := s.SetPosition(frame); err != nil {
			t.Fatal(err)
		}
		n, err := s.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		if d := maxDiff(buf[:n], ref[frame*2:]); d != 0 {
			t.Errorf("seek to %d: off by up to %.4f", frame, d)
		}
	}
}

// A FLAC whose metadata block is of a length with bit 2 set plays from its
// first frame. The block's length was ORed into the 4 bytes of its header
// rather than added to them, so most lossless songs started 4
// bytes early, failed to decode and were skipped.
func TestFLACMetadataLength(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.flac")
	if err != nil {
		t.Fatal(err)
	}
	ref := decodeAll(t, b)
	// The last block's header is no longer the last: a padding block of 6
	// bytes follows it.
	off := int64(4)
	for b[off]&0x80 == 0 {
		off += 4 + (int64(b[off+1])<<16 | int64(b[off+2])<<8 | int64(b[off+3]))
	}
	start := off + 4 + (int64(b[off+1])<<16 | int64(b[off+2])<<8 | int64(b[off+3]))
	padded := append([]byte(nil), b[:start]...)
	padded[off] &^= 0x80
	padded = append(padded, 0x81, 0, 0, 6, 0, 0, 0, 0, 0, 0)
	padded = append(padded, b[start:]...)

	s, err := openStream(downloaded(padded), 5000)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 4096)
	n, err := s.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if d := maxDiff(buf[:n], ref); d != 0 {
		t.Errorf("off by up to %.4f", d)
	}
}

// A song plays as it streams into a file of the cache, rather than the
// app's memory, and the cache keeps it once it has all come in; one cut
// short leaves no file behind.
func TestStreamIntoCache(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	half := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cut" {
			w.Header().Set("Content-Length", strconv.Itoa(len(b)))
			w.Write(b[:len(b)/2])
			w.(http.Flusher).Flush()
			<-half
			return
		}
		w.Write(b)
	}))
	defer srv.Close()
	defer close(half)
	dir := t.TempDir()
	c := NewCache(dir, 1<<30)
	ref := decodeAll(t, b)

	st := &music.Stream{URL: srv.URL + "/whole", Size: int64(len(b)), Quality: "high", Type: "mp3"}
	s, err := openStream(c.download("src", "1", "high", st), int64(len(ref)/2)*1000/44100)
	if err != nil {
		t.Fatal(err)
	}
	var got []float32
	buf := make([]float32, 4096)
	for {
		n, err := s.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil || n == 0 {
			break
		}
	}
	s.Close()
	if d := maxDiff(got, ref); len(got) != len(ref) || d != 0 {
		t.Errorf("played %d of %d samples, off by up to %.4f", len(got), len(ref), d)
	}
	if dl, _, ok := c.lookup("src", "1", "high"); !ok {
		t.Error("not kept")
	} else {
		dl.Close()
	}

	cut := c.download("src", "2", "high", &music.Stream{URL: srv.URL + "/cut", Size: int64(len(b)), Quality: "high", Type: "mp3"})
	cut.waitFor(int64(len(b) / 2))
	cut.Close()
	for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
		if parts, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(parts) == 0 {
			break
		}
	}
	if parts, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(parts) != 0 {
		t.Errorf("left %v", parts)
	}
	if _, _, ok := c.lookup("src", "2", "high"); ok {
		t.Error("kept a song cut short")
	}
}

// A stream whose type would lead out of the cache's folder is played but not
// kept, nor is one asked for at a quality that would. They name the file, taken
// as the source sent them, so a type of "/../../x" wrote the song and its JSON
// beside the cache instead.
func TestCacheKeepsOnlyKnownNames(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(b) }))
	defer srv.Close()
	root := t.TempDir()
	dir := filepath.Join(root, "cache")
	c := NewCache(dir, 1<<30)

	for _, tc := range []struct{ quality, typ string }{{"/../../pwned", "mp3"}, {"high", "/../../pwned"}} {
		dl := c.download("src", "1", tc.quality, &music.Stream{URL: srv.URL, Size: int64(len(b)), Type: tc.typ})
		dl.waitFor(int64(len(b)))
		dl.Close()
		for start := time.Now(); time.Since(start) < 2*time.Second; time.Sleep(10 * time.Millisecond) {
			if parts, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(parts) == 0 {
				break
			}
		}
	}
	var outside []string
	filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() && filepath.Dir(path) != dir {
			outside = append(outside, path)
		}
		return nil
	})
	if len(outside) != 0 {
		t.Errorf("wrote %v outside the cache", outside)
	}
	if songs := c.songs(); len(songs) != 0 {
		t.Errorf("kept %v", songs)
	}
}

// What a source says a stream needs goes with the request for it.
func TestStreamHeaders(t *testing.T) {
	got := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header
		w.Write([]byte("sound"))
	}))
	defer srv.Close()
	dl := NewCache(t.TempDir(), 1<<30).download("src", "1", "high", &music.Stream{URL: srv.URL, Type: "mp3", Headers: map[string]string{"Authorization": "Bearer token"}})
	defer dl.Close()
	select {
	case h := <-got:
		if h.Get("Authorization") != "Bearer token" {
			t.Errorf("headers %v", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request")
	}
	// It ends before the test does: the cache kept the song as the folder was
	// being removed, and the test failed for what was left in it.
	dl.waitFor(1 << 30)
}

// fileSource answers a file for any track, as the built-in source does.
type fileSource struct {
	nothing
	path string
}

func (s fileSource) Info() music.Info { return music.Info{ID: "disk", Playback: music.PlaybackStream} }
func (s fileSource) Stream(id, quality string) (*music.Stream, error) {
	return &music.Stream{URL: music.FileURL(s.path), Type: "mp3"}, nil
}

// A file:// stream plays from disk, leaving nothing in the cache and asking the
// network for nothing, and a seek lands by where the file's frames really start:
// a variable bit rate would go wrong by proportion.
func TestFileStreamSeeks(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	ref := decodeAll(t, b)
	// Bytes that aren't sound, after the frames, make time and bytes out of
	// proportion, as a variable bit rate does.
	path := filepath.Join(t.TempDir(), "my song #1.mp3")
	if err := os.WriteFile(path, append(slices.Clone(b), make([]byte, len(b)/2)...), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := NewCache(t.TempDir(), 1<<30)
	p := &Player{Cache: cache}
	src := fileSource{path: path}
	info, s, err := p.open(src, music.Track{ID: "1", Source: "disk", Duration: int64(len(ref)/2) * 1000 / 44100}, "high", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, isFile := music.PathOf(info.URL); !isFile {
		t.Fatalf("url %q", info.URL)
	}
	buf := make([]float32, 4096)
	for _, frame := range []int64{0, 100, 44100, 123457, s.Len() - 8192} {
		if err := s.SetPosition(frame); err != nil {
			t.Fatal(err)
		}
		n, err := s.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		if d := maxDiff(buf[:n], ref[frame*2:]); d > 1e-4 {
			t.Errorf("seek to %d: off by up to %.4f", frame, d)
		}
	}
	if songs := cache.songs(); len(songs) != 0 {
		t.Errorf("the cache took %v", songs)
	}
}

// A song of no known size seeks while it comes in, by its bit rate, and lands
// where it would with its size known. It used to wait for all of it: a
// plugin's, converted as it's fetched, has no size, and a click past what had
// come in played nothing for as long as the rest of the song took.
func TestSeekBeforeAllIsIn(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	durationMs := int64(len(decodeAll(t, b))/2) * 1000 / 44100
	rest := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(b[:len(b)*3/4])
		w.(http.Flusher).Flush()
		<-rest
	}))
	defer srv.Close()
	defer close(rest)
	dl := (*Cache)(nil).download("s", "1", "high", &music.Stream{URL: srv.URL, Type: "mp3"})
	s, err := openStream(dl, durationMs)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	whole, err := openStream(downloaded(b), durationMs)
	if err != nil {
		t.Fatal(err)
	}
	frame := int64(2 * 44100)
	done := make(chan error, 1)
	go func() { done <- s.SetPosition(frame) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waited for the rest of the song")
	}
	if err := whole.SetPosition(frame); err != nil {
		t.Fatal(err)
	}
	got, want := make([]float32, 4096), make([]float32, 4096)
	n, err := s.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := whole.Read(want)
	if d := maxDiff(got[:n], want[:m]); d > 1e-4 {
		t.Errorf("landed apart from where it would with its size, by up to %.4f", d)
	}
}

// A song with no duration says how long it is once its frames are counted.
func TestFileStreamLengthOfFrames(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.mp3")
	if err != nil {
		t.Fatal(err)
	}
	ref := decodeAll(t, b)
	path := filepath.Join(t.TempDir(), "x.mp3")
	os.WriteFile(path, b, 0o600)
	dl, err := openFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := openStream(dl, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetPosition(0); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Len(), int64(len(ref)/2); got != want {
		t.Errorf("length %d, want %d", got, want)
	}
}

// A song the cache won't keep sets aside no more memory than maxPrealloc,
// whatever size the API says it is, which it set aside in full before.
func TestStreamSizeIsNotTrusted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("short")) }))
	defer srv.Close()
	dl := startDownload(srv.URL, 1<<40, nil, nil, nil)
	defer dl.Close()
	dl.mu.Lock()
	n := cap(dl.data)
	dl.mu.Unlock()
	if n > maxPrealloc {
		t.Errorf("set aside %d bytes", n)
	}
}

// An Ogg Vorbis file gives its length and lands on the frame it's asked for.
func TestOggSeek(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.ogg")
	if err != nil {
		t.Fatal(err)
	}
	straight, err := openStream(downloaded(b), 0)
	if err != nil {
		t.Fatal(err)
	}
	// The encoder rounds a file up to its last block.
	if n := straight.Len(); straight.SampleRate() != 44100 || n < 5*44100 || n > 5*44100+1024 {
		t.Fatalf("rate %d, length %d", straight.SampleRate(), n)
	}
	var ref []float32
	buf := make([]float32, 4096)
	for {
		n, err := straight.Read(buf)
		ref = append(ref, buf[:n]...)
		if err != nil {
			break
		}
	}
	if int64(len(ref)) < 5*44100*2 {
		t.Fatalf("decoded %d samples", len(ref))
	}
	s, err := openStream(downloaded(b), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []int64{0, 4096, 50000, 123457, 200000} {
		if err := s.SetPosition(frame); err != nil {
			t.Fatal(err)
		}
		n, err := s.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		// Past the first block, which overlaps what a straight decode had before it.
		if d := maxDiff(buf[n/2:n], ref[frame*2+int64(n/2):]); d > 0.001 {
			t.Errorf("seek to %d: off by up to %.4f", frame, d)
		}
	}
}

// One still coming in plays without waiting for its end, with the length its
// source says, and only waits for the rest once it's asked to seek.
func TestOggComingIn(t *testing.T) {
	b, err := os.ReadFile("testdata/seek.ogg")
	if err != nil {
		t.Fatal(err)
	}
	dl := downloaded(b)
	dl.done = false
	s, err := openStream(dl, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 4*44100 {
		t.Fatalf("length %d: not what the source said", s.Len())
	}
	buf := make([]float32, 4096)
	if _, err := s.Read(buf); err != nil {
		t.Fatal(err)
	}
	dl.mu.Lock()
	dl.done = true
	dl.cond.Broadcast()
	dl.mu.Unlock()
	if err := s.SetPosition(100000); err != nil {
		t.Fatal(err)
	}
	if s.Len() < 5*44100 {
		t.Fatalf("length %d once it could tell", s.Len())
	}
	if _, err := s.Read(buf); err != nil {
		t.Fatal(err)
	}
}

// The spectrum keeps the last seconds decoded, however long the audio: hours
// in, it measures what plays, forgets what played long ago, and measures again
// after a seek back.
func TestSpectrumKeepsRecentChunks(t *testing.T) {
	const rate = 44100
	s := newSpectrum(rate)
	if keep := time.Duration(len(s.ring)) * fftSize * time.Second / rate; keep < spectrumKeep || keep > spectrumKeep+time.Second {
		t.Fatalf("keeps %v of chunks, want about %v", keep, spectrumKeep)
	}
	tone := func(at int64, frames int) {
		stereo := make([]float32, frames*2)
		for i := range frames {
			v := float32(0.5 * math.Sin(2*math.Pi*440*float64(at+int64(i))/rate))
			stereo[2*i], stereo[2*i+1] = v, v
		}
		s.feed(at, stereo)
	}
	measured := func(at time.Duration) bool {
		_, ok := s.at(int64(at.Seconds() * rate))
		return ok
	}

	// Ten hours in, as an audiobook seeks to where it was left.
	start := int64(10 * 60 * 60 * rate)
	tone(start, 30*rate)
	end := time.Duration(start/rate)*time.Second + 30*time.Second
	if !measured(end - time.Second) {
		t.Error("a second before the end decoded isn't measured")
	}
	if measured(end - 25*time.Second) {
		t.Error("25 seconds before the end decoded is still kept")
	}
	if measured(end + time.Second) {
		t.Error("what isn't decoded yet is measured")
	}
	if !measured(end - spectrumKeep + time.Second) {
		t.Errorf("%v before the end decoded isn't kept", spectrumKeep-time.Second)
	}

	// A seek back decodes, and measures, again.
	back := time.Duration(start/rate)*time.Second + 2*time.Second
	tone(int64(back.Seconds()*rate), rate)
	if !measured(back + 500*time.Millisecond) {
		t.Error("what plays after a seek back isn't measured")
	}
}
