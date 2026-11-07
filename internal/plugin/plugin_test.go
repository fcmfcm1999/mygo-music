package plugin

import (
	"bufio"
	"crypto/aes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"mygo-music/internal/i18n"
	"mygo-music/internal/music"
)

// The test binary is the program plugins start: with this set, it echoes lines.
const echoEnv = "MYGO_MUSIC_TEST_ECHO"

func TestMain(m *testing.M) {
	if os.Getenv(echoEnv) == "fail" {
		fmt.Fprintln(os.Stderr, "the reason it stopped")
		os.Exit(1)
	}
	if os.Getenv(echoEnv) != "" {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			fmt.Println("echo:" + sc.Text())
		}
		return
	}
	os.Exit(m.Run())
}

func write(t *testing.T, manifest, source string) (dir, data string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o600)
	name := "index.js"
	if strings.Contains(manifest, `index.ts`) {
		name = "index.ts"
	}
	os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600)
	return dir, t.TempDir()
}

func load(t *testing.T, manifest, source string) *Plugin {
	t.Helper()
	dir, data := write(t, manifest, source)
	p, err := Load(dir, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

const plain = `{"id":"x"}`

// An export answers plainly or through a promise, and the tracks it answers
// are of the plugin, whatever they say, as that's what they'll play through.
func TestAnswersStampedWithThePlugin(t *testing.T) {
	p := load(t, plain, `
export function search({ query }) {
  return { tracks: [{ id: "1", name: query, source: "lying" }] };
}
export async function recent() {
  await new Promise((r) => setTimeout(r, 1));
  return [{ id: "2", name: "later" }];
}`)
	r, err := p.Search("hello", music.SearchTracks, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Tracks) != 1 || r.Tracks[0].Name != "hello" || r.Tracks[0].Source != "x" {
		t.Fatalf("search: %+v", r.Tracks)
	}
	recent, err := p.Recent()
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Name != "later" || recent[0].Source != "x" {
		t.Fatalf("recent: %+v", recent)
	}
}

// TypeScript runs as it is, so plugins needn't be built.
func TestTypeScript(t *testing.T) {
	p := load(t, `{"id":"x","main":"index.ts"}`, `
export function suggest(p: { query: string }): string[] { return [p.query + "!"]; }`)
	got, err := p.Suggest("a")
	if err != nil || len(got) != 1 || got[0] != "a!" {
		t.Fatalf("got %v, %v", got, err)
	}
}

// The errors the app acts on come through by their code, sync or not, keeping
// what the plugin said, and anything else by its message.
func TestErrors(t *testing.T) {
	p := load(t, plain, `
function fail(code, message) { const e = new Error(message); e.code = code; return e; }
export function stream() { throw fail("unavailable", "not in your country"); }
export async function account() { throw fail("signIn"); }
export function album() { throw new Error("boom"); }
export function artist() { throw "just text"; }`)
	if _, err := p.Stream("1", ""); !errors.Is(err, music.ErrUnavailable) || !strings.Contains(err.Error(), "not in your country") {
		t.Errorf("stream: %v", err)
	}
	if _, err := p.Account(); !errors.Is(err, music.ErrSignIn) {
		t.Errorf("account: %v", err)
	}
	if _, err := p.Album("1"); err == nil || err.Error() != "boom" {
		t.Errorf("album: %v", err)
	}
	if _, err := p.Artist("1"); err == nil || err.Error() != "just text" {
		t.Errorf("artist: %v", err)
	}
}

// What a plugin doesn't export is known without running it, and isn't asked.
func TestWhatItDoesIsItsExports(t *testing.T) {
	p := load(t, plain, `globalThis.ran = 1; export function search() { return {}; }`)
	if !p.Can("search") || p.Can("album") {
		t.Fatal("Can doesn't follow the exports")
	}
	if p.vm != nil {
		t.Fatal("Can started the plugin")
	}
	if _, err := p.Album("1"); !errors.Is(err, music.ErrUnsupported) {
		t.Fatalf("album: %v", err)
	}
}

// A plugin that never returns can't hang the app: the call gives up, and the
// plugin runs again for the next one.
func TestStuckPluginRestarts(t *testing.T) {
	p := load(t, plain, `
export function album() { for (;;) {} }
export function suggest() { return ["fine"]; }`)
	err := p.callWithin(50*time.Millisecond, "album", map[string]string{"id": "1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "in time") {
		t.Fatalf("album: %v", err)
	}
	got, err := p.Suggest("a")
	if err != nil || len(got) != 1 {
		t.Fatalf("after restarting: %v, %v", got, err)
	}
}

// One only waiting on something isn't stuck: it keeps what it has in memory.
func TestWaitingPluginIsLeftRunning(t *testing.T) {
	p := load(t, plain, `
let calls = 0;
export function album() { return new Promise(() => {}); }
export function suggest() { calls++; return [String(calls)]; }`)
	p.Suggest("a")
	if err := p.callWithin(50*time.Millisecond, "album", map[string]string{"id": "1"}, nil); err == nil {
		t.Fatal("a call that never settles answered")
	}
	got, err := p.Suggest("a")
	if err != nil || got[0] != "2" {
		t.Fatalf("it restarted: %v, %v", got, err)
	}
}

// Without the permission fetch reaches nothing; with it, only what it names.
func TestNetworkPermission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `["from the server"]`)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	source := fmt.Sprintf(`export async function suggest() { return await (await fetch(%q)).json(); }`, srv.URL)

	none := load(t, plain, source)
	if _, err := none.Suggest("a"); err == nil {
		t.Error("fetch got through with no permission")
	}
	other := load(t, `{"id":"x","permissions":{"network":["example.com"]}}`, source)
	if _, err := other.Suggest("a"); err == nil {
		t.Error("fetch reached a host the plugin didn't name")
	}
	allowed := load(t, fmt.Sprintf(`{"id":"x","permissions":{"network":[%q]}}`, u.Hostname()), source)
	got, err := allowed.Suggest("a")
	if err != nil || len(got) != 1 {
		t.Errorf("fetch to the host named: %v, %v", got, err)
	}
}

const spawnSource = `
export async function suggest({ query }) {
  const proc = host.spawn(host.setting("program"), []);
  proc.write(query + "\n");
  proc.closeStdin();
  const line = await proc.readLine();
  const end = await proc.readLine();
  return [line, String(end), String(await proc.wait())];
}`

// Starting programs takes both the permission and the listener's say-so.
func TestSpawnNeedsPermissionAndGrant(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(echoEnv, "1")

	asked := load(t, plain, spawnSource)
	asked.Grant(true)
	asked.SetSetting("program", self)
	if _, err := asked.Suggest("a"); err == nil || !strings.Contains(err.Error(), "permission") {
		t.Errorf("without the permission: %v", err)
	}

	p := load(t, `{"id":"x","permissions":{"process":true}}`, spawnSource)
	p.SetSetting("program", self)
	if _, err := p.Suggest("a"); err == nil || !strings.Contains(err.Error(), "allowed") {
		t.Errorf("without the grant: %v", err)
	}
	if err := p.Grant(true); err != nil {
		t.Fatal(err)
	}
	got, err := p.Suggest("hi")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "echo:hi,null,0" {
		t.Errorf("got %v", got)
	}
}

// A plugin starts only the programs its process permission names, even once
// the listener allowed it.
func TestSpawnOnlyNamedPrograms(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(echoEnv, "1")
	named, err := json.Marshal(map[string]any{"id": "x", "permissions": map[string]any{"process": []string{self}}})
	if err != nil {
		t.Fatal(err)
	}
	p := load(t, string(named), spawnSource)
	p.Grant(true)
	p.SetSetting("program", self)
	if got, err := p.Suggest("hi"); err != nil || got[0] != "echo:hi" {
		t.Fatalf("the program it names: %v, %v", got, err)
	}
	other := load(t, `{"id":"x","permissions":{"process":["ffmpeg"]}}`, spawnSource)
	other.Grant(true)
	other.SetSetting("program", self)
	if _, err := other.Suggest("hi"); err == nil || !strings.Contains(err.Error(), "isn't one of the programs") {
		t.Errorf("a program it doesn't name: %v", err)
	}
}

// A path matches a path from the plugin's directory and a name a name, so
// neither stands in for the other, nor a way out of the directory for a path
// in it.
func TestProgramsAllows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plugin")
	ps := Programs{"bin/helper-*", "ffmpeg"}
	for _, c := range []struct {
		command string
		ok      bool
	}{
		{filepath.Join(dir, "bin", "helper-darwin-arm64"), true},
		{filepath.Join(dir, "bin", "other"), false},
		{filepath.Join(dir, "bin", "..", "..", "bin", "helper-x"), false},
		{filepath.Join(dir, "bin", "sub", "helper-x"), false},
		{"ffmpeg", true},
		{"ffprobe", false},
		{"helper-x", false},
		{filepath.Join(dir, "ffmpeg"), false},
	} {
		if got := ps.Allows(dir, c.command); got != c.ok {
			t.Errorf("%s: %v", c.command, got)
		}
	}
	if !(Programs{"*"}).Allows(dir, "anything") {
		t.Error("* didn't allow any")
	}
}

