package app

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-music/internal/i18n"
	"mygo-music/internal/local"
	"mygo-music/internal/music"
	"mygo-music/internal/player"
	"mygo-music/internal/plugin"
)

type pageKind int

const (
	pageHome pageKind = iota
	pagePlaylist
	pageSearch
	pageStations
	pageArtist
	pageAlbum
	pageShow
	pageShows
	pageLiked
	pageSavedAlbums
	pageBrowse
	pageSection
)

type route struct {
	page pageKind
	// src is the ID of the source the page is of. Opened without one, a page
	// is of the active source's.
	src string
	// id is the playlist, artist, album, show or browse page shown.
	id string
	// query and kind are what a search page searched for. On a section's page
	// (pageSection), id is the browse page it's of, "" for Home, and query
	// which of its sections.
	query string
	kind  music.SearchKind
}

// app is the state both windows show. Everything in it is read and changed
// on the main thread: by the views, and by goroutines through post.
type app struct {
	win   *mygo.Window
	notch *notch
	tray  *mygo.Tray // the icon in the menu bar or notification area, while it shows
	art   *artCache
	pl    *player.Player
	pal   palette

	// libs are the sources, the built-in one first, and what the app has of
	// each. The active one's is embedded: a.mine is what the active source
	// lists, and what comes back from a goroutine goes to the library it asked.
	libs []*library
	*library
	local   *local.Source // the built-in source, nil in tests that have none
	plugins []*plugin.Plugin
	headers *headerPlugins // the plugins on, for the headers of their pictures
	allLibs []*library     // every plugin, on or off; libs are those on

	wasBehind bool // the window wasn't at the front as last built

	captions  captionState    // the episode playing's captions, and the model they take
	installer pluginInstaller // installing and updating plugins
	// links are the app's links opened before its window was made, macOS
	// handing over the one it was started with first.
	links []string

	route   route
	history []route
	forward []route // the pages gone back from, the last first to go to

	accountOpen bool         // the dropdown of the account row shows
	signIn      signInDialog // signing in to a source
	// signInPages are the windows of plugins' sign-in pages showing, by
	// plugin. Main thread only.
	signInPages map[string]*mygo.Window
	// signInOver is the window the sign-in dialog is in, for a sign-in page
	// to open over: the settings window's, if asked there. Set on the main
	// thread, read on the plugin's.
	signInOver  *atomic.Pointer[mygo.Window]
	naming      nameDialog         // the name of a playlist asked for
	deleting    deleteDialog       // whether to delete a playlist, asked
	toast       string             // a message to show once
	dailySpin   spinState          // the record on the feature card
	dailyBars   barState           // the bars on its play button
	settingText map[string]*string // the settings of sources, as being typed

	trackList ui.ListState
	// Where the page and the sidebar are scrolled, kept while Now Playing
	// covers them, unbuilt.
	pageScroll ui.ScrollState
	navScroll  ui.ScrollState
	scrolled   route // the page trackList and pageScroll are of

	mineCollapsed, savedCollapsed bool

	query     string
	searchBox ui.Handle

	settingsPage      string // the settings' page that shows
	settingsQuery     string // what's searched for in the settings
	settingsSearchBox ui.Handle
	settingsUnderTop  bool // the settings' page has scrolled under its top edge

	suggestions suggestState // what the search field's query may be after

	nowPlaying  bool
	npOpen      bool    // Now Playing as it last showed: open or not
	npT         float32 // how far open Now Playing last showed, 0 to 1
	npShift     float32 // how far down it last showed, pulled
	npPull      float32 // how far the pointer has pulled it down
	npHeld      bool    // whether the pointer presses on it, to pull it
	npStill     bool    // whether it comes and goes in place, sliding or fading, rather than out of the bar's cover
	npFrom      ui.Rect // the player bar's cover, which it grows out of
	npPane      int     // paneLyrics, paneQueue, paneStage, paneComments or paneShow
	npPaneTop   float32 // how far the cover beside the pane starts below its top
	lyricScroll ui.ScrollState
	lyricGlide  lyricGlide
	lyricAt     int
	lyricTrack  string
	queueList   ui.ListState
	queueAt     int // the row of the queue pane last scrolled to, -1 while it is hidden
	comments    commentState
	tabCount    rollState // the count on the comments' tab, as it rolls

	radioStarting string // the radio asked for its first songs, or ""
	stationAt     int    // which of a few stations their switch has chosen

	volume     float64
	volumeOpen bool // the volume popover of the player bar shows
	mutedFrom  float64

	rowBars barState
	// underTop and underBottom say whether the page has content under its
	// bars, which show their scroll edges then.
	underTop, underBottom bool
	barScrub              scrubState
	npScrub               scrubState
	backdropCache         backdropCache // the backdrop of Now Playing, as last painted
	npArt                 *artwork      // the cover whose colors Now Playing shows
	npCoverState          coverState    // the cover of Now Playing, as it is tilted and flicked
	wallState             wallState     // the wall of the queue's covers, as it is dragged
	quiet                 quietState    // the quiet parts of the song playing, for its top comments
	sig                   playerSig
	quitting              bool

	settings    settings
	settingsWin *mygo.Window
	cacheUsed   int64 // the bytes the song cache takes, -1 until measured
	updates     bool  // the update plugin is used (useUpdater)

	restored     bool   // playback picked up where it was left
	savedSession string // the session as last kept
	sessionDue   bool   // a save of the session is due

	// postFn, if set, takes what post would run on the main thread, as in
	// tests, which have no window.
	postFn func(func())
}

