package app

import (
	"errors"
	"log"
	"time"

	"mygo-music/internal/i18n"
	"mygo-music/internal/local"
	"mygo-music/internal/music"
)

// T returns text in the listener's language; see package i18n.
func T(text string) string { return i18n.T(text) }

// TC is T of a text that reads differently by what it's about.
func TC(text, context string) string { return i18n.TC(text, context) }

// Tf is T of a format, formatted.
func Tf(format string, args ...any) string { return i18n.Tf(format, args...) }

// N marks text kept in a table for T to translate when it shows.
func N(text string) string { return i18n.N(text) }

// capabilities are the names a source's Can is asked about as its library is
// made, ahead of the views that hide what it can't do. A plugin knows from
// compiling it, so the first frame shows the source as it stays.
var capabilities = []string{
	"signIn", "signOut", "account",
	"search", "suggest",
	"playlists", "playlist", "album", "artist", "liked", "recent",
	"home", "browse",
	"isLiked", "like", "likeEpisode",
	"createPlaylist", "renamePlaylist", "deletePlaylist", "addToPlaylist", "removeFromPlaylist", "movePlaylistTrack", "savePlaylist",
	"followArtist", "saveAlbum", "savedAlbums",
	"shows", "show", "subscribeShow",
	"stations", "stationNext",
	"comments", "likeComment", "postComment",
	"lyrics", "stream",
}

func probeCaps(src music.Source) map[string]bool {
	caps := make(map[string]bool, len(capabilities))
	for _, name := range capabilities {
		if src.Can(name) {
			caps[name] = true
		}
	}
	return caps
}

// library is what the app has of a source. What a source answers goes here,
// so an answer that comes after the listener switched to another source lands
// where it belongs, and shows when they come back.
type library struct {
	id      string
	src     music.Source
	caps    map[string]bool // what it can do
	started bool            // what it lists was asked for

	account      *music.Account
	accountBusy  bool // it's being asked who's signed in
	accountAsked bool // it has said who's signed in, or failed to
	// needsSignIn and needsSetup say the source answered ErrSignIn or
	// ErrSetup, and asked says the listener was already taken there once.
	needsSignIn, needsSetup bool
	askedSignIn, askedSetup bool

	mine, saved []music.Playlist // the listener's playlists: made, and saved
	mineAt      time.Time        // when they were last asked for
	savingPl    map[string]bool  // the playlists whose saving or its end is on its way

	liked      map[string]bool // the tracks known to be liked, or not
	wantLikes  []string        // the tracks a heart showed for, to ask about
	likedList  []music.Track   // the liked songs' page
	likedPaged paged
	likedAt    time.Time // when the liked songs' first page was asked for
	likedFresh bool      // it's being asked for anew

	recent      []music.Track
	home        *music.Browse // the source's own Home
	homeErr     string
	homeLoading bool
	homeAt      time.Time // when what the home page shows was asked for

	playlists map[string]*music.Playlist
	loadingPl map[string]bool
	plAt      map[string]time.Time // when each playlist loaded was asked for
	freshPl   map[string]bool      // the loaded playlists being asked for anew
	plErr     map[string]string
	artists   map[string]*artistData
	albums    map[string]*albumData
	shows     map[string]*showData
	browses   map[string]*browseData
	searches  map[route]*searchData
	searched  route // the search last shown, which the sidebar goes back to

	subscribed     []music.Show
	subscribedOnce bool // what the listener subscribes to was asked
	subscribedBusy bool
	subscribedErr  string

	stations     []music.Station
	stationsBusy bool
	stationsOnce bool
	stationsErr  string

	savedAlbums      []music.Album
	savedAlbumsPaged paged
}

func newLibrary(src music.Source) *library {
	lib := &library{id: src.Info().ID, src: src, caps: probeCaps(src)}
	lib.forget()
	return lib
}

// forget drops what was loaded of the source, whose answers may have changed.
func (l *library) forget() {
	l.playlists = map[string]*music.Playlist{}
	l.loadingPl = map[string]bool{}
	l.plAt = map[string]time.Time{}
	l.freshPl = map[string]bool{}
	l.plErr = map[string]string{}
	l.artists = map[string]*artistData{}
	l.albums = map[string]*albumData{}
	l.shows = map[string]*showData{}
	l.browses = map[string]*browseData{}
	l.searches = map[route]*searchData{}
	l.liked = nil
	l.likedList, l.likedPaged, l.likedAt = nil, paged{}, time.Time{}
	l.savedAlbums, l.savedAlbumsPaged = nil, paged{}
	l.subscribed, l.subscribedOnce = nil, false
	l.stations, l.stationsOnce = nil, false
}

// can reports whether the source does what's named.
func (l *library) can(what string) bool { return l != nil && l.caps[what] }

// accountUnknown reports whether the source is yet to say who's signed in.
func (l *library) accountUnknown() bool { return l.can("account") && !l.accountAsked }

// signedOut reports whether the source wants a listener and has none, which it
// isn't known to until it said so.
func (l *library) signedOut() bool {
	return l.can("signIn") && l.account == nil && !l.accountUnknown()
}

// name is what the listener knows the source as.
func (l *library) name() string {
	if l.id == local.ID {
		return T("Local Files")
	}
	return l.src.Info().Name
}

