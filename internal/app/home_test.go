package app

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/local"
	"mygo-music/internal/music"
)

// localApp has only the built-in source, keeping its data in a directory of
// the test's, with no folders yet.
func localApp(t *testing.T) *app {
	t.Helper()
	a := newTestApp()
	a.local = local.New(t.TempDir())
	a.libs = nil
	a.library = addFake(a, a.local)
	a.route = route{page: pageHome, src: local.ID}
	return a
}

// A source's own Home shows what it makes up, as a card's click shows: its
// first section of tracks as the feature card, and the rest as shelves.
func TestHomeShowsTheSourcesSections(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	fake(a).home = func() (*music.Browse, error) {
		return &music.Browse{Sections: []music.Section{
			{Title: "For You", Tracks: []music.Track{song("1", "一")}},
			{Title: "New Albums", Albums: []music.Album{{ID: "8", Name: "迷跡波"}}},
			{Title: "Popular", Playlists: []music.Playlist{{ID: "42", Name: "飙升榜"}}},
		}}, nil
	}
	a.loadHome(a.library)
	runPost(t, ch)
	tt := ui.NewTester(a.view, 1200, 1400)
	tt.Frame()
	if _, ok := tt.Find("For You"); !ok {
		t.Fatal("no feature card")
	}
	if err := tt.Click("迷跡波"); err != nil {
		t.Fatal(err)
	}
	if a.route != (route{page: pageAlbum, src: "fake", id: "8"}) {
		t.Fatalf("the album's card opened %+v", a.route)
	}
	tt.Key(0, ui.KeyBack)
	tt.Frame()
	if err := tt.Click("飙升榜"); err != nil {
		t.Fatal(err)
	}
	if a.route != (route{page: pagePlaylist, src: "fake", id: "42"}) {
		t.Fatalf("the playlist's card opened %+v", a.route)
	}
}

// browseApp's source answers with a page of what a page is called, which the
// test finds in asked.
func browseApp() (*app, chan func(), chan string) {
	a := newTestApp()
	ch := posts(a)
	without(a, "isLiked")
	asked := make(chan string, 8)
	fake(a).browse = func(page string) (*music.Browse, error) {
		asked <- page
		return &music.Browse{Title: "Page " + page, Sections: []music.Section{{Tracks: []music.Track{song(page+"-1", "Song of "+page)}}}}, nil
	}
	return a, ch, asked
}

// A section's See All opens the page with the rest of it, once, which back
// and forward go to and from without asking again.
func TestSeeAllOpensBrowsePage(t *testing.T) {
	a, ch, asked := browseApp()
	a.home = &music.Browse{Sections: []music.Section{{Title: "New Albums", Albums: []music.Album{{ID: "8", Name: "迷跡波"}}, MorePage: "albums"}}}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("See All"); err != nil {
		t.Fatal(err)
	}
	browse := route{page: pageBrowse, src: "fake", id: "albums"}
	if a.route != browse {
		t.Fatalf("opened %+v", a.route)
	}
	if page := wait(t, asked, "the page"); page != "albums" {
		t.Fatalf("asked for %q", page)
	}
	runPost(t, ch)
	tt.Frame()
	if _, ok := tt.Find("Page albums"); !ok {
		t.Fatalf("the page shows %q", tt.Texts())
	}

	tt.Key(0, ui.KeyBack)
	tt.Frame()
	if a.route != (route{page: pageHome, src: "fake"}) {
		t.Fatalf("back went to %+v", a.route)
	}
	tt.Key(0, ui.KeyForward)
	tt.Frame()
	if a.route != browse {
		t.Fatalf("forward went to %+v", a.route)
	}
	if _, ok := tt.Find("Page albums"); !ok || len(asked) > 0 {
		t.Errorf("shows %q, asked %d more", tt.Texts(), len(asked))
	}
}

// A page a source made up, opened again a while after it loaded, is asked
// for anew, showing as it was until the answer comes. It was kept until the
// app restarted, so a page of songs played lately never caught up.
func TestBrowsePageFreshensWhenOpenedLater(t *testing.T) {
	a, ch, asked := browseApp()
	a.home = &music.Browse{Sections: []music.Section{{Title: "New Albums", Albums: []music.Album{{ID: "8", Name: "迷跡波"}}, MorePage: "albums"}}}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("See All"); err != nil {
		t.Fatal(err)
	}
	wait(t, asked, "the page")
	runPost(t, ch)
	tt.Key(0, ui.KeyBack)
	tt.Frame()
	a.library.browses["albums"].at = time.Now().Add(-listsFresh - time.Second)
	tt.Key(0, ui.KeyForward)
	tt.Frame()
	if page := wait(t, asked, "the page again"); page != "albums" {
		t.Fatalf("asked for %q", page)
	}
	if _, ok := tt.Find("Page albums"); !ok {
		t.Errorf("showed %q while asking again", tt.Texts())
	}
	runPost(t, ch)
}

