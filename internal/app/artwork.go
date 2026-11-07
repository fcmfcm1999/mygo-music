package app

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"math"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"mygo-music/internal/music"
)

// artwork is a cover, decoded and ready to draw, with what the player
// derives from it: its color, and a blurred copy to fill a backdrop.
type artwork struct {
	bmp      *ui.Bitmap
	backdrop *ui.Bitmap
	tint     ui.Color // the cover's most vivid color
	failed   bool
	used     time.Time // when it was last asked for
	size     int       // the bytes it keeps: its data, and the backdrop's pixels
}

// artCache loads covers on goroutines and keeps them, up to artLimit
// bytes: their pixels on the GPU while they show, and on the CPU only
// their data, which takes a fraction of the pixels. Its methods run on
// the main thread; post runs a function there.
type artCache struct {
	art map[string]*artwork
	// gone are the covers dropped, for as long as something else holds
	// them: Now Playing keeps the cover it shows, which comes back as it
	// was rather than loading again.
	gone map[string]weak.Pointer[artwork]
	busy map[string]bool
	// loads are the covers loading, by their key of sized, and sources the
	// data they are of, by url: the sizes of a cover asked for at once
	// fetch it once between them, and a backdrop decodes with the cover
	// it is of. Each goes once the last that wants it has loaded, so it
	// keeps no data or pixels past that.
	loads   map[string]*artLoad
	sources map[string]*artSource
	sem     chan struct{}
	pixels  *pixelBudget // of the covers decoding at once
	post    func(func())
	now     func() time.Time
	size    int // the bytes the covers in art keep
	limit   int
	// decoded counts the pixels of the covers decoded since the heap
	// last went back to the system, which tidy has it do once they stop.
	decoded int
	tidy    *time.Timer
}

// artLimit is how many bytes of covers the cache keeps. A cover keeps its
// data, some 50–100 KB for Now Playing's and a few for a row's, so this
// is thousands: going back to a page shows its covers as they were,
// however long ago it showed them, rather than loading them again.
const artLimit = 64 << 20

func newArtCache(post func(func())) *artCache {
	return &artCache{art: map[string]*artwork{}, gone: map[string]weak.Pointer[artwork]{}, busy: map[string]bool{}, loads: map[string]*artLoad{}, sources: map[string]*artSource{}, sem: make(chan struct{}, 8), pixels: newPixelBudget(decodePixels), post: post, now: time.Now, limit: artLimit}
}

// add keeps a cover, and drops those asked for least recently once the
// covers take more than the limit, down to three quarters of it, so that
// it sorts them seldom. Those showing were asked for last, and stay.
func (c *artCache) add(key string, a *artwork) {
	c.art[key] = a
	c.size += a.size
	if c.size <= c.limit {
		return
	}
	for key, p := range c.gone {
		if p.Value() == nil {
			delete(c.gone, key)
		}
	}
	keys := make([]string, 0, len(c.art))
	for key := range c.art {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(x, y string) int { return c.art[x].used.Compare(c.art[y].used) })
	for _, key := range keys {
		if c.size <= c.limit*3/4 {
			break
		}
		a := c.art[key]
		delete(c.art, key)
		c.size -= a.size
		c.gone[key] = weak.Make(a)
	}
}

