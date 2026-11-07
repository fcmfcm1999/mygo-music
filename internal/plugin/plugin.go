// Package plugin runs sources as JavaScript or TypeScript, each in a runtime
// of its own inside the app. A plugin is a directory with a plugin.json:
//
//	{
//	  "id": "example", "name": "Example", "main": "index.js", "playback": "stream",
//	  "minAppVersion": "0.1.8",
//	  "permissions": {"network": ["api.example.com"], "process": ["bin/helper-*", "ffmpeg"]}
//	}
//
// and one module that exports a function for each thing it does, by the names
// of music.Source's, such as search or playlist. Each gets one object of
// parameters, and answers a value or a promise of one. What it doesn't export,
// it doesn't do. A plugin that throws an error with a code of signIn, setup,
// unavailable or unsupported says that of what was asked.
//
// A plugin reaches nothing but the JavaScript standard library, timers and the
// host global, unless it asks: fetch reaches the hosts its network permission
// names, and host.spawn, for those with the process permission that the
// listener granted, starts the programs it names, which can do anything the
// listener can.
package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/eventloop"
	"github.com/MoeForge/moejs/webapi"

	"mygo-music/internal/i18n"
	"mygo-music/internal/music"
)

const (
	callTimeout = 30 * time.Second
	// Signing in waits for the listener, in their browser.
	signInTimeout = 5 * time.Minute
	// A runtime that doesn't take on a task in this long is stuck in the
	// plugin's code, rather than waiting on something.
	stuckAfter = time.Second
	// memoryLimit is what a plugin may keep. Passing it restarts the plugin.
	memoryLimit = 256 << 20
)

// Permissions are what a plugin asks for beyond its own sandbox.
type Permissions struct {
	// Network lists the hosts fetch can reach, *.example.com for those below
	// it, and * for any.
	Network []string `json:"network,omitempty"`
	// Process lists the programs it may start, once the listener granted it.
	Process Programs `json:"process,omitempty"`
}

// Programs are what a plugin may start: each a path from its directory or
// absolute, such as bin/helper-*, or a name looked up on PATH, such as ffmpeg,
// and * for any. A pattern matches as filepath.Match does, so * doesn't cross
// a directory. true is short for any, from before programs were named.
type Programs []string

func (ps *Programs) UnmarshalJSON(b []byte) error {
	var all bool
	if err := json.Unmarshal(b, &all); err == nil {
		*ps = nil
		if all {
			*ps = Programs{"*"}
		}
		return nil
	}
	var names []string
	if err := json.Unmarshal(b, &names); err != nil {
		return errors.New("process is a list of programs, or true for any")
	}
	*ps = names
	return nil
}

// Any reports whether it may start any program.
func (ps Programs) Any() bool { return slices.Contains(ps, "*") }