// A section's Link opens the page it names, on a page that a link led to too,
// and a track listed on such a page plays from there.
func TestLinkOpensBrowsePage(t *testing.T) {
	a, ch, asked := browseApp()
	a.home = &music.Browse{Sections: []music.Section{{Title: "Moods", Links: []music.Link{{Title: "Calm", Page: "calm"}}}}}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Calm"); err != nil {
		t.Fatal(err)
	}
	if a.route != (route{page: pageBrowse, src: "fake", id: "calm"}) {
		t.Fatalf("opened %+v", a.route)
	}
	wait(t, asked, "the page")
	runPost(t, ch)
	tt.Frame()
	if err := tt.Click("Song of calm"); err != nil {
		t.Fatal(err)
	}
	if cur := a.pl.Current(); cur == nil || cur.ID != "calm-1" {
		t.Errorf("playing %+v", cur)
	}
}

// An entry a source adds to the sidebar opens the page it names, and is gone
// with the capability of browsing.
func TestNavItemOpensBrowsePage(t *testing.T) {
	a, _, asked := browseApp()
	fake(a).info.Navigation = []music.NavItem{{Title: "Top Songs", Page: "top", Icon: "chart"}}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Top Songs"); err != nil {
		t.Fatal(err)
	}
	if a.route != (route{page: pageBrowse, src: "fake", id: "top"}) {
		t.Fatalf("opened %+v", a.route)
	}
	if page := wait(t, asked, "the page"); page != "top" {
		t.Fatalf("asked for %q", page)
	}
	without(a, "browse")
	tt.Frame()
	if _, ok := tt.Find("Top Songs"); ok {
		t.Error("the entry stayed without browsing")
	}
}

// A source's Navigation is its sidebar, in its order and by its names, leaving out
// what it doesn't list and what it can't do; with none it gets the app's pages.
func TestNavigationIsTheSidebar(t *testing.T) {
	a, _, _ := browseApp()
	f := fake(a)
	f.caps["stations"], f.caps["liked"], f.caps["shows"] = true, true, true
	f.info.Navigation = []music.NavItem{
		{Title: "Top Songs", Page: "top", Icon: "chart"},
		{Title: "My FM", Builtin: "stations"},
		{Builtin: "home"},
		{Title: "Not Possible", Builtin: "savedAlbums"},
		{Title: "Nonsense", Builtin: "nope"},
	}
	a.library.caps = probeCaps(f)
	without(a, "savedAlbums")
	got := func() []string {
		var labels []string
		for _, item := range a.sidebarItems() {
			labels = append(labels, item.label)
		}
		return labels
	}
	if labels := strings.Join(got(), ","); labels != "Top Songs,My FM,Home" {
		t.Fatalf("sidebar: %s", labels)
	}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("My FM"); err != nil {
		t.Fatal(err)
	}
	if a.route.page != pageStations {
		t.Fatalf("opened %+v", a.route)
	}
	if _, ok := tt.Find("Liked Songs"); ok {
		t.Error("an entry the source didn't list")
	}
	f.info.Navigation = nil
	if labels := strings.Join(got(), ","); labels != "Home,Liked Songs,Radio,Podcasts" {
		t.Fatalf("default sidebar: %s", labels)
	}
}

// With no Home of its own, a source's shows what it played lately, which plays,
// and its playlists, which open.
func TestHomeFallsBackToRecentAndPlaylists(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	without(a, "home", "isLiked")
	fake(a).recent = func() ([]music.Track, error) { return []music.Track{song("1", "一")}, nil }
	fake(a).playlists = func() ([]music.Playlist, error) { return []music.Playlist{{ID: "10", Name: "通勤", Mine: true}}, nil }
	a.loadHome(a.library)
	a.loadMine(a.library)
	until(t, ch, func() bool { return len(a.recent) > 0 && len(a.mine) > 0 })
	a.mineCollapsed = true // for the card to be the one thing showing it
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("一"); err != nil {
		t.Fatal(err)
	}
	if cur := a.pl.Current(); cur == nil || cur.ID != "1" {
		t.Fatalf("playing %+v", cur)
	}
	if err := tt.Click("通勤"); err != nil {
		t.Fatal(err)
	}
	if a.route != (route{page: pagePlaylist, src: "fake", id: "10"}) {
		t.Errorf("opened %+v", a.route)
	}
}

