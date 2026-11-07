package app

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// An animated WebP cover, as some playlists have, shows its first frame,
// in the bitmap too, which decodes its data again once it drops its pixels.
func TestAnimatedWebPCover(t *testing.T) {
	b, err := os.ReadFile("testdata/animated.webp")
	if err != nil {
		t.Fatal(err)
	}
	bmp, img, _, err := artOf(b, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r := img.Bounds(); r.Dx() != 24 || r.Dy() != 24 {
		t.Errorf("first frame is %v, want 24×24", r)
	}
	if w, h := bmp.Size(); w != 24 || h != 24 {
		t.Errorf("bitmap is %d×%d, want 24×24", w, h)
	}
}

// A cover much bigger than it shows makes its bitmap of the smaller copy
// alone. It made one of the whole cover first, then dropped it: a 96-pixel
// thumbnail of a 4000-pixel cover took some 120 MB to load.
func TestBigCoverThumbnailSkipsFullBitmap(t *testing.T) {
	const n = 2000
	src := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			src.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var data bytes.Buffer
	if err := jpeg.Encode(&data, src, nil); err != nil {
		t.Fatal(err)
	}
	src = nil

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	bmp, img, _, err := artOf(data.Bytes(), 96)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := bmp.Size(); w != 96 || h != 96 {
		t.Errorf("bitmap is %d×%d, want 96×96", w, h)
	}
	if r := img.Bounds(); r.Dx() != 96 || r.Dy() != 96 {
		t.Errorf("image is %v, want 96×96", r)
	}
	// Decoding the JPEG takes some 1.5 bytes a pixel and scaling it some
	// 1.5 more; a full RGBA bitmap took 4, and decoding it again 1.5.
	if got, budget := after.TotalAlloc-before.TotalAlloc, uint64(4*n*n); got > budget {
		t.Errorf("loading a 96-pixel thumbnail of a %d-pixel cover allocated %d bytes, budget %d", n, got, budget)
	}
}

// Once the covers take more than the limit, those asked for least
// recently go, so that a long session does not keep every cover it
// showed; one asked for again stays, however long ago it loaded.
func TestArtCacheDropsLeastRecentCovers(t *testing.T) {
	now := time.Unix(1e9, 0)
	c := newArtCache(func(func()) {})
	c.now, c.limit = func() time.Time { return now }, 100
	for _, key := range []string{"first", "second", "third"} {
		c.add(key, &artwork{used: now, size: 30})
		now = now.Add(time.Minute)
	}
	c.get("first", false)
	c.add("fourth", &artwork{used: now, size: 30})
	if _, ok := c.art["second"]; ok {
		t.Error("kept the cover asked for least recently over the limit")
	}
	for _, key := range []string{"first", "fourth"} {
		if _, ok := c.art[key]; !ok {
			t.Errorf("dropped %q, asked for last", key)
		}
	}
}

// Covers stay under the limit however long since they showed. They went
// after 5 minutes not asked for: going back to a page after a while on
// others, or to the window after a while away, loaded them again.
func TestArtCacheKeepsCoversUnderLimit(t *testing.T) {
	now := time.Unix(1e9, 0)
	c := newArtCache(func(func()) {})
	c.now = func() time.Time { return now }
	page := &artwork{used: now, size: 100 << 10}
	c.add("page", page)
	for range 60 {
		now = now.Add(time.Minute)
		c.get("other", false)
	}
	if c.get("page", false) != page {
		t.Error("dropped a cover not asked for in an hour, under the limit")
	}
}

// A cover dropped while something still holds it comes back as it was.
// It loaded again: Now Playing's cover went while other pages showed,
// though Now Playing still held it, and flashed in once it opened again.
func TestArtCacheKeepsHeldCovers(t *testing.T) {
	now := time.Unix(1e9, 0)
	c := newArtCache(func(func()) {})
	c.now, c.limit = func() time.Time { return now }, 60
	held := &artwork{used: now, size: 30}
	c.add("now playing", held)
	for _, key := range []string{"other", "another"} {
		now = now.Add(time.Minute)
		c.add(key, &artwork{used: now, size: 30})
	}
	if _, ok := c.art["now playing"]; ok {
		t.Fatal("kept a cover over the limit")
	}
	if got := c.get("now playing", false); got != held {
		t.Error("loaded again a cover something still held")
	}
	runtime.KeepAlive(held)
}

// The sizes of a cover asked for at once fetch it once between them, and
// a backdrop shares the decode of the cover it is of, keeping neither past
// their loads. Now Playing's cover, its backdrop and the player bar's
// thumbnail each fetched and decoded the same cover.
func TestArtCacheSharesCoverAcrossSizes(t *testing.T) {
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 800, 800)), nil); err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Write(data.Bytes())
	}))
	defer srv.Close()
	posts := make(chan func(), 8)
	c := newArtCache(func(fn func()) { posts <- fn })
	url := srv.URL + "/cover.jpg"
	c.get(sized(url, 96), false)
	c.get(sized(url, 600), false)
	c.get(sized(url, 600), true)
	for range 3 {
		runPost(t, posts)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("three sizes of a cover fetched it %d times, want once", n)
	}
	cover, backdrop := c.get(sized(url, 600), false), c.get(sized(url, 600), true)
	if cover == nil || backdrop == nil || backdrop.backdrop == nil {
		t.Fatal("the cover or its backdrop did not load")
	}
	if cover.bmp != backdrop.bmp {
		t.Error("the backdrop decoded the cover it is of again")
	}
	if len(c.loads) > 0 || len(c.sources) > 0 {
		t.Errorf("kept %d loads and %d sources once loaded", len(c.loads), len(c.sources))
	}
}

// Covers decode while their pixels fit together, and one with more than
// there are decodes alone rather than waiting for ever: eight covers
// thousands of pixels across, decoding at once, took hundreds of
// megabytes before they shrank to a row's.
func TestCoversDecodeWhileTheirPixelsFit(t *testing.T) {
	b := newPixelBudget(100)
	done := make(chan int)
	go func() { done <- b.take(1000) }()
	select {
	case n := <-done:
		b.give(n)
	case <-time.After(time.Second):
		t.Fatal("a cover bigger than the budget waited for ever")
	}

	first := b.take(60)
	go func() { done <- b.take(60) }()
	select {
	case <-done:
		t.Fatal("two covers decoded at once whose pixels don't fit together")
	case <-time.After(50 * time.Millisecond):
	}
	b.give(first)
	select {
	case n := <-done:
		b.give(n)
	case <-time.After(time.Second):
		t.Fatal("a cover still waited once the one before it decoded")
	}
}

// A cover whose URL says where its size goes is fetched the size it shows,
// each size its own: the app fetched a plugin's covers as large as they
// were made, a few megabytes each, for rows of 40 pixels.
func TestCoverFetchedTheSizeItShows(t *testing.T) {
	var data bytes.Buffer
	jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil)
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.RawQuery)
		mu.Unlock()
		w.Write(data.Bytes())
	}))
	defer srv.Close()
	posts := make(chan func(), 8)
	c := newArtCache(func(fn func()) { posts <- fn })
	cover := srv.URL + "/cover.jpg?param={size}y{size}"
	c.get(sized(cover, 96), false)
	c.get(sized(cover, 600), false)
	for range 2 {
		runPost(t, posts)
	}
	slices.Sort(asked)
	if want := []string{"param=600y600", "param=96y96"}; !slices.Equal(asked, want) {
		t.Errorf("fetched %q, want %q", asked, want)
	}
}
