package music

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// useImageHeaders sets ImageHeaders for a test, putting back what was there.
func useImageHeaders(t *testing.T, f func(*url.URL) map[string]string) {
	was := ImageHeaders
	ImageHeaders = f
	t.Cleanup(func() { ImageHeaders = was })
}

// A picture is asked for with the headers its source gives for it, such as
// the cookie of the session it keeps its covers behind.
func TestFetchWithImageHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "music-token=abc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("cover"))
	}))
	defer srv.Close()
	useImageHeaders(t, func(u *url.URL) map[string]string {
		return map[string]string{"Cookie": "music-token=abc"}
	})
	if b, err := Fetch(srv.URL + "/cover?id=1"); err != nil || string(b) != "cover" {
		t.Fatalf("got %q, %v", b, err)
	}
}

// Headers given for a host go no further than it: a redirect to another is
// asked for without them, so a source's token never reaches a stranger.
// Go itself only kept Cookie and Authorization back.
func TestHeadersStayOnTheirHost(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Token")
		w.Write([]byte("cover"))
	}))
	defer other.Close()
	// 127.0.0.1 and localhost are other hosts, as far as headers go.
	port := must(url.Parse(other.URL)).Port()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:"+port+"/c.jpg", http.StatusFound)
	}))
	defer srv.Close()
	useImageHeaders(t, func(u *url.URL) map[string]string {
		if u.Hostname() != "127.0.0.1" {
			return nil
		}
		return map[string]string{"X-Token": "secret"}
	})
	if _, err := Fetch(srv.URL + "/c.jpg"); err != nil {
		t.Fatal(err)
	}
	if leaked != "" {
		t.Errorf("the redirect took the header along: %q", leaked)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// A picture whose URL says where its size goes, with {size}, as a source
// gives it or escaped, is asked for the size it shows; one shown at no size
// in particular is asked for at the most the app shows one at, and one
// without it as it is.
func TestSizedImageFillsItsPlaceholder(t *testing.T) {
	for _, c := range []struct {
		in   string
		px   int
		want string
	}{
		{"https://img.example.com/a.jpg?param={size}y{size}", 96, "https://img.example.com/a.jpg?param=96y96"},
		{"https://img.example.com/%7Bsize%7D/a.jpg", 240, "https://img.example.com/240/a.jpg"},
		{"https://img.example.com/a.jpg?w={size}", 0, "https://img.example.com/a.jpg?w=600"},
		{"https://img.example.com/a.jpg?size=big", 96, "https://img.example.com/a.jpg?size=big"},
	} {
		if got := SizedImage(c.in, c.px); got != c.want {
			t.Errorf("SizedImage(%q, %d) = %q, want %q", c.in, c.px, got, c.want)
		}
	}
}