// Allows reports whether command, as host.spawn has it after resolving a path
// from dir, is one of the programs. A path only matches a path, and a name a
// name, so a program that's somewhere else, but called the same, isn't one.
func (ps Programs) Allows(dir, command string) bool {
	bare := !strings.ContainsAny(command, `/\`)
	if !bare {
		command = filepath.Clean(command)
	}
	for _, pattern := range ps {
		if pattern == "*" {
			return true
		}
		pattern = filepath.FromSlash(pattern)
		if strings.ContainsAny(pattern, `/\`) != !bare {
			continue
		}
		if !bare && !filepath.IsAbs(pattern) {
			pattern = filepath.Join(dir, pattern)
		}
		name := command
		if runtime.GOOS == "windows" {
			// Names and paths there are any case, and a program needn't say .exe.
			pattern, name = strings.ToLower(pattern), strings.ToLower(name)
			if bare && filepath.Ext(pattern) == "" {
				name = strings.TrimSuffix(name, ".exe")
			}
		}
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}
	return false
}

// Manifest is a plugin's plugin.json.
type Manifest struct {
	music.Info
	Main string `json:"main,omitempty"`
	// MinAppVersion is the oldest version of the app the plugin works with,
	// such as 0.1.8. An older one won't install it, or load it.
	MinAppVersion  string           `json:"minAppVersion,omitempty"`
	Permissions    Permissions      `json:"permissions"`
	RequestHeaders []RequestHeaders `json:"requestHeaders,omitempty"`
}

// appVersion is the app's version, which the app sets. With none, as when
// it's built for development, any plugin loads.
var appVersion string

// SetAppVersion says which version of the app this is.
func SetAppVersion(v string) { appVersion = v }

// NeedsNewerApp is why a plugin doesn't load: it works with a newer version of
// the app than this.
type NeedsNewerApp struct {
	Plugin  string // its name
	Version string // the oldest version it works with
}

func (e *NeedsNewerApp) Error() string {
	return fmt.Sprintf("%s needs MyGo Music %s or later", e.Plugin, e.Version)
}

// Plugin is a music.Source in JavaScript. It starts on first use, and again on
// the next use after it got stuck.
type Plugin struct {
	manifest Manifest
	dir      string // where plugin.json is
	data     string // the plugin's own data, and ours about it
	mod      *moejs.Module
	exports  map[string]bool
	locales  map[string]map[string]string // by language, the English to what it is in it

	// starting is held while the plugin starts, and by Close, so one start
	// runs at a time and none outlives a Close. It isn't mu: the module's top
	// level runs as it starts, and the host functions it calls take mu.
	starting sync.Mutex

	mu       sync.Mutex // guards what follows
	settings map[string]string
	storage  map[string]json.RawMessage
	granted  bool // the listener let it start programs
	vm       *vm
	prompt   func(music.SignInPrompt)
}

// OnSignInPrompt says what to do with what the plugin wants shown as it signs in.
func (p *Plugin) OnSignInPrompt(show func(music.SignInPrompt)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prompt = show
}

// Load reads and compiles the plugin in dir, keeping its data in dataDir,
// without running it.
func Load(dir, dataDir string) (*Plugin, error) {
	b, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if m.ID == "" {
		return nil, fmt.Errorf("%s: plugin.json needs an id", dir)
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	if m.Playback == "" {
		m.Playback = music.PlaybackStream
	}
	if m.Main == "" {
		m.Main = "index.js"
	}
	if m.MinAppVersion != "" {
		if _, ok := versionNumbers(m.MinAppVersion); !ok {
			return nil, fmt.Errorf("%s: minAppVersion isn't a version such as 0.1.8: %q", dir, m.MinAppVersion)
		}
		if _, ok := versionNumbers(appVersion); ok && IsNewer(m.MinAppVersion, appVersion) {
			return nil, &NeedsNewerApp{Plugin: m.Name, Version: m.MinAppVersion}
		}
	}
	if err := checkRequestHeaders(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	source, err := os.ReadFile(filepath.Join(dir, m.Main))
	if err != nil {
		return nil, err
	}
	compile := moejs.Compile
	if strings.HasSuffix(m.Main, ".ts") {
		compile = moejs.CompileTS
	}
	mod, err := compile(m.Main, string(source))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	p := &Plugin{
		manifest: m,
		dir:      dir,
		data:     filepath.Join(dataDir, m.ID),
		mod:      mod,
		exports:  map[string]bool{},
		settings: map[string]string{},
		storage:  map[string]json.RawMessage{},
	}
	for _, name := range mod.Exports() {
		p.exports[name] = true
	}
	if p.locales, err = readLocales(dir); err != nil {
		return nil, err
	}
	readJSON(filepath.Join(p.data, "settings.json"), &p.settings)
	readJSON(filepath.Join(p.data, "storage.json"), &p.storage)
	readJSON(filepath.Join(p.data, "granted.json"), &p.granted)
	return p, nil
}

// readLocales reads the plugin's translations: for each language the app
// speaks but English, locales/<language>.json, such as locales/zh-CN.json, of
// the English text to what it is in that language, as the app's own are.
func readLocales(dir string) (map[string]map[string]string, error) {
	locales := map[string]map[string]string{}
	for _, l := range i18n.Languages {
		if l.Code == i18n.English {
			continue
		}
		path := filepath.Join(dir, "locales", l.Code+".json")
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var texts map[string]string
		if err := json.Unmarshal(b, &texts); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		locales[l.Code] = texts
	}
	return locales, nil
}

// t is the text in the listener's language, if the plugin translated it.
func (p *Plugin) t(text string) string {
	if tr := p.locales[i18n.Language()][text]; tr != "" {
		return tr
	}
	return text
}

func readJSON(path string, into any) {
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, into)
	}
}

// writeJSON replaces the file whole, so that a crash leaves the last one.
func (p *Plugin) writeJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.data, 0o700); err != nil {
		return err
	}
	path := filepath.Join(p.data, name)
	if err := os.WriteFile(path+".part", b, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".part", path)
}

// Discover loads every plugin in the directories of dir. One that doesn't load
// is logged and left out: it shouldn't take the others with it.
func Discover(dir, dataDir string) []*Plugin {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var plugins []*Plugin
	for _, e := range entries {
		// A dot is what's on its way in, or anything else that's no plugin.
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, err := Load(filepath.Join(dir, e.Name()), dataDir)
		if err != nil {
			log.Printf("plugin: %v", err)
			continue
		}
		plugins = append(plugins, p)
	}
	return plugins
}

// Permissions are what plugin.json asks for.
func (p *Plugin) Permissions() Permissions { return p.manifest.Permissions }

// Granted reports whether the listener let the plugin start its programs.
func (p *Plugin) Granted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.granted
}

// Grant lets the plugin start programs, or stops it from doing so, keeping the
// choice. The plugin starts over for it: what it can reach depends on it, and
// taking it back ends the programs it started.
func (p *Plugin) Grant(on bool) error {
	p.mu.Lock()
	p.granted = on
	p.mu.Unlock()
	p.Close()
	return p.writeJSON("granted.json", on)
}

// Close stops the plugin, and the programs it started.
func (p *Plugin) Close() {
	p.starting.Lock()
	defer p.starting.Unlock()
	p.mu.Lock()
	v := p.vm
	p.vm = nil
	p.mu.Unlock()
	if v != nil {
		v.close()
	}
}

// vm is a running plugin: only its loop's goroutine touches the runtime.
type vm struct {
	rt     *moejs.Runtime
	loop   *eventloop.Loop
	api    *webapi.API
	helper moejs.Value // calls an export and reports how it went

	inFlight int // on the loop: calls not answered yet

	mu       sync.Mutex // guards what follows
	programs map[*program]bool
	closed   bool
}

func (v *vm) close() {
	v.mu.Lock()
	v.closed = true
	v.mu.Unlock()
	v.killPrograms()
	v.api.Close()
	v.loop.Terminate()
}

// helperBody calls fn, whether it's sync or async, and says how it went
// through ok or bad. An error's code is read here, as Go only sees its message
// through the exception of what throws.
const helperBody = `
const fail = (e) => {
  const isObject = e !== null && typeof e === "object";
  bad(String((isObject && e.code) || ""), isObject ? String(e.message || "") : String(e));
};
try {
  Promise.resolve(fn(arg)).then((r) => ok(r === undefined ? null : r), fail);
} catch (e) {
  fail(e);
}`

var helperScript = func() *moejs.Script {
	s, err := moejs.CompileFunction("host-call", "fn, arg, ok, bad", helperBody, moejs.FunctionOptions{})
	if err != nil {
		panic(err)
	}
	return s
}()

// running is the plugin's runtime, started if it isn't.
func (p *Plugin) running() (*vm, error) {
	if v := p.current(); v != nil {
		return v, nil
	}
	p.starting.Lock()
	defer p.starting.Unlock()
	if v := p.current(); v != nil {
		return v, nil
	}
	// Not under mu: the top level may read its storage or settings.
	v, err := p.start()
	if err != nil {
		return nil, fmt.Errorf("starting plugin %s: %w", p.manifest.ID, err)
	}
	p.mu.Lock()
	p.vm = v
	p.mu.Unlock()
	return v, nil
}

func (p *Plugin) current() *vm {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.vm
}

// start runs the plugin, its module's top level too. The caller holds
// p.starting, and not p.mu, which the host functions take.
func (p *Plugin) start() (*vm, error) {
	// host.dataDir is there for the plugin to use, and for programs it starts.
	if err := os.MkdirAll(p.data, 0o700); err != nil {
		return nil, err
	}
	rt := moejs.NewRuntime(moejs.Options{MemoryLimit: memoryLimit})
	loop, err := eventloop.New(rt, eventloop.Options{
		OnError: func(err error) { log.Printf("plugin %s: %v", p.manifest.ID, err) },
	})
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	web := p.webOptions()
	p.mu.Unlock()
	api, err := webapi.Install(loop, web)
	if err != nil {
		return nil, err
	}
	v := &vm{rt: rt, loop: loop, api: api, programs: map[*program]bool{}}
	if err := p.installHost(v); err != nil {
		api.Close()
		return nil, err
	}
	if v.helper, err = rt.RunScript(helperScript); err != nil {
		api.Close()
		return nil, err
	}
	// A top level that awaits something finishes once the loop runs.
	if err := rt.Load(p.mod); err != nil && !errors.Is(err, moejs.ErrModulePending) {
		api.Close()
		return nil, err
	}
	if err := loop.Start(); err != nil {
		api.Close()
		return nil, err
	}
	return v, nil
}

// webOptions are what fetch may do: with no network permission it reaches
// nothing, and a plugin with programs reaches those on this machine, as that's
// how it talks to them. The caller holds p.mu.
func (p *Plugin) webOptions() webapi.Options {
	hosts := append([]string(nil), p.manifest.Permissions.Network...)
	if len(p.manifest.Permissions.Process) > 0 && p.granted {
		hosts = append(hosts, "127.0.0.1", "localhost")
	}
	if len(hosts) == 0 {
		return webapi.Options{}
	}
	opts := webapi.Options{Client: &http.Client{Timeout: 10 * time.Minute}}
	for _, h := range hosts {
		if h == "*" {
			return opts
		}
	}
	opts.Allow = webapi.AllowHosts(hosts...)
	return opts
}

type answer struct {
	json []byte
	err  error
}

// asError is the error a plugin threw, those the app acts on by what they are.
func asError(code, message string) error {
	var known error
	switch code {
	case "signIn":
		known = music.ErrSignIn
	case "setup":
		known = music.ErrSetup
	case "unavailable":
		known = music.ErrUnavailable
	case "unsupported":
		known = music.ErrUnsupported
	default:
		if message == "" {
			return errors.New("the plugin failed")
		}
		return errors.New(message)
	}
	if message == "" {
		return known
	}
	return fmt.Errorf("%w: %s", known, message)
}

// ask calls an export on the loop; the answer comes through got, once.
func (v *vm) ask(method string, params []byte, got func(answer)) {
	rt := v.rt
	fn, ok := rt.Export(method)
	if !ok {
		got(answer{err: music.ErrUnsupported})
		return
	}
	arg := moejs.Undefined()
	if params != nil {
		var err error
		if arg, err = rt.ParseJSON(params); err != nil {
			got(answer{err: err})
			return
		}
	}
	// The call's data goes once nothing is using it. It can't go any sooner, as
	// another call may be waiting on something with its own.
	done := func(a answer) {
		got(a)
		v.inFlight--
		v.loop.RunOnLoop(func(rt *moejs.Runtime) {
			if v.inFlight == 0 {
				rt.ReleaseCallData()
			}
		})
	}
	v.inFlight++
	okFn := rt.Function("ok", 1, func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		out, err := rt.AppendJSON(nil, moejs.Arg(args, 0))
		done(answer{json: out, err: err})
		return moejs.Undefined(), nil
	})
	badFn := rt.Function("bad", 2, func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		done(answer{err: asError(moejs.Arg(args, 0).String(), moejs.Arg(args, 1).String())})
		return moejs.Undefined(), nil
	})
	if _, err := rt.CallFunction(v.helper, moejs.Undefined(), fn, arg, okFn, badFn); err != nil {
		// Only an interrupt gets here: the helper catches what the plugin throws.
		done(answer{err: err})
	}
}

// call asks the plugin and decodes its answer into out, unless out is nil.
func (p *Plugin) call(method string, params, out any) error {
	return p.callWithin(callTimeout, method, params, out)
}

func (p *Plugin) callWithin(timeout time.Duration, method string, params, out any) error {
	if !p.exports[method] {
		return music.ErrUnsupported
	}
	v, err := p.running()
	if err != nil {
		return err
	}
	var encoded []byte
	if params != nil {
		if encoded, err = json.Marshal(params); err != nil {
			return err
		}
	}
	ch := make(chan answer, 1)
	if !v.loop.RunOnLoop(func(*moejs.Runtime) {
		v.ask(method, encoded, func(a answer) { ch <- a })
	}) {
		p.drop(v)
		return fmt.Errorf("the plugin stopped; try again")
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case a := <-ch:
		if a.err != nil {
			return a.err
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(a.json, out)
	case <-timer.C:
		p.dropIfStuck(v)
		return fmt.Errorf("the plugin didn't answer %s in time", method)
	}
}

// dropIfStuck ends a runtime that takes on no more tasks, which code that never
// returns, or passing the memory limit, leaves it as. A plugin only waiting on
// something, and slow about it, goes on.
func (p *Plugin) dropIfStuck(v *vm) {
	alive := make(chan struct{})
	v.loop.RunOnLoop(func(*moejs.Runtime) { close(alive) })
	select {
	case <-alive:
	case <-time.After(stuckAfter):
		log.Printf("plugin %s: stuck; restarting it", p.manifest.ID)
		p.drop(v)
	}
}

func (p *Plugin) drop(v *vm) {
	p.mu.Lock()
	if p.vm == v {
		p.vm = nil
	}
	p.mu.Unlock()
	// Terminate waits for the loop, which the plugin may be keeping busy; it
	// interrupts it first, so this ends, but needn't hold up the caller.
	go v.close()
}

// Info is what plugin.json says of the plugin, in the listener's language as
// far as the plugin translated it: its name, settings and sidebar.
func (p *Plugin) Info() music.Info {
	info := p.manifest.Info
	if len(p.locales[i18n.Language()]) == 0 {
		return info
	}
	info.Name = p.t(info.Name)
	info.Settings = slices.Clone(info.Settings)
	for i, st := range info.Settings {
		info.Settings[i].Label, info.Settings[i].Help = p.t(st.Label), p.t(st.Help)
	}
	info.Navigation = slices.Clone(info.Navigation)
	for i, n := range info.Navigation {
		info.Navigation[i].Title = p.t(n.Title)
	}
	return info
}

// Can reports whether the plugin exports the function. It's known from
// compiling it, without running anything, so the views can ask at any time.
func (p *Plugin) Can(what string) bool { return p.exports[what] }

func (p *Plugin) Setting(key string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings[key]
}

// SetSetting keeps the setting. A running plugin reads it through
// host.setting, and hears of it through setSetting if it exports that.
func (p *Plugin) SetSetting(key, value string) error {
	p.mu.Lock()
	p.settings[key] = value
	settings := make(map[string]string, len(p.settings))
	for k, s := range p.settings {
		settings[k] = s
	}
	running := p.vm != nil
	p.mu.Unlock()
	if err := p.writeJSON("settings.json", settings); err != nil {
		return err
	}
	if running && p.exports["setSetting"] {
		return p.call("setSetting", map[string]string{"key": key, "value": value}, nil)
	}
	return nil
}

var _ music.Source = (*Plugin)(nil)
var _ music.Recorder = (*Plugin)(nil)
var _ music.Prompter = (*Plugin)(nil)