// get returns the cover at url, or nil while it loads.
func (c *artCache) get(url string, backdrop bool) *artwork {
	if url == "" {
		return nil
	}
	key := url
	if backdrop {
		key += "#backdrop"
	}
	now := c.now()
	if a, ok := c.art[key]; ok {
		a.used = now
		return a
	}
	if a := c.gone[key].Value(); a != nil {
		delete(c.gone, key)
		a.used = now
		c.add(key, a)
		return a
	}
	if c.busy[key] {
		return nil
	}
	c.busy[key] = true
	l := c.hold(url)
	go func() {
		c.load(url, l)
		var blurred *image.RGBA
		if l.err == nil && backdrop {
			blurred = blurImage(l.img)
		}
		c.post(func() {
			delete(c.busy, key)
			c.letGo(url, l)
			c.decoded += l.pixels
			l.pixels = 0
			c.tidyUp()
			if l.err != nil {
				c.add(key, &artwork{failed: true, used: c.now()})
				return
			}
			// A backdrop shares the bitmap of the cover it is of, which
			// each counts: the cache keeps either without the other.
			a := &artwork{bmp: l.bmp, tint: l.tint, used: c.now(), size: l.size}
			if blurred != nil {
				a.backdrop = ui.NewBitmap(blurred)
				a.size += len(blurred.Pix)
			}
			c.add(key, a)
			// Shown by the frame post draws, the cover drops its pixels,
			// which its data decodes again if they are needed.
			time.AfterFunc(artRelease, func() { mygo.RunOnMain(l.bmp.ReleasePixels) })
		})
	}()
	return nil
}

// artLoad is a cover loading at a size, for the cover and its backdrop.
type artLoad struct {
	once sync.Once
	src  *artSource
	bmp  *ui.Bitmap
	img  image.Image
	tint ui.Color
	size int
	err  error
	n    int // those waiting for it, counted on the main thread
	// pixels are those of the cover as it came, which decoding it took.
	pixels int
}

// artSource is the data of a cover being fetched, for its sizes loading:
// those its image server makes at the size asked fetch one each.
type artSource struct {
	url  string
	once sync.Once
	data []byte
	err  error
	n    int // the loads of it, counted on the main thread
}

// hold returns the load of the cover a key of sized names, the one
// loading already if there is one, until letGo.
func (c *artCache) hold(key string) *artLoad {
	l := c.loads[key]
	if l == nil {
		url := fetchURL(key)
		s := c.sources[url]
		if s == nil {
			s = &artSource{url: url}
			c.sources[url] = s
		}
		s.n++
		l = &artLoad{src: s}
		c.loads[key] = l
	}
	l.n++
	return l
}

// letGo lets go of a load that hold returned, and of its data once
// nothing loading wants either.
func (c *artCache) letGo(key string, l *artLoad) {
	if l.n--; l.n > 0 {
		return
	}
	if c.loads[key] == l {
		delete(c.loads, key)
	}
	s := l.src
	if s.n--; s.n == 0 && c.sources[s.url] == s {
		delete(c.sources, s.url)
	}
}

// load fetches and decodes the cover a key of sized names, on a goroutine,
// unless another has: those after the first wait for it.
func (c *artCache) load(key string, l *artLoad) {
	l.once.Do(func() {
		_, px := splitSized(key)
		s := l.src
		// A cover holds its place from its fetch to its decode, so that
		// covers fetch no faster than they decode: those fetched waiting
		// kept their data, megabytes each as sources give them.
		c.sem <- struct{}{}
		defer func() { <-c.sem }()
		s.once.Do(func() { s.data, s.err = music.Fetch(s.url) })
		if l.err = s.err; l.err != nil {
			return
		}
		l.pixels = coverPixels(s.data)
		took := c.pixels.take(l.pixels)
		l.bmp, l.img, l.size, l.err = artOf(s.data, px)
		c.pixels.give(took)
		if l.err == nil {
			l.tint = dominantColor(l.img)
		}
	})
}

// decodePixels is how many pixels of covers decode at once: a cover as a
// source gives it can be 3000 pixels across, which decodes into some
// 25 MB before it shrinks to a row's, and eight of those at once took
// hundreds of megabytes. Small covers decode eight at a time as before;
// covers that big, two or three.
const decodePixels = 24 << 20

// coverPixels returns how many pixels the cover of data has, or 0 if its
// header doesn't say.
func coverPixels(data []byte) int {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(stillCover(data)))
	if err != nil {
		return 0
	}
	return cfg.Width * cfg.Height
}

// tidyAfter is how long after the last cover of many decoded the heap
// they took goes back to the system, and tidyPixels how many pixels of
// covers that takes: Go keeps the memory a burst of decoding took for
// minutes, which the system counts as the app's all that while.
const (
	tidyAfter  = 3 * time.Second
	tidyPixels = 32 << 20
)