// "process": true, from before programs were named, still means any; false,
// none.
func TestProcessTrueMeansAny(t *testing.T) {
	var m Manifest
	if err := json.Unmarshal([]byte(`{"permissions":{"process":true}}`), &m); err != nil || !m.Permissions.Process.Any() {
		t.Errorf("true: %v, %v", m.Permissions.Process, err)
	}
	m = Manifest{}
	if err := json.Unmarshal([]byte(`{"permissions":{"process":false}}`), &m); err != nil || len(m.Permissions.Process) != 0 {
		t.Errorf("false: %v, %v", m.Permissions.Process, err)
	}
}

// Programs end with the plugin, rather than staying around after the app.
func TestCloseEndsPrograms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal 0 doesn't tell here")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(echoEnv, "1")
	p := load(t, `{"id":"x","permissions":{"process":true}}`, `
export function suggest() { return [String(host.spawn(host.setting("program"), []).pid)]; }`)
	p.Grant(true)
	p.SetSetting("program", self)
	got, err := p.Suggest("a")
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	fmt.Sscan(got[0], &pid)
	v := p.vm
	p.Close()
	v.mu.Lock()
	left := len(v.programs)
	v.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d programs left", left)
	}
	proc, _ := os.FindProcess(pid)
	deadline := time.Now().Add(2 * time.Second)
	for proc.Signal(syscall.Signal(0)) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the program is still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// What a plugin stores and the choice to let it run programs outlive the app.
