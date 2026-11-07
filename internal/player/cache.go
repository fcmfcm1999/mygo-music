package player

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"mygo-music/internal/music"
)

// Cache keeps the songs played on disk, so that they play again at once,
// and without the network: each named for its source, its ID at the source
// and its quality, as <key>-<quality>.<type>, with what its stream was
// beside it in the same name's .json. Once they take more than the limit,
// those played longest ago go first.
type Cache struct {
	dir string

	mu    sync.Mutex
	limit int64 // in bytes; 0 keeps nothing
}

// NewCache returns a cache in dir of limit bytes.
func NewCache(dir string, limit int64) *Cache {
	return &Cache{dir: dir, limit: limit}
}

// SetLimit changes how much the cache may take, and drops what is over.
func (c *Cache) SetLimit(limit int64) {
	c.mu.Lock()
	c.limit = limit
	c.mu.Unlock()
	c.prune("")
}

// cacheKey names a song's files, whatever the source's IDs look like: they
// are the source's, and may hold anything.
func cacheKey(source, id, quality string) string {
	sum := sha256.Sum256([]byte(source + "\x00" + id))
	return hex.EncodeToString(sum[:16]) + "-" + quality
}

// keepable reports whether a song of quality and type ext may be kept: they
// name its file, so only those known, which keep it in the cache's folder.
func keepable(quality, ext string) bool {
	return slices.Contains(music.Qualities, quality) && slices.Contains(songTypes, ext)
}

// lookup returns a song kept at quality or a better one, open to read, with
// its stream.
func (c *Cache) lookup(source, id, quality string) (*download, *music.Stream, bool) {
	f, st, ok := c.find(source, id, quality)
	if !ok {
		return nil, nil, false
	}
	// Played now: the last to go.
	now := time.Now()
	os.Chtimes(f.Name(), now, now)
	return opened(f, st.Size), st, true
}

// has reports whether a song is kept at quality or a better one.
func (c *Cache) has(source, id, quality string) bool {
	f, _, ok := c.find(source, id, quality)
	if ok {
		f.Close()
	}
	return ok
}

// find opens a song kept at quality or a better one, all of it there, with its
// stream.
func (c *Cache) find(source, id, quality string) (*os.File, *music.Stream, bool) {
	if c == nil {
		return nil, nil, false
	}
	at := max(slices.Index(music.Qualities, quality), 0)
	for _, q := range music.Qualities[at:] {
		key := cacheKey(source, id, q)
		meta, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
		if err != nil {
			continue
		}
		var st music.Stream
		if json.Unmarshal(meta, &st) != nil || !keepable(q, st.Type) {
			continue
		}
		f, err := os.Open(filepath.Join(c.dir, key+"."+st.Type))
		if err != nil {
			continue
		}
		if info, err := f.Stat(); err != nil || info.Size() != st.Size {
			f.Close()
			continue
		}
		return f, &st, true
	}
	return nil, nil, false
}

// download fetches the song of the stream st, asked for at quality, into a
// file, to keep once it has all come in; or into memory, if the cache would
// not keep it.
func (c *Cache) download(source, id, quality string, st *music.Stream) *download {
	part := c.part(st)
	if part == nil {
		return startDownload(st.URL, st.Size, st.Headers, nil, nil)
	}
	return startDownload(st.URL, st.Size, st.Headers, part, func(path string, n int64) (string, bool) {
		return c.keep(source, id, quality, st, path, n)
	})
}

// part makes a file for a song of the stream st to come into, or returns
// nil if the cache would not keep it. Each has a name of its own: the same
// song played again while it still comes in has another.
func (c *Cache) part(st *music.Stream) *os.File {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	limit := c.limit
	c.mu.Unlock()
	// A song of more than a quarter of it would push out too many.
	if limit <= 0 || st.Size > limit/4 {
		return nil
	}
	if os.MkdirAll(c.dir, 0o700) != nil {
		return nil
	}
	f, err := os.CreateTemp(c.dir, "*.part")
	if err != nil {
		return nil
	}
	return f
}

// keep keeps the song of the stream st, n bytes, from the file part, which
// is closed, and drops what is over the limit then. It's kept at the quality
// the source says it is, or else the one asked for. It returns where the song
// is: kept, or still at part.
func (c *Cache) keep(source, id, quality string, st *music.Stream, part string, n int64) (string, bool) {
	c.mu.Lock()
	limit := c.limit
	c.mu.Unlock()
	if n > limit/4 {
		return part, false
	}
	kept := *st
	if kept.Type == "" {
		kept.Type = "mp3"
	}
	kept.Size = n
	// The URL and its headers are of no use once it's here, and a source may
	// sign them.
	kept.URL, kept.Headers = "", nil
	if slices.Contains(music.Qualities, kept.Quality) {
		quality = kept.Quality
	}
	if !keepable(quality, kept.Type) {
		return part, false
	}
	key := cacheKey(source, id, quality)
	meta, _ := json.Marshal(kept)
	path := filepath.Join(c.dir, key+"."+kept.Type)
	if os.Rename(part, path) != nil {
		return part, false
	}
	os.WriteFile(filepath.Join(c.dir, key+".json"), meta, 0o600)
	c.prune(path)
	return path, true
}

// Size returns how many bytes the songs kept take.
func (c *Cache) Size() int64 {
	var n int64
	for _, f := range c.songs() {
		n += f.size
	}
	return n
}

// Clear drops every song kept.
func (c *Cache) Clear() {
	for _, f := range c.songs() {
		c.remove(f.path)
	}
}

type cachedSong struct {
	path string
	size int64
	used time.Time
}

func (c *Cache) songs() []cachedSong {
	entries, _ := os.ReadDir(c.dir)
	var out []cachedSong
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if ext == "" || !slices.Contains(songTypes, ext[1:]) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, cachedSong{filepath.Join(c.dir, e.Name()), info.Size(), info.ModTime()})
	}
	return out
}

func (c *Cache) remove(path string) {
	os.Remove(path)
	os.Remove(strings.TrimSuffix(path, filepath.Ext(path)) + ".json")
}

// prune drops the songs played longest ago until the rest fit the limit,
// but keep, the song just kept.
func (c *Cache) prune(keep string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dropParts()
	songs := c.songs()
	slices.SortFunc(songs, func(a, b cachedSong) int { return a.used.Compare(b.used) })
	var total int64
	for _, f := range songs {
		total += f.size
	}
	for _, f := range songs {
		if total <= c.limit {
			return
		}
		if f.path != keep {
			c.remove(f.path)
			total -= f.size
		}
	}
}

// dropParts removes the files of songs that never all came in, left by an
// app that quit as they came: one coming in is written to every moment.
func (c *Cache) dropParts() {
	parts, _ := filepath.Glob(filepath.Join(c.dir, "*.part"))
	for _, path := range parts {
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.Remove(path)
		}
	}
}