// post runs fn on the main thread and redraws both windows. Without a
// window, as in tests, it drops fn.
func (a *app) post(fn func()) {
	if a.postFn != nil {
		a.postFn(fn)
		return
	}
	if a.win == nil {
		return
	}
	a.win.Update(func() {
		fn()
		a.redraw()
	})
}

func (a *app) redraw() {
	if a.win != nil {
		a.win.Invalidate()
	}
	if a.notch != nil && a.notch.win != nil {
		a.notch.win.Invalidate()
	}
	if a.settingsWin != nil {
		a.settingsWin.Invalidate()
	}
}

// shotHook, when set, drives the app to check its design.
var shotHook func(a *app)

func (a *app) showMain() {
	if a.win != nil {
		if a.win.IsMinimized() {
			a.win.Restore()
		}
		a.win.Show()
		a.win.Focus()
		// From the menu bar alone the app may be inactive.
		mygo.App.Focus()
	}
}

// userDataDir is where the app keeps what's the listener's.
func userDataDir(sub string) string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		dir = filepath.Join(os.TempDir(), "mygo-music")
	}
	return filepath.Join(dir, sub)
}

// makeSources makes the built-in source and those of the plugins dropped in,
// none of them started yet, and makes active the one last used, if it's there.
func (a *app) makeSources() {
	a.local = local.New(userDataDir("local"))
	builtIn := newLibrary(a.local)
	a.allLibs = []*library{builtIn}
	plugin.SetOpenURL(mygo.Shell.OpenExternal)
	plugin.SetAppVersion(mygo.App.Version())
	plugin.SetWebSignIn(a.webSignIn)
	a.plugins = plugin.Discover(pluginsDir(), userDataDir("plugin-data"))
	a.headers = &headerPlugins{}
	music.ImageHeaders = a.headers.image
	for _, p := range a.plugins {
		lib := newLibrary(p)
		if !slices.ContainsFunc(a.allLibs, func(l *library) bool { return l.id == lib.id }) {
			a.allLibs = append(a.allLibs, lib)
		}
	}
	a.enabledChanged()
	a.library = a.libs[0]
	if lib := a.libOf(a.settings.Source); lib != nil {
		a.library = lib
	}
	a.route = route{page: pageHome, src: a.library.id}
	a.local.OnChange(func() {
		a.post(func() { a.libraryChanged(builtIn) })
	})
}

// enabledChanged lists the plugins that are on. There's always one: with all of
// them off in the settings, as by hand, it's the built-in one.
func (a *app) enabledChanged() {
	a.libs = nil
	for _, lib := range a.allLibs {
		if !slices.Contains(a.settings.DisabledPlugins, lib.id) {
			a.libs = append(a.libs, lib)
		}
	}
	if len(a.libs) == 0 && len(a.allLibs) > 0 {
		a.libs = a.allLibs[:1]
	}
	if a.headers != nil {
		var on []*plugin.Plugin
		for _, lib := range a.libs {
			if p := pluginOf(lib); p != nil {
				on = append(on, p)
			}
		}
		a.headers.set(on)
	}
}

// setPluginEnabled turns a plugin on or off. Off, it stops, its pages go, and if
// it's the one showing, the first one on takes over; its songs still queued say
// they're gone. The last one on can't go.
func (a *app) setPluginEnabled(lib *library, on bool) {
	disabled := slices.DeleteFunc(slices.Clone(a.settings.DisabledPlugins), func(id string) bool { return id == lib.id })
	if !on {
		if len(a.libs) == 1 && a.libs[0] == lib {
			return
		}
		disabled = append(disabled, lib.id)
	}
	a.settings.DisabledPlugins = disabled
	a.enabledChanged()
	if !on {
		if a.library == lib {
			a.switchSource(a.libs[0].id)
		}
		if closer, ok := lib.src.(interface{ Close() }); ok {
			go closer.Close()
		}
	}
	a.changed()
}

// pluginsDir is where plugins are dropped in.
func pluginsDir() string { return userDataDir("plugins") }

// sourceByID finds the source a track says it's of, for the player.
func (a *app) sourceByID(id string) music.Source {
	if lib := a.libOf(id); lib != nil {
		return lib.src
	}
	return nil
}