func TestStorageAndGrantAreKept(t *testing.T) {
	dir, data := write(t, `{"id":"x","permissions":{"process":true}}`, `
export function signOut() { host.storage.set("token", { value: "abc" }); }
export function suggest() { const t = host.storage.get("token"); return [t ? t.value : "none"]; }`)
	p, err := Load(dir, data)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := p.Suggest("a"); got[0] != "none" {
		t.Fatalf("before setting: %v", got)
	}
	if err := p.SignOut(); err != nil {
		t.Fatal(err)
	}
	p.Grant(true)
	p.Close()
	again, err := Load(dir, data)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got, err := again.Suggest("a"); err != nil || got[0] != "abc" {
		t.Fatalf("after loading again: %v, %v", got, err)
	}
	if !again.Granted() {
		t.Fatal("the grant wasn't kept")
	}
}

// The module's top level can use its storage and settings as it starts, as a
// plugin reads its saved sign-in there.
func TestTopLevelReadsStorage(t *testing.T) {
	dir, data := write(t, `{"id":"x"}`, `
const saved = host.storage.get("session");
host.storage.set("seen", true);
host.storage.remove("gone");
const server = host.setting("server");
export function home() { return { title: (saved ? saved.user : "none") + " " + (server || "-") }; }
export function signOut() { host.storage.set("session", { user: "egoist" }); }`)
	first, err := Load(dir, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SignOut(); err != nil {
		t.Fatal(err)
	}
	first.SetSetting("server", "nas.local")
	first.Close()
	p, err := Load(dir, data)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	done := make(chan string, 1)
	go func() {
		b, err := p.Home()
		if err != nil {
			done <- err.Error()
			return
		}
		done <- b.Title
	}()
	select {
	case got := <-done:
		if got != "egoist nas.local" {
			t.Fatalf("home: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the plugin got stuck starting")
	}
}

// A browser sign-in comes back with its query, and a plugin can only open pages.
func TestSignInRedirect(t *testing.T) {
	var opened string
	SetOpenURL(func(u string) error { opened = u; return nil })
	defer SetOpenURL(func(string) error { return nil })
	p := load(t, `{"id":"x","permissions":{"network":["example.com"]}}`, `
export async function signIn() {
  host.openURL("https://example.com/auth");
  const q = await host.waitForRedirect({ port: 48917, path: "/cb", timeout: 3000 });
  return { id: q.code, name: "me" };
}
export function suggest() { host.openURL("file:///etc/passwd"); return []; }`)
	type result struct {
		a   *music.Account
		err error
	}
	ch := make(chan result, 1)
	go func() {
		a, err := p.SignIn()
		ch <- result{a, err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err := http.Get("http://127.0.0.1:48917/cb?code=abc")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	r := <-ch
	if r.err != nil || r.a.ID != "abc" {
		t.Fatalf("sign in: %+v, %v", r.a, r.err)
	}
	if opened != "https://example.com/auth" {
		t.Fatalf("opened %q", opened)
	}
	if _, err := p.Suggest("a"); err == nil {
		t.Fatal("opened something that's not a page")
	}
}

// A sign-in on a page answers its cookies, for an address the plugin reaches
// alone: not another site's, nor without a network permission, nor on a page
// that isn't secure.
func TestWebSignIn(t *testing.T) {
	var asked WebSignIn
	SetWebSignIn(func(o WebSignIn) (string, error) { asked = o; return "SID=abc; HSID=def", nil })
	defer SetWebSignIn(func(WebSignIn) (string, error) { return "", errors.New("no page") })
	ask := func(url, cookiesOf string) string {
		return fmt.Sprintf(`{ url: %q, cookiesOf: %q, until: ["SID"] }`, url, cookiesOf)
	}
	p := load(t, `{"id":"x","name":"X","permissions":{"network":["api.example.com","*.example.org"]}}`, `
export async function signIn() {
  const { cookie } = await host.webSignIn(`+ask("https://accounts.example.com/login", "https://api.example.com")+`);
  return { id: cookie, name: "me" };
}
export async function search({ query }) {
  const asks = {
    elsewhere: `+ask("https://accounts.example.com/login", "https://other.example.net")+`,
    below: `+ask("https://accounts.example.com/login", "https://www.example.org")+`,
    plain: `+ask("http://accounts.example.com/login", "https://api.example.com")+`,
  };
  await host.webSignIn(asks[query]);
  return {};
}`)
	acc, err := p.SignIn()
	if err != nil || acc.ID != "SID=abc; HSID=def" {
		t.Fatalf("signed in as %+v, %v", acc, err)
	}
	if asked.Plugin != "x" || asked.Name != "X" || asked.URL != "https://accounts.example.com/login" || asked.Until[0] != "SID" {
		t.Errorf("asked for %+v", asked)
	}
	for query, ok := range map[string]bool{"elsewhere": false, "below": true, "plain": false} {
		if _, err := p.Search(query, music.SearchTracks, 0); (err == nil) != ok {
			t.Errorf("%s: %v", query, err)
		}
	}
	none := load(t, plain, `export async function signIn() { await host.webSignIn(`+ask("https://a.example.com", "https://a.example.com")+`); }`)
	if _, err := none.SignIn(); err == nil {
		t.Error("signed in without the network permission")
	}
}

// One broken plugin is left out, not the end of the others, and a missing name
// or way of playing has a default.
func TestDiscover(t *testing.T) {
	root := t.TempDir()
	for name, files := range map[string][2]string{
		"good":   {`{"id":"good"}`, `export function search() {}`},
		"syntax": {`{"id":"syntax"}`, `export function (`},
		"json":   {`{`, ``},
	} {
		d := filepath.Join(root, name)
		os.MkdirAll(d, 0o700)
		os.WriteFile(filepath.Join(d, "plugin.json"), []byte(files[0]), 0o600)
		os.WriteFile(filepath.Join(d, "index.js"), []byte(files[1]), 0o600)
	}
	ps := Discover(root, t.TempDir())
	if len(ps) != 1 || ps[0].Info().ID != "good" {
		t.Fatalf("got %d plugins", len(ps))
	}
	if i := ps[0].Info(); i.Name != "good" || i.Playback != music.PlaybackStream {
		t.Fatalf("defaults: %+v", i)
	}
}

// A plugin can clear a folder of its own data, as signing out does, but nothing
// outside it, nor all of it, which holds the app's records of it too.
func TestRemoveDataDir(t *testing.T) {
	p := load(t, plain, `
export function suggest({ query }) { host.removeDataDir(query); return []; }`)
	if _, err := p.Suggest("x"); err != nil {
		t.Fatal(err) // starting it makes the data directory
	}
	inside := filepath.Join(p.data, "cache", "file")
	os.MkdirAll(filepath.Dir(inside), 0o700)
	os.WriteFile(inside, []byte("x"), 0o600)
	outside := filepath.Join(filepath.Dir(p.data), "other")
	os.WriteFile(outside, []byte("x"), 0o600)

	for _, bad := range []string{"..", "../other", ".", "", "/etc"} {
		if _, err := p.Suggest(bad); err == nil {
			t.Errorf("removed %q", bad)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("outside its data: %v", err)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Errorf("after the refusals: %v", err)
	}
	if _, err := p.Suggest("cache"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Errorf("still there: %v", err)
	}
}

// A program that stops says why, for the plugin to tell the listener rather than
// leave them waiting.
func TestProgramSaysWhyItStopped(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(echoEnv, "fail")
	p := load(t, `{"id":"x","permissions":{"process":true}}`, `
export async function suggest() {
  const proc = host.spawn(host.setting("program"), []);
  const code = await proc.wait();
  return [String(code), proc.errorOutput()];
}`)
	p.Grant(true)
	p.SetSetting("program", self)
	got, err := p.Suggest("a")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "1" || !strings.Contains(got[1], "the reason it stopped") {
		t.Fatalf("got %q", got)
	}
}

// The hashes and ciphers a plugin gets are the standard ones: known answers.
func TestCrypto(t *testing.T) {
	p := load(t, plain, `
export function suggest() {
  return [host.crypto.md5("abc"), host.crypto.aesEcbEncrypt("0123456789abcdef", "hello")];
}`)
	got, err := p.Suggest("")
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != "900150983cd24fb0d6963f7d28e17f72" {
		t.Errorf("md5: %s", got[0])
	}
	block, _ := aes.NewCipher([]byte("0123456789abcdef"))
	want := []byte("hello\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b\x0b")
	out := make([]byte, 16)
	block.Encrypt(out, want)
	if got[1] != hex.EncodeToString(out) {
		t.Errorf("aes: %s, want %x", got[1], out)
	}
}

// What a plugin wants shown as it signs in reaches the app as it goes.
func TestSignInPrompt(t *testing.T) {
	p := load(t, plain, `
export function signIn() {
  host.signInPrompt({ qr: "https://example.com/code", text: "Scan it" });
  return { id: "1", name: "me" };
}`)
	var got music.SignInPrompt
	p.OnSignInPrompt(func(prompt music.SignInPrompt) { got = prompt })
	if _, err := p.SignIn(); err != nil {
		t.Fatal(err)
	}
	if got.QR != "https://example.com/code" || got.Text != "Scan it" {
		t.Fatalf("got %+v", got)
	}
}

// A plugin that needs a newer app than this doesn't load, so it can't be
// installed either; with no version of its own, as in development, it does.
func TestMinAppVersion(t *testing.T) {
	defer SetAppVersion(appVersion)
	dir, data := write(t, `{"id":"x","name":"X","minAppVersion":"0.1.10"}`, `export function suggest() { return []; }`)
	for _, c := range []struct {
		app   string
		loads bool
	}{
		{"0.1.9", false},
		{"0.1.10", true},
		{"0.2.0", true},
		{"", true},
		{"dev", true},
	} {
		SetAppVersion(c.app)
		_, err := Load(dir, data)
		var needs *NeedsNewerApp
		if c.loads && err != nil || !c.loads && (!errors.As(err, &needs) || needs.Version != "0.1.10") {
			t.Errorf("app %q: %v", c.app, err)
		}
	}
	SetAppVersion("1.0.0")
	bad, data := write(t, `{"id":"x","minAppVersion":"soon"}`, `export function suggest() { return []; }`)
	if _, err := Load(bad, data); err == nil {
		t.Error("loaded with a minAppVersion that isn't a version")
	}
}

// A plugin's name, settings and sidebar, and what it translates with host.t,
// follow the listener's language as it changes, without the plugin starting
// over; what it didn't translate stays in English.
func TestPluginLocales(t *testing.T) {
	defer i18n.SetLanguage(i18n.Language())
	dir, data := write(t, `{"id":"x","name":"Tube","settings":[{"key":"k","label":"Folder","help":"Where it is"}],
"navigation":[{"title":"Charts","page":"charts"},{"title":"New","page":"new"}]}`, `
export function suggest() { return [host.language(), host.t("Charts"), host.t("Nothing")]; }`)
	os.MkdirAll(filepath.Join(dir, "locales"), 0o700)
	os.WriteFile(filepath.Join(dir, "locales", "zh-CN.json"), []byte(`{"Tube":"管","Folder":"文件夹","Charts":"排行榜"}`), 0o600)
	p, err := Load(dir, data)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	i18n.SetLanguage(i18n.English)
	if got, err := p.Suggest(""); err != nil || strings.Join(got, ",") != "en,Charts,Nothing" {
		t.Errorf("in English: %v, %v", got, err)
	}
	i18n.SetLanguage(i18n.Chinese)
	info := p.Info()
	if info.Name != "管" || info.Settings[0].Label != "文件夹" || info.Settings[0].Help != "Where it is" ||
		info.Navigation[0].Title != "排行榜" || info.Navigation[1].Title != "New" {
		t.Errorf("in Chinese: %+v", info)
	}
	if got, err := p.Suggest(""); err != nil || strings.Join(got, ",") != "zh-CN,排行榜,Nothing" {
		t.Errorf("in Chinese: %v, %v", got, err)
	}
	i18n.SetLanguage(i18n.English)
	if p.Info().Name != "Tube" || p.manifest.Navigation[0].Title != "Charts" {
		t.Error("translating changed what plugin.json says")
	}
}

// A locale that isn't JSON is the author's mistake to hear of, not English.
func TestBadLocale(t *testing.T) {
	dir, data := write(t, plain, `export function suggest() { return []; }`)
	os.MkdirAll(filepath.Join(dir, "locales"), 0o700)
	os.WriteFile(filepath.Join(dir, "locales", "zh-CN.json"), []byte(`{"a":`), 0o600)
	if _, err := Load(dir, data); err == nil || !strings.Contains(err.Error(), "zh-CN.json") {
		t.Errorf("got %v", err)
	}
}