// tidyUp has the heap the covers decoded took go back to the system, once
// enough decoded and none has for a while.
func (c *artCache) tidyUp() {
	if c.decoded < tidyPixels {
		return
	}
	if c.tidy != nil {
		c.tidy.Reset(tidyAfter)
		return
	}
	var t *time.Timer
	t = time.AfterFunc(tidyAfter, func() {
		c.post(func() {
			if c.tidy != t {
				return
			}
			c.tidy = nil
			// Those still loading tidy up once they have.
			if len(c.loads) > 0 {
				return
			}
			c.decoded = 0
			go debug.FreeOSMemory()
		})
	})
	c.tidy = t
}

// pixelBudget lets those that take pixels of it go on while they fit.
type pixelBudget struct {
	mu         sync.Mutex
	cond       sync.Cond
	free, size int
}

func newPixelBudget(size int) *pixelBudget {
	b := &pixelBudget{free: size, size: size}
	b.cond.L = &b.mu
	return b
}

// take waits until n pixels are free, and takes them: all of them, for
// more than there are. It returns how many it took, to give back.
func (b *pixelBudget) take(n int) int {
	n = min(n, b.size)
	b.mu.Lock()
	for b.free < n {
		b.cond.Wait()
	}
	b.free -= n
	b.mu.Unlock()
	return n
}

func (b *pixelBudget) give(n int) {
	b.mu.Lock()
	b.free += n
	b.mu.Unlock()
	b.cond.Broadcast()
}

// artRelease is how long a cover keeps its pixels once loaded: past the
// frame that first shows it, and the smaller copies it shows from there.
const artRelease = 2 * time.Second

// artOf decodes the data of a cover to show at px, as a bitmap to draw and an
// image to measure, with the bytes of its data. One much bigger than it shows
// is kept smaller: a file's embedded cover can be thousands of pixels across.
// Its bitmap is made only of the smaller copy, never of the cover as it came.
func artOf(b []byte, px int) (*ui.Bitmap, image.Image, int, error) {
	b = stillCover(b)
	img, err := decodeImage(b)
	if err != nil {
		return nil, nil, 0, err
	}
	if size := img.Bounds().Size(); px > 0 && max(size.X, size.Y) > px*3/2 {
		if small, data, err := shrinkArt(img, px); err == nil {
			bmp, err := ui.DecodeBitmap(data)
			if err != nil {
				return nil, nil, 0, err
			}
			return bmp, small, len(data), nil
		}
	}
	bmp, err := ui.DecodeBitmap(b)
	if err != nil {
		return nil, nil, 0, err
	}
	return bmp, img, len(b), nil
}

// fetchURL is the URL to fetch the cover a key of sized names from: one
// the size it shows, from a source whose image server makes it so.
func fetchURL(key string) string {
	url, px := splitSized(key)
	return music.SizedImage(url, px)
}

// splitSized takes a key of sized apart.
func splitSized(key string) (string, int) {
	i := strings.LastIndexByte(key, '#')
	if i < 0 {
		return key, 0
	}
	px, err := strconv.Atoi(key[i+1:])
	if err != nil {
		return key, 0
	}
	return key[:i], px
}

// shrinkArt scales img to px on its longer side, and encodes it for a bitmap,
// which keeps its data to decode its pixels again.
func shrinkArt(img image.Image, px int) (image.Image, []byte, error) {
	size := img.Bounds().Size()
	w, h := px, size.Y*px/size.X
	if size.Y > size.X {
		w, h = size.X*px/size.Y, px
	}
	img = halveTo(img, max(w, h))
	small := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	draw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, small, &jpeg.Options{Quality: 90}); err != nil {
		return nil, nil, err
	}
	return small, buf.Bytes(), nil
}

