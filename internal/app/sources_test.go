package app

import (
	"errors"
	"mygo-music/internal/plugin"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/i18n"
	"mygo-music/internal/music"
)

// addOther adds a second source, called Other, whose pages and sidebar a test
// tells from those of fake.
func addOther(a *app) (*fakeSource, *library) {
	other := newFakeSource("other")
	other.info.Name = "Other"
	return other, addFake(a, other)
}

// chooseSource chooses a source with the sidebar's switcher.
func chooseSource(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if err := tt.Click("Plugin"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(name); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

// The sidebar's switcher only shows once there's another source to choose.
func TestSwitcherNeedsAnotherSource(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if _, ok := tt.Find("Plugin"); ok {
		t.Error("a switcher with nothing to switch to")
	}
	addOther(a)
	tt.Frame()
	if _, ok := tt.Find("Plugin"); !ok {
		t.Error("no switcher with two sources")
	}
}

// The switcher's dropdown is driven by keys too: Escape closes it on the
// source it was on, and Down then Enter switches to the next one.
func TestSwitchSourceWithKeys(t *testing.T) {
	a := newTestApp()
	_, lib := addOther(a)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	if err := tt.Click("Plugin"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("Other"); !ok {
		t.Fatal("the dropdown didn't open")
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if _, ok := tt.Find("Other"); ok || a.library == lib {
		t.Fatalf("Escape: open %v, active %q", ok, a.library.id)
	}

	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.library != lib || a.settings.Source != "other" {
		t.Fatalf("active %q, remembered %q", a.library.id, a.settings.Source)
	}
	if _, ok := tt.Find("fake"); ok {
		t.Error("the dropdown stayed open")
	}
}

// Choosing another source makes it active and shows its Home and its playlists,
// which it's asked for the first time then, and remembers it; back returns to
// the page of the source before, which is active again.
func TestSwitchSource(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	without(a, "isLiked")
	other, lib := addOther(a)
	lib.started = false
	other.playlists = func() ([]music.Playlist, error) {
		return []music.Playlist{{ID: "1", Name: "Other's list", Mine: true}}, nil
	}
	other.home = func() (*music.Browse, error) {
		return &music.Browse{Sections: []music.Section{{Title: "Other's shelf", Albums: []music.Album{{ID: "8", Name: "Other's album"}}}}}, nil
	}
	a.mine = []music.Playlist{{ID: "10", Name: "My list", Mine: true}}
	a.playlists["10"] = &music.Playlist{ID: "10", Name: "My list", Mine: true}
	list := route{page: pagePlaylist, src: "fake", id: "10"}
	a.open(list)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	chooseSource(t, tt, "Other")
	if a.library != lib || a.route != (route{page: pageHome, src: "other"}) || a.settings.Source != "other" {
		t.Fatalf("active %q on %+v, remembered %q", a.library.id, a.route, a.settings.Source)
	}
	until(t, ch, func() bool { return len(lib.mine) > 0 && lib.home != nil })
	tt.Frame()
	if _, ok := tt.Find("Other's list"); !ok {
		t.Errorf("no playlist of its in the sidebar: %q", tt.Texts())
	}
	if _, ok := tt.Find("My list"); ok {
		t.Error("the other source's playlist stayed in the sidebar")
	}
	if _, ok := tt.Find("Other's album"); !ok {
		t.Errorf("no Home of its: %q", tt.Texts())
	}

	tt.Key(0, ui.KeyBack)
	tt.Frame()
	if a.library != a.libs[0] || a.route != list || a.settings.Source != "fake" {
		t.Fatalf("back: active %q on %+v, remembered %q", a.library.id, a.route, a.settings.Source)
	}
	if _, ok := tt.Find("My list"); !ok {
		t.Error("its playlist didn't come back to the sidebar")
	}
}

// Going to the source that's active goes nowhere, and a page of a source gone
// since shows Home of the one that's active.
func TestSwitchToNoOtherSource(t *testing.T) {
	a := newTestApp()
	a.switchSource("fake")
	a.switchSource("gone")
	if len(a.history) != 0 || a.route.page != pageHome {
		t.Fatalf("on %+v, history %v", a.route, a.history)
	}
	a.show(route{page: pageArtist, src: "gone", id: "1"})
	if a.route != (route{page: pageHome, src: "fake"}) || a.library != a.libs[0] {
		t.Errorf("on %+v, active %q", a.route, a.library.id)
	}
}

// What a source answers after the listener switched to another lands in its own
// library, and shows when they come back, without asking again.
func TestAnswerLandsInItsOwnLibrary(t *testing.T) {
	asked := make(chan string, 4)
	a := newTestApp()
	ch := posts(a)
	without(a, "isLiked")
	first := a.library
	fake(a).playlist = func(id string) (*music.Playlist, error) {
		asked <- id
		return &music.Playlist{ID: id, Name: "Late list", Tracks: []music.Track{song("1", "Late song")}}, nil
	}
	_, lib := addOther(a)
	a.open(route{page: pagePlaylist, id: "7"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	wait(t, asked, "the playlist")

	chooseSource(t, tt, "Other")
	runPost(t, ch)
	tt.Frame()
	if first.playlists["7"] == nil || lib.playlists["7"] != nil {
		t.Fatalf("landed in the first %v, in the other %v", first.playlists["7"] != nil, lib.playlists["7"] != nil)
	}
	if a.library != lib {
		t.Errorf("the answer switched the source to %q", a.library.id)
	}
	if _, ok := tt.Find("Late song"); ok {
		t.Error("the answer showed on the other source's page")
	}

	tt.Key(0, ui.KeyBack)
	tt.Frame()
	if _, ok := tt.Find("Late song"); !ok || len(asked) > 0 {
		t.Errorf("shows %q, asked %d more", tt.Texts(), len(asked))
	}
}

// A song playing from a source that isn't active likes with its own source, and
// its heart answers from its own library.
func TestHeartOfASongOfAnotherSource(t *testing.T) {
	liked := make(chan string, 1)
	a := newTestApp()
	other, lib := addOther(a)
	lib.account = &music.Account{ID: "1"}
	other.like = func(id string, on bool) error { liked <- id; return nil }
	fake(a).like = func(string, bool) error { t.Error("liked with the active source"); return nil }
	tr := song("7", "宝船")
	tr.Source = "other"
	a.pl.Cue([]music.Track{tr})
	a.pl.SetLyrics(nil)
	tt := ui.NewTester(a.view, 1200, 800)
	settled(tt)
	if err := tt.Click("Like"); err != nil {
		t.Fatal(err)
	}
	if id := wait(t, liked, "the like"); id != "7" || !lib.liked["7"] || a.libs[0].liked["7"] {
		t.Errorf("liked %s; in its library %v, in the active one %v", id, lib.liked["7"], a.libs[0].liked["7"])
	}
}

// Each thing a source can't do is gone with its capability, where it shows
// while it can.
func TestHiddenWithoutCapability(t *testing.T) {
	// The song's own heart, for a page that has no other.
	playing := func(a *app) {
		a.pl.Cue([]music.Track{song("7", "宝船")})
		a.pl.SetLyrics(nil)
	}
	page := func(r route) func(a *app) { return func(a *app) { a.route = r } }
	loaded := func(a *app) {
		a.account = &music.Account{ID: "1", Name: "小明"}
		a.artists["12"] = &artistData{page: &music.ArtistPage{Artist: music.Artist{ID: "12", Name: "乙"}, Tracks: []music.Track{song("1", "一")}}}
		a.albums["8"] = &albumData{album: &music.AlbumPage{Album: music.Album{ID: "8", Name: "迷跡波"}}}
		a.playlists["30"] = &music.Playlist{ID: "30", Name: "华语经典"}
		a.shows["5"] = &showData{show: &music.Show{ID: "5", Name: "夜航"}, page: paged{loaded: true}}
	}
	for _, c := range []struct {
		name, capability, label string
		set                     func(a *app)
	}{
		{"liked songs", "liked", "Liked Songs", nil},
		{"radio", "stations", "Radio", nil},
		{"podcasts", "shows", "Podcasts", nil},
		{"saved albums", "savedAlbums", "Saved Albums", nil},
		{"search", "search", "Search", nil},
		{"new playlist", "createPlaylist", "New Playlist", nil},
		{"heart", "like", "Like", playing},
		{"follow", "followArtist", "Follow", page(route{page: pageArtist, src: "fake", id: "12"})},
		{"save playlist", "savePlaylist", "Save", page(route{page: pagePlaylist, src: "fake", id: "30"})},
		{"save album", "saveAlbum", "Save", page(route{page: pageAlbum, src: "fake", id: "8"})},
		{"subscribe", "subscribeShow", "Subscribe", page(route{page: pageShow, src: "fake", id: "5"})},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			loaded(a)
			if c.set != nil {
				c.set(a)
			}
			tt := ui.NewTester(a.view, 1200, 800)
			settled(tt)
			if _, ok := tt.Find(c.label); !ok {
				t.Fatalf("no %q while it can: %q", c.label, tt.Texts())
			}
			without(a, c.capability)
			settled(tt)
			if _, ok := tt.Find(c.label); ok {
				t.Errorf("%q shows without %s", c.label, c.capability)
			}
		})
	}
}

// Now Playing has no Comments tab for a source without comments, and leaves the
// comments for the queue if they were showing.
func TestCommentsGoWithoutCapability(t *testing.T) {
	asked := make(chan string, 1)
	a := newTestApp()
	fake(a).comments = func(track string, _ int) (*music.CommentPage, error) {
		asked <- track
		return &music.CommentPage{}, nil
	}
	without(a, "comments")
	a.pl.Cue([]music.Track{song("7", "宝船")})
	a.pl.SetLyrics(nil)
	a.nowPlaying, a.npPane = true, paneComments
	tt := ui.NewTester(a.view, 1200, 800)
	settled(tt)
	if _, ok := tt.Find("Comments"); ok {
		t.Error("a Comments tab without comments")
	}
	if a.npPane != paneQueue {
		t.Errorf("on pane %d", a.npPane)
	}
	if len(asked) > 0 {
		t.Error("asked a source without comments for them")
	}
}

// The menus of a playlist offer what the source can do of renaming and deleting,
// and no menu at all without either.
func TestPlaylistMenuGoesWithoutCapability(t *testing.T) {
	menu := func(t *testing.T, a *app, tt *ui.Tester) []string {
		t.Helper()
		tt.Frame()
		if err := tt.RightClick("睡前"); err != nil {
			t.Fatal(err)
		}
		items := tt.Menu()
		tt.CloseMenu()
		return items
	}
	a := editTestApp()
	a.route, a.homeLoading = route{page: pageHome, src: "fake"}, true
	tt := ui.NewTester(a.view, 1200, 800)
	if items := menu(t, a, tt); len(items) < 2 {
		t.Fatalf("the menu is %q", items)
	}
	without(a, "renamePlaylist")
	if err := tt.RightClick("睡前"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Rename…"); err == nil {
		t.Error("offered to rename without being able to")
	}
	if err := tt.ChooseMenuItem("Delete Playlist…"); err != nil {
		t.Errorf("no deleting without renaming: %v", err)
	}
	a.deleting.open = false
	without(a, "deletePlaylist")
	if items := menu(t, a, tt); items != nil {
		t.Errorf("a menu of %q with nothing to do", items)
	}

	a.route = route{page: pagePlaylist, src: "fake", id: "10"}
	tt.Frame()
	if _, ok := tt.Find("More"); ok {
		t.Error("the playlist's page has a menu with nothing in it")
	}
}

// A song's menu doesn't offer what its source can't do with playlists.
func TestSongMenuGoesWithoutCapability(t *testing.T) {
	a := editTestApp()
	a.playlists["10"].Tracks = append(a.playlists["10"].Tracks, song("3", "三"))
	without(a, "addToPlaylist", "removeFromPlaylist", "movePlaylistTrack")
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []string{"Add to Playlist", "Remove from Playlist", "Move Up", "Move Down"} {
		for _, label := range tt.Menu() {
			if label == item {
				t.Errorf("offered %q without being able to", item)
			}
		}
	}
	tt.CloseMenu()
}

// A source answering an error as a page loads shows the page's error, with Retry,
// which loads it again, and the page once it answers.
func TestPageErrorRetries(t *testing.T) {
	offline := errors.New("offline")
	for _, c := range []struct {
		name string
		r    route
		set  func(f *fakeSource, fails func() error)
	}{
		{"playlist", route{page: pagePlaylist, id: "7"}, func(f *fakeSource, fails func() error) {
			f.playlist = func(id string) (*music.Playlist, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return &music.Playlist{ID: id, Name: "Answered"}, nil
			}
		}},
		{"artist", route{page: pageArtist, id: "7"}, func(f *fakeSource, fails func() error) {
			f.artist = func(id string) (*music.ArtistPage, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return &music.ArtistPage{Artist: music.Artist{ID: id, Name: "Answered"}}, nil
			}
		}},
		{"album", route{page: pageAlbum, id: "7"}, func(f *fakeSource, fails func() error) {
			f.album = func(id string) (*music.AlbumPage, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return &music.AlbumPage{Album: music.Album{ID: id, Name: "Answered"}}, nil
			}
		}},
		{"show", route{page: pageShow, id: "7"}, func(f *fakeSource, fails func() error) {
			f.show = func(id string, _ int) (*music.ShowPage, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return &music.ShowPage{Show: music.Show{ID: id, Name: "Answered"}}, nil
			}
		}},
		{"browse", route{page: pageBrowse, id: "7"}, func(f *fakeSource, fails func() error) {
			f.browse = func(string) (*music.Browse, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return &music.Browse{Title: "Answered"}, nil
			}
		}},
		{"liked", route{page: pageLiked}, func(f *fakeSource, fails func() error) {
			f.liked = func(int) (*music.Page, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return &music.Page{Tracks: []music.Track{song("1", "Answered")}}, nil
			}
		}},
		{"stations", route{page: pageStations}, func(f *fakeSource, fails func() error) {
			f.stations = func() ([]music.Station, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return []music.Station{{ID: "1", Name: "Answered"}}, nil
			}
		}},
		{"podcasts", route{page: pageShows}, func(f *fakeSource, fails func() error) {
			f.shows = func() ([]music.Show, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return []music.Show{{ID: "1", Name: "Answered"}}, nil
			}
		}},
		{"saved albums", route{page: pageSavedAlbums}, func(f *fakeSource, fails func() error) {
			f.savedAlbums = func(int) (*music.AlbumList, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return &music.AlbumList{Albums: []music.Album{{ID: "1", Name: "Answered"}}}, nil
			}
		}},
		{"search", route{page: pageSearch, query: "q", kind: music.SearchTracks}, func(f *fakeSource, fails func() error) {
			f.search = func(string, music.SearchKind, int) (*music.SearchResult, error) {
				if err := fails(); err != nil {
					return nil, err
				}
				return &music.SearchResult{Tracks: []music.Track{song("1", "Answered")}}, nil
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			ch := posts(a)
			without(a, "isLiked")
			var tries atomic.Int32
			c.set(fake(a), func() error {
				if tries.Add(1) == 1 {
					return offline
				}
				return nil
			})
			a.open(c.r)
			tt := ui.NewTester(a.view, 1200, 800)
			tt.Frame()
			runPost(t, ch)
			tt.Frame()
			tt.Frame()
			if !tt.HasText("offline") {
				t.Errorf("doesn't say what's wrong: %q", tt.Texts())
			}
			if err := tt.Click("Retry"); err != nil {
				t.Fatalf("%v: %q", err, tt.Texts())
			}
			runPost(t, ch)
			tt.Frame()
			tt.Frame()
			if tries.Load() != 2 {
				t.Errorf("asked %d times", tries.Load())
			}
			if _, ok := tt.Find("Retry"); ok || !tt.HasText("Answered") {
				t.Errorf("once it answered, shows %q", tt.Texts())
			}
		})
	}
}

// Home that a source fails to answer shows the error, with Retry.
func TestHomeErrorRetries(t *testing.T) {
	var tries atomic.Int32
	a := newTestApp()
	ch := posts(a)
	without(a, "recent")
	fake(a).home = func() (*music.Browse, error) {
		if tries.Add(1) == 1 {
			return nil, errors.New("offline")
		}
		return &music.Browse{Sections: []music.Section{{Title: "Shelf", Albums: []music.Album{{ID: "1", Name: "Answered"}}}}}, nil
	}
	a.loadHome(a.library)
	runPost(t, ch)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if !tt.HasText("offline") {
		t.Errorf("doesn't say what's wrong: %q", tt.Texts())
	}
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	runPost(t, ch)
	tt.Frame()
	if _, ok := tt.Find("Answered"); !ok {
		t.Errorf("once it answered, shows %q", tt.Texts())
	}
}

// A source that answers ErrSignIn opens the dialog once, however often it says
// so after, and the page offers to sign in for when it's put away.
func TestErrSignInAsksOnce(t *testing.T) {
	var tries atomic.Int32
	a := newTestApp()
	ch := posts(a)
	fake(a).playlist = func(string) (*music.Playlist, error) { tries.Add(1); return nil, music.ErrSignIn }
	a.open(route{page: pagePlaylist, id: "7"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	runPost(t, ch)
	if !a.signIn.open || a.signIn.lib != a.library || !a.needsSignIn {
		t.Fatalf("dialog %+v, needs signing in %v", a.signIn, a.needsSignIn)
	}
	settled(tt)
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	settled(tt)
	if a.signIn.open {
		t.Fatal("Cancel left the dialog open")
	}

	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	runPost(t, ch)
	settled(tt)
	if a.signIn.open || tries.Load() != 2 {
		t.Fatalf("asked again: dialog open %v, tried %d times", a.signIn.open, tries.Load())
	}

	if err := tt.Click("Sign In"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	if !a.signIn.open {
		t.Error("the page's Sign In opened no dialog")
	}
}

// A source that answers ErrSetup needs setting up, which shows on the page and
// its settings, and goes once a setting is kept.
func TestErrSetupOffersSettings(t *testing.T) {
	kept := make(chan string, 1)
	a := newTestApp()
	ch := posts(a)
	fake(a).info.Settings = []music.Setting{{Key: "key", Label: "API Key"}}
	fake(a).playlist = func(string) (*music.Playlist, error) { return nil, music.ErrSetup }
	fake(a).setSetting = func(key, value string) error { kept <- key + "=" + value; return nil }
	a.library.askedSetup = true // opening the window takes a real app
	a.open(route{page: pagePlaylist, id: "7"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	runPost(t, ch)
	tt.Frame()
	if !a.needsSetup {
		t.Fatal("doesn't need setting up")
	}
	if _, ok := tt.Find("Open Settings"); !ok {
		t.Errorf("the page offers no settings: %q", tt.Texts())
	}

	a.settingsPage = "plugins"
	settings := ui.NewTester(a.settingsView, 760, 1400)
	settings.Frame()
	// The field, under the title labelled as it is.
	r, ok := settings.Find("API Key")
	if !ok {
		t.Fatal("no field")
	}
	settings.ClickAt(r.X+20, r.Y+r.H+18)
	settings.Type("secret")
	settings.Key(0, ui.KeyEnter)
	if got := wait(t, kept, "the setting"); got != "key=secret" {
		t.Fatalf("kept %s", got)
	}
	until(t, ch, func() bool { return !a.needsSetup })
	if a.askedSetup {
		t.Error("it wouldn't ask next time it needs it")
	}
}

// Sign in to X in the account row opens the dialog, whose Sign In calls the source,
// and the account shows; Sign Out goes back.
func TestSignInAndOut(t *testing.T) {
	asked := make(chan struct{}, 1)
	signedOut := make(chan struct{}, 1)
	a := newTestApp()
	ch := posts(a)
	f := fake(a)
	f.signIn = func() (*music.Account, error) {
		asked <- struct{}{}
		return &music.Account{ID: "1", Name: "小明"}, nil
	}
	f.signOut = func() error { signedOut <- struct{}{}; return nil }
	a.homeLoading = true // for the dialog's Sign In to be the one that shows
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Sign in to fake"); err != nil {
		t.Fatal(err)
	}
	settled(tt)
	if !a.signIn.open {
		t.Fatal("no dialog")
	}
	if err := tt.Click("Sign In"); err != nil {
		t.Fatal(err)
	}
	wait(t, asked, "signing in")
	until(t, ch, func() bool { return a.account != nil })
	if a.signIn.open || a.signIn.waiting {
		t.Errorf("the dialog stayed: %+v", a.signIn)
	}
	settled(tt)
	if _, ok := tt.Find("小明"); !ok {
		t.Fatalf("no account in %q", tt.Texts())
	}
	if _, ok := tt.Find("Sign in to fake"); ok {
		t.Error("still offers to sign in")
	}

	if err := tt.Click("小明"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Sign Out"); err != nil {
		t.Fatal(err)
	}
	wait(t, signedOut, "signing out")
	settled(tt)
	if a.account != nil {
		t.Error("still signed in")
	}
	if _, ok := tt.Find("Sign in to fake"); !ok {
		t.Errorf("doesn't offer to sign in again: %q", tt.Texts())
	}
}

// Sign In in the settings asks in the settings window. It used to open the
// dialog in the main window, behind the settings, where it went unseen.
func TestSignInFromSettingsAsksThere(t *testing.T) {
	a := newTestApp()
	a.settingsPage = "plugins"
	main := ui.NewTester(a.view, 1200, 800)
	settings := ui.NewTester(a.settingsView, 780, 1000)
	main.Frame()
	settings.Frame()
	if err := settings.Click("Sign In"); err != nil {
		t.Fatal(err)
	}
	settled(settings)
	settled(main)
	if _, ok := settings.Find("Cancel"); !ok {
		t.Errorf("no dialog in the settings: %q", settings.Texts())
	}
	if _, ok := main.Find("Cancel"); ok {
		t.Error("the dialog opened in the main window")
	}
}

// The settings ask a source not yet used who is signed in, and offer to sign
// out of it. They used to offer Sign In, as it hadn't been asked yet.
func TestSettingsAskAnUnusedSourceItsAccount(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	other := newFakeSource("other")
	other.account = func() (*music.Account, error) { return &music.Account{ID: "1", Name: "小明"}, nil }
	lib := addFake(a, other)
	lib.started, lib.accountAsked = false, false
	a.settingsPage = "plugins"
	settings := ui.NewTester(a.settingsView, 780, 1000)
	settings.Frame()
	until(t, ch, func() bool { return lib.account != nil })
	settled(settings)
	if _, ok := settings.Find("Sign Out"); !ok {
		t.Errorf("doesn't offer to sign out: %q", settings.Texts())
	}
}

// A sign-in that fails says why in its dialog, ready to try again.
func TestSignInFails(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	fake(a).signIn = func() (*music.Account, error) { return nil, errors.New("the code expired") }
	a.promptSignIn(a.library)
	a.startSignIn()
	if !a.signIn.waiting {
		t.Fatal("not waiting")
	}
	runPost(t, ch)
	if !a.signIn.open || a.signIn.waiting || a.signIn.err != "the code expired" || a.account != nil {
		t.Errorf("the dialog is %+v, account %+v", a.signIn, a.account)
	}
}

// Trying again after a sign-in failed at once leaves the dialog as it was: it
// showed its wait for a frame, which takes a line where the error took two, and
// the dialog sprang after it and back, its text moving by fractions of a pixel
// for half a second. One that takes a while says it's waiting.
func TestSignInFailingAtOnceStaysPut(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	msg := "Paste your session's cookie in Settings > Plugins > Example, which says how to find it"
	slow := make(chan struct{})
	var hold chan struct{}
	fake(a).signIn = func() (*music.Account, error) {
		if hold != nil {
			<-hold
		}
		return nil, errors.New(msg)
	}
	tt := ui.NewTester(func(c *ui.Context) { a.signInDialog(c, a.pal, false) }, 800, 600)
	a.promptSignIn(a.library)
	settled(tt)
	if err := tt.Click(T("Sign In")); err != nil {
		t.Fatal(err)
	}
	runPost(t, ch)
	settled(tt)
	before, ok := tt.Find(msg)
	if !ok {
		t.Fatalf("doesn't say why: %q", tt.Texts())
	}

	if err := tt.Click(T("Sign In")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	runPost(t, ch)
	for range 30 {
		if r, ok := tt.Find(msg); !ok || r != before {
			t.Fatalf("the error went from %v to %v (shown %v)", before, r, ok)
		}
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}

	hold = slow
	if err := tt.Click(T("Sign In")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(signInSettle)
	tt.Frame()
	if _, ok := tt.Find(T("Waiting for you to finish signing in…")); !ok {
		t.Errorf("doesn't say it's waiting: %q", tt.Texts())
	}
	close(slow)
	runPost(t, ch)
}

// A sign-in that ends after the dialog was put away still counts, with what's the
// listener's loaded anew.
func TestSignInEndsAfterDialog(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	f := fake(a)
	f.signIn = func() (*music.Account, error) { return &music.Account{ID: "1", Name: "小明"}, nil }
	f.playlists = func() ([]music.Playlist, error) { return []music.Playlist{{ID: "10", Name: "通勤", Mine: true}}, nil }
	a.promptSignIn(a.library)
	a.startSignIn()
	a.signIn.open = false
	until(t, ch, func() bool { return a.account != nil && len(a.mine) > 0 })
	if a.signIn.open {
		t.Error("the dialog came back")
	}
}

// Who's signed in is asked of the source once it starts: signed in already, it's
// known without the dialog, and one who isn't asks nothing of the listener.
func TestAccountAskedOnStart(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	fake(a).account = func() (*music.Account, error) { return &music.Account{ID: "1", Name: "小明"}, nil }
	a.library.started = false
	a.startLibrary(a.library)
	until(t, ch, func() bool { return a.account != nil })
	if a.signIn.open {
		t.Error("asked the listener to sign in")
	}
}

// A plugin shows as itself in the first frame, before anything it was asked has
// come back: this showed the app's default sidebar until a goroutine had asked
// what it can do, and then "sign in" to someone signed in, each moving what's
// around them.
func TestSourceShowsAsItselfAtOnce(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	f := newFakeSource("plugin")
	f.info.Navigation = []music.NavItem{{Title: "Daily", Page: "daily"}}
	answer := make(chan struct{})
	f.account = func() (*music.Account, error) {
		<-answer
		return &music.Account{ID: "1", Name: "小明"}, nil
	}
	lib := newLibrary(f)
	a.libs, a.allLibs, a.library = []*library{lib}, []*library{lib}, lib
	a.route = route{page: pageHome, src: lib.id}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	check := func() {
		t.Helper()
		if _, ok := tt.Find("Daily"); !ok {
			t.Error("its own page isn't in the sidebar")
		}
		if tt.HasText("Sign in to plugin") || tt.HasText("Settings") {
			t.Error("the account's row says what isn't known yet")
		}
	}
	check()
	a.startLibrary(lib)
	tt.Frame()
	check()
	close(answer)
	until(t, ch, func() bool { return a.account != nil })
	tt.Frame()
	if !tt.HasText("小明") {
		t.Error("who's signed in doesn't show")
	}
}

// Who's signed in, said for the first time, changes nothing listed as the source
// started: Home loaded twice at each launch, its picks changing under the listener.
func TestFirstAccountLoadsNothingAgain(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	f := fake(a)
	f.account = func() (*music.Account, error) { return &music.Account{ID: "1", Name: "小明"}, nil }
	asked, answer := make(chan struct{}, 4), make(chan struct{})
	f.home = func() (*music.Browse, error) {
		asked <- struct{}{}
		<-answer
		return &music.Browse{Sections: []music.Section{{Title: "Picks"}}}, nil
	}
	a.library.started, a.library.accountAsked = false, false
	a.startLibrary(a.library)
	wait(t, asked, "Home")
	until(t, ch, func() bool { return a.account != nil })
	close(answer)
	until(t, ch, func() bool { return a.home != nil })
	select {
	case <-asked:
		t.Error("Home was asked for again")
	case <-time.After(50 * time.Millisecond):
	}
}

// What the source refused as it started, not knowing the listener yet, it's asked
// again once it says who they are.
func TestFirstAccountLoadsAgainWhatFailed(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	f := fake(a)
	answer := make(chan struct{})
	f.account = func() (*music.Account, error) {
		<-answer
		return &music.Account{ID: "1", Name: "小明"}, nil
	}
	f.home = func() (*music.Browse, error) { return nil, errors.New("not yet") }
	a.library.started, a.library.accountAsked = false, false
	a.startLibrary(a.library)
	until(t, ch, func() bool { return a.homeErr != "" })
	f.home = func() (*music.Browse, error) {
		return &music.Browse{Sections: []music.Section{{Title: "Picks"}}}, nil
	}
	close(answer)
	until(t, ch, func() bool { return a.home != nil })
}

// A heart asked about before the source said who's signed in is asked about again
// after: its answer is dropped, which left it empty for good.
func TestLikeAskedBeforeAccountAskedAgain(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	f := fake(a)
	accountGo, likedGo := make(chan struct{}), make(chan struct{})
	f.account = func() (*music.Account, error) {
		<-accountGo
		return &music.Account{ID: "1", Name: "小明"}, nil
	}
	f.isLiked = func(ids []string) (map[string]bool, error) {
		<-likedGo
		return map[string]bool{"1": true}, nil
	}
	a.library.started, a.library.accountAsked = false, false
	a.startLibrary(a.library)
	tr := song("1", "Song")
	a.wantLike(&tr)
	a.askLikes()
	close(accountGo)
	until(t, ch, func() bool { return a.account != nil })
	close(likedGo)
	a.wantLike(&tr)
	a.askLikes()
	until(t, ch, func() bool { return a.isLiked(&tr) })
}

// Changing the language says the app's words in it from the next frame on, and
// English again once chosen.
func TestSetLanguage(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	a := newTestApp()
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	onMainThread(func() { a.setLanguage(i18n.Chinese) })
	if i18n.Language() != i18n.Chinese || a.settings.Language != i18n.Chinese {
		t.Fatalf("language %q, kept %q", i18n.Language(), a.settings.Language)
	}
	tt.Frame()
	if _, ok := tt.Find(T("Liked Songs")); !ok {
		t.Errorf("no %q in %q", T("Liked Songs"), tt.Texts())
	}

	onMainThread(func() { a.setLanguage(i18n.English) })
	tt.Frame()
	if _, ok := tt.Find("Liked Songs"); !ok {
		t.Errorf("English didn't come back: %q", tt.Texts())
	}
}

// A plugin asking to start programs may not until the listener allows it in the
// settings, and the same button takes that back.
func TestSettingsAllowPrograms(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	src := &guardedSource{fakeSource: newFakeSource("guarded")}
	src.perms.Process = plugin.Programs{"*"}
	src.perms.Network = []string{"api.example.com"}
	addFake(a, src)

	a.settingsPage = "plugins"
	settings := ui.NewTester(a.settingsView, 760, 1400)
	settings.Frame()
	if err := settings.Click("Allow Programs"); err != nil {
		t.Fatal(err)
	}
	runPost(t, ch)
	if !src.Granted() {
		t.Fatal("not allowed after allowing")
	}
	settings.Frame()
	if err := settings.Click("Stop Allowing"); err != nil {
		t.Fatal(err)
	}
	runPost(t, ch)
	if src.Granted() {
		t.Fatal("still allowed after stopping")
	}
}

// guardedSource is a fake source with permissions, as a plugin has.
type guardedSource struct {
	*fakeSource
	perms   plugin.Permissions
	mu      sync.Mutex
	granted bool
}

func (s *guardedSource) Permissions() plugin.Permissions { return s.perms }

func (s *guardedSource) Granted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.granted
}

func (s *guardedSource) Grant(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.granted = on
	return nil
}

// A source's QR code and what it says show while signing in, and go with it.
func TestSignInShowsPrompt(t *testing.T) {
	release := make(chan struct{})
	a := newTestApp()
	ch := posts(a)
	src := &promptingSource{fakeSource: newFakeSource("qr")}
	src.signIn = func() (*music.Account, error) {
		src.show(music.SignInPrompt{QR: "https://example.com/code", Text: "Scan this code"})
		<-release
		return &music.Account{ID: "1", Name: "Me"}, nil
	}
	lib := addFake(a, src)
	a.homeLoading = true // for the dialog's Sign In to be the one that shows
	a.promptSignIn(lib)
	tt := ui.NewTester(a.view, 1200, 800)
	settled(tt)
	if err := tt.Click("Sign In"); err != nil {
		t.Fatal(err)
	}
	runPost(t, ch)
	settled(tt)
	if a.signIn.qr == nil {
		t.Fatal("no QR code")
	}
	if _, ok := tt.Find("Scan this code"); !ok {
		t.Errorf("the prompt doesn't show: %q", tt.Texts())
	}
	close(release)
	until(t, ch, func() bool { return lib.account != nil })
	if a.signIn.open {
		t.Error("the dialog stayed")
	}
}

// promptingSource is a fake source with something to show while signing in.
type promptingSource struct {
	*fakeSource
	show func(music.SignInPrompt)
}

func (s *promptingSource) OnSignInPrompt(show func(music.SignInPrompt)) { s.show = show }

// Signing in to a source other than the active one goes to it. If the listener
// closed the dialog before it finished, it signs in without moving them.
func TestSignInSwitchesToTheSource(t *testing.T) {
	signIn := func(closed bool) (*app, *library) {
		release := make(chan struct{})
		a := newTestApp()
		ch := posts(a)
		src := newFakeSource("other")
		src.signIn = func() (*music.Account, error) {
			<-release
			return &music.Account{ID: "1", Name: "Me"}, nil
		}
		lib := addFake(a, src)
		a.promptSignIn(lib)
		a.startSignIn()
		if closed {
			a.promptSignIn(a.libs[0]) // another dialog since
		}
		close(release)
		until(t, ch, func() bool { return lib.account != nil })
		return a, lib
	}
	if a, lib := signIn(false); a.library != lib {
		t.Errorf("still on %s after signing in", a.library.id)
	}
	if a, lib := signIn(true); a.library == lib {
		t.Error("moved after the dialog had gone")
	}
}

// Going back to following the system speaks its language again.
func TestFollowSystemLanguage(t *testing.T) {
	defer i18n.SetLanguage(i18n.English)
	was := systemLanguages
	defer func() { systemLanguages = was }()
	systemLanguages = func() []string { return []string{"zh-Hans-CN"} }
	a := newTestApp()
	onMainThread(func() { a.setLanguage(i18n.English) })
	if i18n.Language() != i18n.English {
		t.Fatalf("speaks %q", i18n.Language())
	}
	onMainThread(func() { a.setLanguage(i18n.System) })
	if i18n.Language() != i18n.Chinese || a.settings.Language != i18n.System {
		t.Fatalf("speaks %q, kept %q", i18n.Language(), a.settings.Language)
	}
}

// The settings' sidebar opens its pages, one at a time.
func TestSettingsSidebarOpensPages(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.settingsView, 780, 800)
	tt.Frame()
	if _, ok := tt.Find("Language"); !ok {
		t.Fatalf("General isn't the first page: %q", tt.Texts())
	}
	if err := tt.Click("Playback"); err != nil {
		t.Fatal(err)
	}
	settled(tt)
	if _, ok := tt.Find("Quality"); !ok {
		t.Errorf("no Quality on Playback: %q", tt.Texts())
	}
	if _, ok := tt.Find("Language"); ok {
		t.Error("General's still showing")
	}
}

// A search finds settings across the pages, and only those; one that finds
// nothing says so; and choosing a page leaves it.
func TestSettingsSearch(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.settingsView, 780, 800)
	tt.Frame()
	if err := tt.Click("Search Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("QUALITY")
	settled(tt)
	if _, ok := tt.Find("Quality"); !ok {
		t.Fatalf("didn't find it: %q", tt.Texts())
	}
	if _, ok := tt.Find("Language"); ok {
		t.Error("shows what it didn't look for")
	}
	if _, ok := tt.Find("Even Out Loudness"); ok {
		t.Error("shows a row of the same page it didn't look for")
	}

	a.settingsQuery = "zzzz"
	settled(tt)
	if _, ok := tt.Find("No settings found"); !ok {
		t.Errorf("says nothing of finding nothing: %q", tt.Texts())
	}

	// By a page's name, all of it.
	a.settingsQuery = "playback"
	settled(tt)
	if _, ok := tt.Find("Even Out Loudness"); !ok {
		t.Errorf("the page's rows aren't there: %q", tt.Texts())
	}

	if err := tt.Click("Cache"); err != nil {
		t.Fatal(err)
	}
	settled(tt)
	if a.settingsQuery != "" || a.settingsPage != "cache" {
		t.Errorf("query %q on page %q", a.settingsQuery, a.settingsPage)
	}
}

// A control a search found works as on its page.
func TestSettingsFoundControlWorks(t *testing.T) {
	a := newTestApp()
	a.settingsQuery = "loudness"
	tt := ui.NewTester(a.settingsView, 780, 800)
	settled(tt)
	was := a.settings.Normalize
	// The row's title is labelled as its switch is: click the switch, at its end.
	top, ok1 := tt.Find("Even Out Loudness")
	bottom, ok2 := tt.Find("Adjust each song's volume by what its plugin measured of it, for them to sound as loud as each other")
	if !ok1 || !ok2 {
		t.Fatalf("not found: %q", tt.Texts())
	}
	tt.ClickAt(780-28-16-12, (top.Y+bottom.Y+bottom.H)/2)
	tt.Frame()
	if a.settings.Normalize == was {
		t.Error("the switch did nothing")
	}
}

// Turning the plugin that shows off moves to the next one on, and it's gone from
// what songs can play through; the last one on can't be turned off; and it's kept.
func TestTurnPluginOff(t *testing.T) {
	a := newTestApp()
	_, other := addOther(a)
	first := a.libs[0]
	a.switchSource(other.id)
	settings := func() *ui.Tester {
		a.settingsPage = "plugins"
		tt := ui.NewTester(a.settingsView, 780, 1400)
		tt.Frame()
		return tt
	}
	tt := settings()
	if err := tt.Click("Enable " + other.name()); err != nil {
		t.Fatal(err)
	}
	if a.library != first {
		t.Fatalf("still on %s", a.library.id)
	}
	if a.sourceByID(other.id) != nil {
		t.Error("its songs can still play")
	}
	if !slices.Contains(a.settings.DisabledPlugins, other.id) {
		t.Error("not kept")
	}

	a.setPluginEnabled(first, false)
	if len(a.libs) != 1 || a.libs[0] != first {
		t.Fatalf("turned the last one off: %d left", len(a.libs))
	}

	a.setPluginEnabled(other, true)
	if a.sourceByID(other.id) == nil || slices.Contains(a.settings.DisabledPlugins, other.id) {
		t.Error("didn't come back on")
	}
}

// From GitHub… in the plugins' + menu asks for a repository: something that's
// no repository asks again, without going to GitHub; a repository goes, and what
// comes back that's wrong shows, ready to try again.
func TestInstallPluginFromSettings(t *testing.T) {
	asked := make(chan string, 1)
	was := fetchPlugin
	defer func() { fetchPlugin = was }()
	fetchPlugin = func(repo, dir string) (*plugin.Pending, error) {
		asked <- repo
		return nil, plugin.ErrNoRepo
	}
	a := newTestApp()
	ch := posts(a)
	a.settingsPage = "plugins"
	tt := ui.NewTester(a.settingsView, 780, 1000)
	tt.Frame()
	if err := tt.Click("Install a Plugin"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("From GitHub…"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	tt.Frame()

	a.installer.repo = "not a repo"
	tt.Frame()
	if err := tt.Click("Install"); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 0 || a.installer.err == "" {
		t.Fatalf("asked %d, says %q", len(asked), a.installer.err)
	}

	a.installer.repo = "https://github.com/owner/repo"
	tt.Frame()
	if err := tt.Click("Install"); err != nil {
		t.Fatal(err)
	}
	if got := wait(t, asked, "the download"); got != "owner/repo" {
		t.Fatalf("asked for %q", got)
	}
	runPost(t, ch)
	if a.installer.busy || a.installer.err != T("There's no such public repository on GitHub") {
		t.Errorf("busy %v, says %q", a.installer.busy, a.installer.err)
	}
}

// A link to install a plugin shows the plugins' settings and downloads it from
// the repository it names, to ask about there, installing nothing by itself;
// links to anything else do nothing.
func TestInstallPluginFromLink(t *testing.T) {
	asked := make(chan string, 1)
	was := fetchPlugin
	defer func() { fetchPlugin = was }()
	fetchPlugin = func(repo, dir string) (*plugin.Pending, error) {
		asked <- repo
		return &plugin.Pending{Release: plugin.Release{Repo: repo}, Manifest: plugin.Manifest{ID: "linked", Name: "Linked"}}, nil
	}
	a := newTestApp()
	ch := posts(a)
	libs := len(a.allLibs)

	for _, link := range []string{linkScheme + "://settings", linkScheme + "://install-plugin", "https://example.com/install-plugin?repo=owner/repo"} {
		a.openLink(link)
	}
	if len(asked) != 0 || a.settingsPage == "plugins" {
		t.Fatalf("acted on a link that isn't to install one: asked %d, on %q", len(asked), a.settingsPage)
	}

	a.openLink(linkScheme + "://install-plugin?repo=" + url.QueryEscape("https://github.com/owner/repo/tree/main"))
	if got := wait(t, asked, "the download"); got != "owner/repo" {
		t.Fatalf("asked for %q", got)
	}
	if a.settingsPage != "plugins" {
		t.Errorf("shows the settings' %q", a.settingsPage)
	}
	runPost(t, ch)
	if !a.installer.open || a.installer.pending == nil || a.installer.pending.Manifest.ID != "linked" {
		t.Fatalf("asks about %v", a.installer.pending)
	}
	if len(a.allLibs) != libs {
		t.Error("installed it without asking")
	}
}

// Picking a plugin's folder from the plugins' + menu copies it in and asks about
// it, as one from GitHub is asked about, until it's turned down; a zip that's no
// zip says so.
func TestInstallPluginFromDisk(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"id":"disk","name":"Disk"}`), 0o644)
	os.WriteFile(filepath.Join(src, "index.js"), []byte(""), 0o644)
	notZip := filepath.Join(src, "index.js")
	plugins := t.TempDir()
	wasPick, wasOpen := pickPlugin, openPlugin
	defer func() { pickPlugin, openPlugin = wasPick, wasOpen }()
	pickPlugin = func(a *app, folder bool, got func(string)) {
		if folder {
			got(src)
		} else {
			got(notZip)
		}
	}
	openPlugin = func(path, _ string) (*plugin.Pending, error) { return plugin.Open(path, plugins) }
	a := newTestApp()
	ch := posts(a)
	a.settingsPage = "plugins"
	tt := ui.NewTester(a.settingsView, 780, 1000)
	tt.Frame()

	choose := func(item string) {
		t.Helper()
		if err := tt.Click("Install a Plugin"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(item); err != nil {
			t.Fatalf("%v; the menu shows %q", err, tt.Menu())
		}
	}
	choose("From a Folder…")
	runPost(t, ch)
	tt.Frame()
	if a.installer.pending == nil || a.installer.err != "" {
		t.Fatalf("asks about nothing, and says %q", a.installer.err)
	}
	if err := tt.Click(T("Cancel")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.installer.pending != nil {
		t.Error("still asks about it")
	}

	settled(tt)
	choose("From a Zip…")
	runPost(t, ch)
	if a.installer.pending != nil || a.installer.err == "" || a.installer.busy {
		t.Errorf("pending %v, busy %v, says %q", a.installer.pending, a.installer.busy, a.installer.err)
	}
}

// Any plugin can be removed from its menu in the plugins' settings, from this
// computer too, once the listener says so: it's gone from the sources and its
// folder goes to the Trash. Saying no leaves it.
func TestRemovePluginFromSettings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "disk")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"disk","name":"Disk"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0o644)
	p, err := plugin.Load(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	trashed := make(chan string, 1)
	answer := false
	wasConfirm, wasTrash := confirmRemovePlugin, trashItem
	defer func() { confirmRemovePlugin, trashItem = wasConfirm, wasTrash }()
	confirmRemovePlugin = func(a *app, name string, yes func()) {
		if answer {
			yes()
		}
	}
	trashItem = func(path string) error { trashed <- path; return nil }
	a := newTestApp()
	lib := addFake(a, p)
	a.plugins = append(a.plugins, p)
	a.settingsPage = "plugins"
	tt := ui.NewTester(a.settingsView, 780, 1400)
	tt.Frame()

	remove := func() {
		t.Helper()
		if err := tt.Click("More for Disk"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("Remove…"); err != nil {
			t.Fatalf("%v; the menu shows %q", err, tt.Menu())
		}
		tt.Frame()
	}
	remove()
	if !slices.Contains(a.allLibs, lib) {
		t.Fatal("removed though the listener said no")
	}
	answer = true
	remove()
	if slices.Contains(a.allLibs, lib) || slices.Contains(a.libs, lib) || slices.Contains(a.plugins, p) {
		t.Error("still a source")
	}
	if got := wait(t, trashed, "the folder going to the Trash"); got != dir {
		t.Errorf("trashed %q", got)
	}
}

// A new version asking for more than the old had has to ask again.
func TestUpdateAsksForMore(t *testing.T) {
	had := plugin.Permissions{Network: []string{"a.example.com"}}
	for _, c := range []struct {
		wants plugin.Permissions
		more  bool
	}{
		{plugin.Permissions{Network: []string{"a.example.com"}}, false},
		{plugin.Permissions{}, false},
		{plugin.Permissions{Network: []string{"b.example.com"}}, true},
		{plugin.Permissions{Network: []string{"*"}}, true},
		{plugin.Permissions{Process: plugin.Programs{"ffmpeg"}}, true},
	} {
		if got := asksForMore(had, c.wants); got != c.more {
			t.Errorf("%+v: %v", c.wants, got)
		}
	}
	if asksForMore(plugin.Permissions{Network: []string{"*"}}, had) {
		t.Error("any website already covers one")
	}
	// Another program is more, even with the one it had; any program covers it.
	ffmpeg := plugin.Permissions{Process: plugin.Programs{"ffmpeg"}}
	if !asksForMore(ffmpeg, plugin.Permissions{Process: plugin.Programs{"ffmpeg", "bin/helper"}}) {
		t.Error("another program wasn't asked about")
	}
	if asksForMore(plugin.Permissions{Process: plugin.Programs{"*"}}, ffmpeg) {
		t.Error("any program already covers one")
	}
}

// Checking a plugin for updates from its menu asks GitHub even though the
// daily check just did: with nothing new it's up to date, and a new version
// shows its Update button.
func TestCheckPluginUpdateNow(t *testing.T) {
	latest := "v0.1.0"
	was := latestPlugin
	defer func() { latestPlugin = was }()
	latestPlugin = func(repo string) (*plugin.Release, error) {
		return &plugin.Release{Repo: repo, Version: latest}, nil
	}
	a := newTestApp()
	ch := posts(a)
	addFake(a, githubPlugin(t))
	a.settings.PluginsChecked = time.Now()
	a.settingsPage = "plugins"
	tt := ui.NewTester(a.settingsView, 780, 1000)
	tt.Frame()

	check := func() {
		t.Helper()
		if err := tt.Click("More for GH"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("Check for Updates"); err != nil {
			t.Fatalf("%v; the menu shows %q", err, tt.Menu())
		}
		runPost(t, ch)
		tt.Frame()
	}
	check()
	if a.installer.checked["gh"] != upToDate || len(a.settings.PluginUpdates) != 0 {
		t.Errorf("checked %v, found %v", a.installer.checked, a.settings.PluginUpdates)
	}
	latest = "v0.2.0"
	check()
	if _, ok := tt.Find("Update to v0.2.0"); !ok {
		t.Errorf("no update: %q", tt.Texts())
	}
	settled(tt)
}

// Updating a plugin installs the version its check found without asking GitHub
// what's there again, as each ask counts against its limit; once the app has
// opened again and it's forgotten what was found, it asks.
func TestUpdatePluginReusesCheck(t *testing.T) {
	asked := 0
	wasLatest, wasFetch, wasRelease := latestPlugin, fetchPlugin, fetchRelease
	defer func() { latestPlugin, fetchPlugin, fetchRelease = wasLatest, wasFetch, wasRelease }()
	latestPlugin = func(repo string) (*plugin.Release, error) {
		asked++
		return &plugin.Release{Repo: repo, Version: "v0.2.0"}, nil
	}
	fetched := make(chan string, 1)
	fetchRelease = func(rel *plugin.Release, dir string) (*plugin.Pending, error) {
		fetched <- "release " + rel.Version
		return nil, errors.New("offline")
	}
	fetchPlugin = func(repo, dir string) (*plugin.Pending, error) {
		fetched <- "latest of " + repo
		return nil, errors.New("offline")
	}
	a := newTestApp()
	ch := posts(a)
	addFake(a, githubPlugin(t))
	a.settingsPage = "plugins"
	tt := ui.NewTester(a.settingsView, 780, 1000)
	tt.Frame()
	if err := tt.Click("More for GH"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Check for Updates"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	runPost(t, ch)
	tt.Frame()

	update := func() string {
		t.Helper()
		if err := tt.Click("Update to v0.2.0"); err != nil {
			t.Fatalf("%v; shows %q", err, tt.Texts())
		}
		got := wait(t, fetched, "the download")
		runPost(t, ch)
		tt.Frame()
		return got
	}
	if got := update(); got != "release v0.2.0" || asked != 1 {
		t.Errorf("updating fetched the %s, asking GitHub %d times", got, asked)
	}
	a.installer.found = nil
	if got := update(); got != "latest of o/gh" {
		t.Errorf("after opening again, updating fetched the %s", got)
	}
	settled(tt)
}

// githubPlugin is a plugin installed from o/gh at v0.1.0.
func githubPlugin(t *testing.T) *plugin.Plugin {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "gh")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"gh","name":"GH"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "index.js"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, ".origin.json"), []byte(`{"repo":"o/gh","version":"v0.1.0"}`), 0o644)
	p, err := plugin.Load(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Clicking where a plugin came from in its settings opens its repository on
// GitHub.
func TestPluginRepoOpensGitHub(t *testing.T) {
	a := newTestApp()
	addFake(a, githubPlugin(t))
	a.settingsPage = "plugins"
	tt := ui.NewTester(a.settingsView, 780, 1000)
	tt.Frame()
	if err := tt.Click("o/gh"); err != nil {
		t.Fatal(err)
	}
	if got := tt.OpenedURLs(); len(got) != 1 || got[0] != "https://github.com/o/gh" {
		t.Errorf("opened %q", got)
	}
	settled(tt)
}
