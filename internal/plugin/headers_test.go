package plugin

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// A plugin's requestHeaders go with the pictures and audio of their hosts,
// filled in from its settings and storage as they are when asked, so a token
// it changes goes with the next request; a header of a value not there yet
// is left out.
func TestRequestHeadersFilledAsAsked(t *testing.T) {
	p := load(t, `{"id":"x","permissions":{"network":["*"]},
  "requestHeaders":[
    {"hosts":["{setting:server}"],"headers":{"Cookie":"music-token={storage:token}"}},
    {"hosts":["img.example.com"],"kinds":["image"],"headers":{"Referer":"https://example.com/"}}
  ]}`, `
export function signIn() { host.storage.set("token", "first"); return { id: "me", name: "Me" }; }
export function home() { host.storage.set("token", "second"); return {}; }`)
	if err := p.SetSetting("server", "https://NAS.local:5666/"); err != nil {
		t.Fatal(err)
	}
	cover := mustURL("http://nas.local:5666/music/api/v1/static/cover?id=1")
	if h := p.HeadersFor(cover, KindImage); h != nil {
		t.Errorf("before signing in: %v", h)
	}
	if _, err := p.SignIn(); err != nil {
		t.Fatal(err)
	}
	if h := p.HeadersFor(cover, KindImage); h["Cookie"] != "music-token=first" {
		t.Errorf("signed in: %v", h)
	}
	if _, err := p.Home(); err != nil {
		t.Fatal(err)
	}
	if h := p.HeadersFor(cover, KindAudio); h["Cookie"] != "music-token=second" {
		t.Errorf("after the token changed: %v", h)
	}
	if h := p.HeadersFor(mustURL("https://img.example.com/a.jpg"), KindAudio); h != nil {
		t.Errorf("an image rule went with audio: %v", h)
	}
	if h := p.HeadersFor(mustURL("https://img.example.com/a.jpg"), KindImage); h["Referer"] != "https://example.com/" {
		t.Errorf("image rule: %v", h)
	}
	if h := p.HeadersFor(mustURL("https://elsewhere.example/a.jpg"), KindImage); h != nil {
		t.Errorf("went to a host not named: %v", h)
	}
}

// A stream's audio takes the plugin's requestHeaders, under the headers the
// stream gives itself.
func TestStreamTakesRequestHeaders(t *testing.T) {
	p := load(t, `{"id":"x","permissions":{"network":["api.example.com"]},
  "requestHeaders":[{"hosts":["api.example.com"],"headers":{"Cookie":"a=1","X-Client":"mygo"}}]}`, `
export function stream() {
  return { url: "https://api.example.com/audio/1.mp3", type: "mp3", headers: { "x-client": "own" } };
}`)
	st, err := p.Stream("1", "")
	if err != nil {
		t.Fatal(err)
	}
	if st.Headers["Cookie"] != "a=1" || st.Headers["x-client"] != "own" || st.Headers["X-Client"] != "" {
		t.Errorf("headers %v", st.Headers)
	}
}

// A plugin can't name a host for its headers that its network permission
// doesn't reach, or every host, so they go only where it already fetches.
func TestRequestHeadersStayInPermission(t *testing.T) {
	for manifest, want := range map[string]string{
		`{"id":"x","permissions":{"network":["api.example.com"]},"requestHeaders":[{"hosts":["cdn.example.com"],"headers":{"a":"b"}}]}`: "outside the network permission",
		`{"id":"x","permissions":{"network":["*"]},"requestHeaders":[{"hosts":["*"],"headers":{"a":"b"}}]}`:                             "isn't a host",
		`{"id":"x","permissions":{"network":["*"]},"requestHeaders":[{"hosts":["a.com"],"kinds":["video"],"headers":{"a":"b"}}]}`:       "isn't image or audio",
		`{"id":"x","permissions":{"network":["*"]},"requestHeaders":[{"hosts":["a.com"],"headers":{"Host":"b"}}]}`:                      "isn't a header it can set",
	} {
		dir, data := write(t, manifest, `export function home() { return {}; }`)
		if _, err := Load(dir, data); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", manifest, err, want)
		}
	}
	p := load(t, `{"id":"x","permissions":{"network":["*.example.com"]},"requestHeaders":[{"hosts":["img.example.com","{setting:server}"],"headers":{"a":"b"}}]}`, `export function home() { return {}; }`)
	// A server the listener names outside the permission gets nothing.
	if err := p.SetSetting("server", "evil.test"); err != nil {
		t.Fatal(err)
	}
	if h := p.HeadersFor(mustURL("https://evil.test/c.jpg"), KindImage); h != nil {
		t.Errorf("sent %v to a server outside the network permission", h)
	}
}

// A stored value breaking a header's line doesn't go.
func TestRequestHeadersRefuseLineBreaks(t *testing.T) {
	dir, data := write(t, `{"id":"x","permissions":{"network":["a.com"]},"requestHeaders":[{"hosts":["a.com"],"headers":{"Cookie":"t={storage:token}"}}]}`, `export function home() { return {}; }`)
	os.MkdirAll(filepath.Join(data, "x"), 0o700)
	os.WriteFile(filepath.Join(data, "x", "storage.json"), []byte(`{"token":"a\r\nX-Evil: 1"}`), 0o600)
	p, err := Load(dir, data)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if h := p.HeadersFor(mustURL("https://a.com/c.jpg"), KindImage); h != nil {
		t.Errorf("sent %q", h)
	}
}