// halveTo halves img while it stays at least twice px on its longer side,
// each pixel the mean of the four it covers: a scale that blends many
// pixels into each, as one to a row's size from 3000 pixels does, takes
// three times as long without.
func halveTo(img image.Image, px int) image.Image {
	size := img.Bounds().Size()
	for max(size.X, size.Y) >= 4*px {
		half := image.NewRGBA(image.Rect(0, 0, max(size.X/2, 1), max(size.Y/2, 1)))
		draw.ApproxBiLinear.Scale(half, half.Bounds(), img, img.Bounds(), draw.Src, nil)
		img, size = half, half.Bounds().Size()
	}
	return img
}

func fetchImage(url string) (image.Image, error) {
	b, err := music.Fetch(url)
	if err != nil {
		return nil, err
	}
	return decodeImage(b)
}

func decodeImage(b []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(stillCover(b)))
	return img, err
}

// stillCover returns the data of a cover that the decoders read. Some
// covers are animated WebP, whatever their name says, which they cannot:
// those show their first frame.
func stillCover(b []byte) []byte {
	if still := firstWebPFrame(b); still != nil {
		return still
	}
	return b
}

// firstWebPFrame returns the first frame of an animated WebP as a still
// WebP of its own, or nil if b is not one.
func firstWebPFrame(b []byte) []byte {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return nil
	}
	chunk := func(b []byte) (name string, body, rest []byte, ok bool) {
		if len(b) < 8 {
			return "", nil, nil, false
		}
		n := int(binary.LittleEndian.Uint32(b[4:8]))
		if n > len(b)-8 {
			return "", nil, nil, false
		}
		end := min(len(b), 8+n+n&1)
		return string(b[:4]), b[8 : 8+n], b[end:], true
	}
	riff := func(chunks ...[]byte) []byte {
		body := []byte("WEBP")
		for _, c := range chunks {
			body = append(body, c...)
		}
		out := binary.LittleEndian.AppendUint32([]byte("RIFF"), uint32(len(body)))
		return append(out, body...)
	}
	for rest := b[12:]; ; {
		name, body, next, ok := chunk(rest)
		if !ok {
			return nil
		}
		rest = next
		if name != "ANMF" || len(body) < 16 {
			continue
		}
		// A frame: where it sits, its size, how long it shows, then its
		// image as the chunks of a still WebP, its alpha first if any.
		w, h := body[6:9], body[9:12]
		frame := body[16:]
		var alpha []byte
		for len(frame) > 0 {
			name, _, after, ok := chunk(frame)
			if !ok {
				return nil
			}
			whole := frame[:len(frame)-len(after)]
			switch name {
			case "ALPH":
				alpha = whole
			case "VP8L":
				return riff(whole)
			case "VP8 ":
				if alpha == nil {
					return riff(whole)
				}
				// Alpha needs the extended header, which carries the size.
				vp8x := []byte{'V', 'P', '8', 'X', 10, 0, 0, 0, 0x10, 0, 0, 0}
				vp8x = append(append(vp8x, w...), h...)
				return riff(vp8x, alpha, whole)
			}
			frame = after
		}
		return nil
	}
}

// shrink averages img down to n×n pixels.
func shrink(img image.Image, n int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			x0, x1 := b.Min.X+x*b.Dx()/n, b.Min.X+(x+1)*b.Dx()/n
			y0, y1 := b.Min.Y+y*b.Dy()/n, b.Min.Y+(y+1)*b.Dy()/n
			var r, g, bl, cnt uint32
			for yy := y0; yy < max(y1, y0+1); yy += max(1, (y1-y0)/4) {
				for xx := x0; xx < max(x1, x0+1); xx += max(1, (x1-x0)/4) {
					cr, cg, cb, _ := img.At(xx, yy).RGBA()
					r, g, bl, cnt = r+cr>>8, g+cg>>8, bl+cb>>8, cnt+1
				}
			}
			out.SetRGBA(x, y, color.RGBA{uint8(r / cnt), uint8(g / cnt), uint8(bl / cnt), 255})
		}
	}
	return out
}