// closeSources stops the plugins, which end with the app.
func (a *app) closeSources() {
	for _, p := range a.plugins {
		p.Close()
	}
}

// Run runs the app until it quits.
func Run() error {
	// Opening the app again shows the running one's window: Linux and
	// Windows would start another, with a second tray icon. Asked before
	// anything else, so the second one touches no data on its way out.
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	// The display name may change while the listener's library and settings
	// stay where earlier versions kept them.
	if dir, err := mygo.App.Path(mygo.PathAppData); err == nil {
		mygo.App.SetPath(mygo.PathUserData, filepath.Join(dir, "MyGo Music"))
	}
	if dir, err := os.UserCacheDir(); err == nil {
		mygo.App.SetPath(mygo.PathCache, filepath.Join(dir, "MyGo Music"))
	}
	a := &app{
		signInOver:  new(atomic.Pointer[mygo.Window]),
		settingText: map[string]*string{},
		lyricAt:     -1,
		queueAt:     -1,
		cacheUsed:   -1,
	}
	a.useUpdater(i18n.Resolve(loadSettings().Language, systemLanguages()))
	mygo.App.WhenReady(func() {
		a.settings = loadSettings()
		a.settings.applyAppearance()
		i18n.SetLanguage(i18n.Resolve(a.settings.Language, systemLanguages()))
		a.npPane = a.lyricsPaneChosen()
		a.makeSources()
		// Before the window: on Windows it lays out its content as it is
		// made, and the view asks the player what plays.
		a.art = newArtCache(a.post)
		a.pl = player.New(a.post)
		a.pl.Quiet = func(fn func()) { mygo.RunOnMain(fn) }
		a.pl.Source = a.sourceByID
		a.pl.Failed = a.sourceFailed
		a.pl.Quality = func() string { return a.settings.Quality }
		a.pl.Normalize = func() bool { return a.settings.Normalize }
		a.pl.Cache = newSongCache(a.settings.CacheMB)
		a.pl.Changed = func() {
			a.keepSession()
			a.syncCaptions()
		}
		a.volume = a.settings.Volume
		a.pl.SetVolume(a.volume)
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:         mygo.App.Name(),
			Width:         1180,
			Height:        780,
			MinWidth:      880,
			MinHeight:     580,
			StateKey:      "main",
			TitleBarStyle: mygo.TitleBarHidden,
			// As tall as the edge over a page (Linux, Windows).
			TitleBarHeight: buttonsHeight,
			// Inset from the corner, within the sidebar, as macOS 26's
			// apps with a sidebar place them.
			TrafficLightPosition: &mygo.Point{X: 20, Y: 20},
			Vibrancy:             mygo.VibrancySidebar,
			BackgroundColor:      "light-dark(#ffffff, #141416)",
			Content:              ui.View(a.view),
		})
		a.setMenu()
		a.boot()
		// The notch player is macOS's, over its menu bar: elsewhere it would
		// hang at the top of the screen as a black box.
		if runtime.GOOS == "darwin" {
			a.notch = newNotch(a)
		}
		// From the menu bar alone, the Dock icon shows while the window
		// does.
		a.syncDock()
		a.win.OnShow(a.syncDock)
		a.win.OnHide(a.syncDock)
		// Hidden, it draws nothing for a while: what the heap took for
		// the pages it showed goes back to the system, which Go would
		// keep for minutes.
		a.win.OnHide(func() { go debug.FreeOSMemory() })
		// Shown or switched to a while after, it shows what's new.
		a.win.OnFocus(a.freshenHome)
		a.applyTray()
		for _, link := range a.links {
			a.openLink(link)
		}
		a.links = nil
		if shotHook != nil {
			shotHook(a)
		}
		// Closing the window hides it: music plays on, and the Dock icon
		// or the tray's brings it back. A Linux desktop without a tray
		// has neither, so there closing it quits.
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if a.quitting {
				return
			}
			e.PreventDefault()
			if runtime.GOOS == "darwin" || a.tray != nil {
				a.win.Hide()
			} else {
				mygo.App.Quit()
			}
		})
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		a.quitting = true
		a.settings.Volume = a.volume
		a.settings.save()
		a.saveSession()
	})
	// Windows leaves an icon in the notification area after its app until
	// the pointer passes over it.
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if a.tray != nil {
			a.tray.Destroy()
		}
		a.closeSources()
	})
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnActivate(func(bool) { a.showMain() })
	mygo.App.OnSecondInstance(func([]string, string) { a.showMain() })
	// The installed app's scheme is registered as it's installed; the
	// development app, which isn't, registers its own every time it starts.
	if mygo.IsDev() {
		if err := mygo.App.RegisterURLScheme(linkScheme); err != nil {
			log.Printf("registering %s links: %v", linkScheme, err)
		}
	}
	mygo.App.OnOpenURL(func(link string) {
		if a.win == nil {
			a.links = append(a.links, link)
			return
		}
		a.openLink(link)
	})
	return mygo.App.Run()
}