// A source's own Home, slower than its playlists, has them show nowhere on it
// meanwhile: they showed as the fallback Home, which its sections then took the
// place of, moving everything at each launch.
func TestHomeWaitsForTheSourcesOwn(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	without(a, "isLiked")
	answer := make(chan struct{})
	fake(a).home = func() (*music.Browse, error) {
		<-answer
		return &music.Browse{Sections: []music.Section{{Title: "推荐歌单", Playlists: []music.Playlist{{ID: "20", Name: "夜跑"}}}}}, nil
	}
	fake(a).playlists = func() ([]music.Playlist, error) { return []music.Playlist{{ID: "10", Name: "通勤", Mine: true}}, nil }
	a.mineCollapsed = true // for the sidebar not to list it either
	a.loadHome(a.library)
	a.loadMine(a.library)
	until(t, ch, func() bool { return len(a.mine) > 0 })
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if tt.HasText("通勤") {
		t.Error("the playlists show while Home loads")
	}
	close(answer)
	until(t, ch, func() bool { return a.home != nil })
	tt.Frame()
	if !tt.HasText("夜跑") || tt.HasText("通勤") {
		t.Error("Home isn't the source's own")
	}
}

// A source's Home that has no sections falls back as one that has no Home does.
func TestHomeWithoutSectionsFallsBack(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	fake(a).home = func() (*music.Browse, error) { return &music.Browse{}, nil }
	fake(a).recent = func() ([]music.Track, error) { return []music.Track{song("1", "一")}, nil }
	a.loadHome(a.library)
	runPost(t, ch)
	if a.home != nil || len(a.recent) != 1 {
		t.Errorf("home %+v, recent %+v", a.home, a.recent)
	}
}

// With nothing at all to show, Home says so rather than loading on: and a
// source to sign in to offers that.
func TestHomeEmptyState(t *testing.T) {
	a := newTestApp()
	without(a, "signIn")
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if _, ok := tt.Find("Nothing here yet"); !ok {
		t.Fatalf("Home shows %q", tt.Texts())
	}

	a = newTestApp()
	tt = ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Sign In"); err != nil {
		t.Fatal(err)
	}
	if !a.signIn.open || a.signIn.lib != a.library {
		t.Errorf("the dialog is %+v", a.signIn)
	}
}

// The local files' empty Home asks for a music folder, by the dialog that tests
// replace.
func TestLocalEmptyHomeAddsFolder(t *testing.T) {
	asked := 0
	real := pickFolders
	pickFolders = func(*app, func([]string)) { asked++ }
	defer func() { pickFolders = real }()

	a := localApp(t)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Add a Music Folder"); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Errorf("asked for folders %d times", asked)
	}
}

// Home loads anew once it's been showing a while, but not while it's fresh.
func TestHomeFreshens(t *testing.T) {
	asked := make(chan struct{}, 4)
	a := newTestApp()
	fake(a).home = func() (*music.Browse, error) { asked <- struct{}{}; return nil, nil }

	a.homeAt = time.Now().Add(-homeFresh / 2)
	a.freshenHome()
	if a.homeLoading {
		t.Fatal("loaded Home anew while fresh")
	}
	a.homeAt = time.Now().Add(-2 * homeFresh)
	a.freshenHome()
	wait(t, asked, "Home")
}

// A plugin's Home stays as it was while it loads anew, and a browse page shown
// loads anew when its source says what it lists changed.
func TestLibraryChangedReloadsWhatShows(t *testing.T) {
	a, ch, asked := browseApp()
	a.browses["top"] = &browseData{page: &music.Browse{Title: "Old"}}
	a.browses["other"] = &browseData{page: &music.Browse{Title: "Other"}}
	a.home = &music.Browse{Sections: []music.Section{{Title: "Old Home"}}}
	a.route = route{page: pageBrowse, src: "fake", id: "top"}

	a.libraryChanged(a.library)
	if a.home == nil || a.browses["top"].page == nil || a.browses["top"].page.Title != "Old" {
		t.Errorf("what shows went: home %+v, page %+v", a.home, a.browses["top"].page)
	}
	if a.browses["other"] != nil {
		t.Error("a page not showing was kept")
	}
	if page := wait(t, asked, "the page shown"); page != "top" {
		t.Fatalf("asked for %q", page)
	}
	until(t, ch, func() bool { return a.browses["top"].page.Title == "Page top" })
}