// dominantColor returns the most vivid color that covers much of img: the
// hue that its saturated pixels share most, at their mean.
func dominantColor(img image.Image) ui.Color {
	small := shrink(img, 24)
	const buckets = 18
	var weight [buckets]float64
	var sum [buckets][3]float64
	var gray [3]float64
	for i := 0; i < len(small.Pix); i += 4 {
		r, g, b := float64(small.Pix[i])/255, float64(small.Pix[i+1])/255, float64(small.Pix[i+2])/255
		h, s, v := hsv(r, g, b)
		gray[0], gray[1], gray[2] = gray[0]+r, gray[1]+g, gray[2]+b
		w := s * s * v
		if s < 0.15 || v < 0.15 {
			continue
		}
		k := int(h/360*buckets) % buckets
		weight[k] += w
		sum[k][0], sum[k][1], sum[k][2] = sum[k][0]+r*w, sum[k][1]+g*w, sum[k][2]+b*w
	}
	best := 0
	for k := range weight {
		if weight[k] > weight[best] {
			best = k
		}
	}
	if weight[best] < 2 {
		n := float64(len(small.Pix) / 4)
		return ui.RGB(uint8(gray[0]/n*255), uint8(gray[1]/n*255), uint8(gray[2]/n*255))
	}
	w := weight[best]
	return ui.RGB(uint8(sum[best][0]/w*255), uint8(sum[best][1]/w*255), uint8(sum[best][2]/w*255))
}

func hsv(r, g, b float64) (h, s, v float64) {
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	v = mx
	if mx == 0 {
		return 0, 0, 0
	}
	s = (mx - mn) / mx
	d := mx - mn
	switch {
	case d == 0:
		h = 0
	case mx == r:
		h = math.Mod((g-b)/d, 6) * 60
	case mx == g:
		h = ((b-r)/d + 2) * 60
	default:
		h = ((r-g)/d + 4) * 60
	}
	if h < 0 {
		h += 360
	}
	return h, s, v
}

// blurImage returns img shrunk and blurred, more saturated, to fill a
// backdrop behind what plays, scaled up as smooth as it is.
func blurImage(img image.Image) *image.RGBA {
	const n = 48
	small := shrink(img, n)
	px := make([][3]float64, n*n)
	for i := range px {
		r, g, b := float64(small.Pix[4*i]), float64(small.Pix[4*i+1]), float64(small.Pix[4*i+2])
		// More saturated, as light through colored glass.
		l := 0.3*r + 0.59*g + 0.11*b
		px[i] = [3]float64{l + (r-l)*1.5, l + (g-l)*1.5, l + (b-l)*1.5}
	}
	tmp := make([][3]float64, n*n)
	for range 3 {
		boxBlur(px, tmp, n, 5)
	}
	out := image.NewRGBA(image.Rect(0, 0, n, n))
	for i, p := range px {
		for c := range 3 {
			out.Pix[4*i+c] = uint8(math.Max(0, math.Min(255, p[c])))
		}
		out.Pix[4*i+3] = 255
	}
	return out
}

// boxBlur blurs px of n×n pixels by a box of radius r, across then down,
// clamping at the edges.
func boxBlur(px, tmp [][3]float64, n, r int) {
	at := func(i int) int { return max(0, min(n-1, i)) }
	for y := range n {
		for x := range n {
			var s [3]float64
			for k := -r; k <= r; k++ {
				p := px[y*n+at(x+k)]
				s[0], s[1], s[2] = s[0]+p[0], s[1]+p[1], s[2]+p[2]
			}
			d := float64(2*r + 1)
			tmp[y*n+x] = [3]float64{s[0] / d, s[1] / d, s[2] / d}
		}
	}
	for y := range n {
		for x := range n {
			var s [3]float64
			for k := -r; k <= r; k++ {
				p := tmp[at(y+k)*n+x]
				s[0], s[1], s[2] = s[0]+p[0], s[1]+p[1], s[2]+p[2]
			}
			d := float64(2*r + 1)
			px[y*n+x] = [3]float64{s[0] / d, s[1] / d, s[2] / d}
		}
	}
}