// libOf returns the library of the source id, or nil.
func (a *app) libOf(id string) *library {
	for _, lib := range a.libs {
		if lib.id == id {
			return lib
		}
	}
	return nil
}

// trackLib returns the library of the source of tr, or the active one's for a
// track that doesn't say.
func (a *app) trackLib(tr *music.Track) *library {
	if lib := a.libOf(tr.Source); lib != nil {
		return lib
	}
	return a.library
}

// boot loads the active source's pages, and picks up playback where it was left.
// The others are asked once they're needed: a plugin runs from when it's first
// asked.
func (a *app) boot() {
	a.keepCheckingPlugins()
	a.startLibrary(a.library)
	a.restoreSession()
	if a.local != nil {
		a.local.Rescan()
	}
}

// startLibrary loads what the sidebar and Home show of a source, once.
func (a *app) startLibrary(lib *library) {
	if lib.started {
		return
	}
	lib.started = true
	a.loadAccount(lib)
	a.loadMine(lib)
	a.loadHome(lib)
}

// activate makes lib the active source: its sidebar and pages show.
func (a *app) activate(lib *library) {
	if lib == a.library {
		return
	}
	a.library = lib
	a.settings.Source = lib.id
	a.settings.save()
	a.dismissSuggestions()
	a.accountOpen = false
	a.startLibrary(lib)
}

// switchSource goes to a source's Home, as the sidebar's switcher does.
func (a *app) switchSource(id string) {
	if lib := a.libOf(id); lib != nil && lib != a.library {
		a.open(route{page: pageHome, src: id})
	}
}

// errNoAnswer stands for a source that answered nothing where it had to answer
// something, which shouldn't take the app down with it.
var errNoAnswer = errors.New("the source gave no answer")

// answered says errNoAnswer of a nil answer to a question without an error.
func answered[T any](v *T, err error) (*T, error) {
	if v == nil && err == nil {
		return nil, errNoAnswer
	}
	return v, err
}

// problem makes out what went wrong with what a source was asked, for a page's
// error state or a toast: msg, with what the source said. ErrSignIn and
// ErrSetup take the listener where they can fix it, the first time only if
// nobody asked for this in particular.
func (a *app) problem(lib *library, err error, msg string) string {
	log.Printf("source %s: %s: %v", lib.id, msg, err)
	switch {
	case errors.Is(err, music.ErrSignIn):
		lib.needsSignIn = true
		if !lib.askedSignIn {
			lib.askedSignIn = true
			a.promptSignIn(lib)
		}
		return Tf("Sign in to %s to see this", lib.name())
	case errors.Is(err, music.ErrSetup):
		lib.needsSetup = true
		if !lib.askedSetup {
			lib.askedSetup = true
			a.openSourceSettings()
		}
		return Tf("%s needs setting up first", lib.name())
	case errors.Is(err, music.ErrUnsupported):
		return msg
	}
	return msg + ": " + err.Error()
}

// actionFailed says that something the listener did didn't work, taking them
// where they can fix it if that's the reason.
func (a *app) actionFailed(lib *library, err error, msg string) {
	log.Printf("source %s: %s: %v", lib.id, msg, err)
	switch {
	case errors.Is(err, music.ErrSignIn):
		a.promptSignIn(lib)
	case errors.Is(err, music.ErrSetup):
		a.openSourceSettings()
	default:
		a.toast = msg
	}
}

// sourceFailed hears from the player that a source wants something of the
// listener before it plays.
func (a *app) sourceFailed(source string, err error) {
	if lib := a.libOf(source); lib != nil {
		a.problem(lib, err, T("Couldn't play this song"))
	}
}

// libraryChanged shows what changed in a source that says so, as files found
// on disk: other pages load anew as they open. The page showing stays as it is,
// rather than going blank at each batch of files, but for a page the source
// makes up, or Home, which take the new one in place as it comes.
func (a *app) libraryChanged(lib *library) {
	was := *lib
	lib.forget()
	lib.home, lib.browses, lib.liked = was.home, was.browses, was.liked
	if r := a.route; r.src == lib.id {
		switch r.page {
		case pagePlaylist:
			if pl := was.playlists[r.id]; pl != nil {
				lib.playlists[r.id] = pl
			}
		case pageAlbum:
			if d := was.albums[r.id]; d != nil && d.album != nil {
				lib.albums[r.id] = d
			}
		case pageArtist:
			if d := was.artists[r.id]; d != nil && d.page != nil {
				lib.artists[r.id] = d
			}
		case pageSearch:
			if d := was.searches[r]; d != nil {
				lib.searches[r] = d
			}
		case pageLiked:
			lib.likedList, lib.likedPaged = was.likedList, was.likedPaged
		}
	}
	for page := range lib.browses {
		if a.route.page != pageBrowse || a.route.id != page || a.route.src != lib.id {
			delete(lib.browses, page)
		}
	}
	a.loadMine(lib)
	a.loadHome(lib)
	if a.route.src == lib.id {
		a.reload(a.route)
	}
}

// reload loads what the page of r shows anew.
func (a *app) reload(r route) {
	if r.page == pageBrowse {
		a.loadBrowse(a.libOf(r.src), r.id, true)
		return
	}
	a.load(r)
}
