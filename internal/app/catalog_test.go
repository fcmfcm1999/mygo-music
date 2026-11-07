package app

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// searchRequest is a search asked of a source.
type searchRequest struct {
	query  string
	kind   music.SearchKind
	offset int
}

// fakeSearch makes the source's search report what it was asked, and find
// nothing, which lands nowhere without a window.
func fakeSearch(a *app) chan searchRequest {
	asked := make(chan searchRequest, 8)
	fake(a).search = func(q string, kind music.SearchKind, offset int) (*music.SearchResult, error) {
		asked <- searchRequest{q, kind, offset}
		return nil, errors.New("offline")
	}
	return asked
}

// Enter searches songs for the query typed; a tab searches it for another
// kind, which the next search keeps, and back leaves the search.
func TestSearchKinds(t *testing.T) {
	a := newTestApp()
	asked := fakeSearch(a)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	tt.Type("迷星叫")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := wait(t, asked, "the search"); got != (searchRequest{"迷星叫", music.SearchTracks, 0}) {
		t.Fatalf("searched %+v", got)
	}

	if err := tt.Click("Artists"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := wait(t, asked, "the artists"); got != (searchRequest{"迷星叫", music.SearchArtists, 0}) {
		t.Fatalf("searched %+v", got)
	}
	a.search("壱雫空")
	if got := wait(t, asked, "the next search"); got.kind != music.SearchArtists {
		t.Errorf("the next search looked for %q", got.kind)
	}
	if !slices.Equal(a.settings.SearchHistory, []string{"壱雫空", "迷星叫"}) {
		t.Errorf("history %q", a.settings.SearchHistory)
	}

	tt.Key(0, ui.KeyBack)
	tt.Key(0, ui.KeyBack)
	if a.route.page != pageHome {
		t.Errorf("back went to %+v", a.route)
	}
}

// The tabs over a search's songs switch it to another kind: it crashed,
// the list's state reset as the list built the tabs in its header.
func TestSearchKindFromSongs(t *testing.T) {
	a := catalogApp()
	asked := fakeSearch(a)
	a.open(route{page: pageSearch, query: "MyGO", kind: music.SearchTracks})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Artists"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.route.kind != music.SearchArtists {
		t.Fatalf("on %+v", a.route)
	}
	if len(asked) > 0 {
		t.Error("the artists found were searched for again")
	}
}

// The Playlists tab searches playlists, and a playlist it found opens its
// page.
func TestSearchPlaylists(t *testing.T) {
	a := catalogApp()
	asked := fakeSearch(a)
	a.open(route{page: pageSearch, query: "MyGO", kind: music.SearchTracks})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Playlists"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := wait(t, asked, "the playlists"); got != (searchRequest{"MyGO", music.SearchPlaylists, 0}) {
		t.Fatalf("searched %+v", got)
	}

	a.searches[a.route] = &searchData{
		res:  music.SearchResult{Playlists: []music.Playlist{{ID: "42", Name: "迷途人偶", TrackCount: 52}}},
		page: paged{loaded: true},
	}
	a.playlists["42"] = &music.Playlist{ID: "42", Name: "迷途人偶"} // so its page loads nothing
	tt.Frame()
	if err := tt.Click("迷途人偶"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.route != (route{page: pagePlaylist, src: "fake", id: "42"}) {
		t.Errorf("clicked through to %+v", a.route)
	}
}

// A search's next page loads once its foot comes into view, from where the
// last ended, and a search done again loads from its start.
func TestSearchLoadsMore(t *testing.T) {
	a := newTestApp()
	asked := fakeSearch(a)
	r := route{page: pageSearch, src: "fake", query: "迷星叫", kind: music.SearchPlaylists}
	a.searches[r] = &searchData{
		res:  music.SearchResult{Playlists: []music.Playlist{{ID: "1", Name: "甲"}, {ID: "2", Name: "乙"}}, More: true},
		page: paged{loaded: true, more: true},
	}
	a.open(r)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	tt.Frame()
	if got := wait(t, asked, "the next page"); got != (searchRequest{"迷星叫", music.SearchPlaylists, 2}) {
		t.Fatalf("asked for %+v", got)
	}

	a.search("迷星叫")
	if got := wait(t, asked, "the search again"); got.offset != 0 {
		t.Errorf("searched again from %d", got.offset)
	}
}

// A search before, from the empty field's popup, searches again, clicked
// or chosen with the arrows; it was lost when its option was keyed anew.
func TestSearchHistoryChoice(t *testing.T) {
	for _, keys := range []bool{false, true} {
		a := newTestApp()
		asked := fakeSearch(a)
		a.settings.SearchHistory = []string{"迷星叫", "壱雫空"}
		tt := ui.NewTester(a.view, 1200, 800)
		tt.Frame()
		if err := tt.Click("Search"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if keys {
			tt.Key(0, ui.KeyDown)
			tt.Key(0, ui.KeyDown)
			tt.Key(0, ui.KeyEnter)
		} else if err := tt.Click("壱雫空"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if got := wait(t, asked, "the search"); got.query != "壱雫空" || a.query != "壱雫空" {
			t.Fatalf("keys %v: searched %+v, field %q", keys, got, a.query)
		}
		if !slices.Equal(a.settings.SearchHistory, []string{"壱雫空", "迷星叫"}) {
			t.Errorf("keys %v: history %q", keys, a.settings.SearchHistory)
		}
	}
}

// A click anywhere on the sidebar's search field focuses it: only the text
// in the middle took the click, not the icon, the padding or the edges.
func TestSearchFieldClickFocuses(t *testing.T) {
	for _, at := range []struct {
		name   string
		dx, dy float32 // from the field's center
	}{
		{"icon", -(sidebarWidth-28)/2 + 12, 0},
		{"end", (sidebarWidth-28)/2 - 4, 0},
		{"top", 0, -13},
		{"bottom", 0, 13},
	} {
		a := newTestApp()
		tt := ui.NewTester(a.view, 1200, 800)
		tt.Frame()
		r, ok := tt.Find("Search")
		if !ok {
			t.Fatal("no search field")
		}
		tt.ClickAt(sidebarWidth/2+at.dx, r.Y+r.H/2+at.dy)
		tt.Frame()
		tt.Type("迷星叫")
		tt.Frame()
		if a.query != "迷星叫" {
			t.Errorf("a click on the field's %s left it unfocused", at.name)
		}
	}
}

// The × in the search field empties it and takes Search Results out of the
// sidebar, leaving the results' page for Home, but not another page.
func TestClearSearch(t *testing.T) {
	for _, onPage := range []bool{true, false} {
		a := newTestApp()
		fakeSearch(a)
		tt := ui.NewTester(a.view, 1200, 800)
		tt.Frame()
		tt.Click("Search")
		tt.Type("迷星叫")
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if !onPage {
			a.open(route{page: pageLiked})
		}
		before := a.route
		tt.Frame()
		if err := tt.Click("Clear Search"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if tt.HasText("Search Results") || a.query != "" {
			t.Errorf("on page %v: the results stay in the sidebar, field %q", onPage, a.query)
		}
		want := before
		if onPage {
			want = route{page: pageHome, src: "fake"}
		}
		if a.route != want {
			t.Errorf("on page %v: showing %+v, want %+v", onPage, a.route, want)
		}
	}
}

// Clear Recent Searches in the empty field's popup forgets the searches
// before, and closes the popup without searching.
func TestClearRecentSearches(t *testing.T) {
	a := newTestApp()
	asked := fakeSearch(a)
	a.settings.SearchHistory = []string{"迷星叫", "壱雫空"}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Clear Recent Searches"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if len(a.settings.SearchHistory) != 0 {
		t.Errorf("history %q", a.settings.SearchHistory)
	}
	if tt.HasText("迷星叫") {
		t.Error("the popup still lists the searches before")
	}
	select {
	case got := <-asked:
		t.Errorf("clearing searched %+v", got)
	default:
	}
}

// With MM_FRAMES set, saves the search field's popup, with the searches
// before and with suggestions, light and dark.
func TestSearchPopupFrames(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, dark := range []bool{false, true} {
		for _, typed := range []bool{false, true} {
			a := newTestApp()
			ch := posts(a)
			a.settings.SearchHistory = []string{"迷星叫", "壱雫空", "a"}
			fake(a).suggest = func(q string) ([]string, error) { return []string{"迷跡波", "迷跡波 Live"}, nil }
			tt := ui.NewTester(a.view, 1000, 600)
			tt.SetDark(dark)
			tt.Frame()
			tt.Click("Search")
			if typed {
				tt.Type("迷跡")
				tt.Frame()
				runPost(t, ch)
			}
			tt.Advance(time.Second)
			tt.Frame()
			img := tt.Image()
			s := float32(img.Bounds().Dx()) / 1000
			crop := image.Rect(0, 0, int((sidebarWidth+80)*s), int(300*s))
			out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
			draw.Draw(out, out.Bounds(), img, crop.Min, draw.Src)
			name := fmt.Sprintf("search-dark%v-typed%v.png", dark, typed)
			saveFrame(t, dir, name, out)
		}
	}
}

// Typing asks for suggestions once it pauses, and a suggestion searches for
// what it says, remembering that.
func TestSuggestionSearches(t *testing.T) {
	asked := make(chan string, 4)
	a := newTestApp()
	ch := posts(a)
	fake(a).suggest = func(q string) ([]string, error) { asked <- q; return []string{"迷跡波 Live"}, nil }
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	tt.Click("Search")
	tt.Type("迷跡")
	tt.Frame()
	if q := wait(t, asked, "suggestions"); q != "迷跡" {
		t.Fatalf("suggestions for %q", q)
	}
	runPost(t, ch)
	tt.Frame()
	if err := tt.Click("迷跡波 Live"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.route.page != pageSearch || a.route.query != "迷跡波 Live" || !slices.Equal(a.settings.SearchHistory, []string{"迷跡波 Live"}) {
		t.Fatalf("opened %+v, history %q", a.route, a.settings.SearchHistory)
	}
}

// A click on an episode plays the show's episodes from it, and Now Playing
// shows what it is about in place of lyrics.
func TestPlayEpisode(t *testing.T) {
	a := newTestApp()
	ep := func(id, name string) music.Track {
		return music.Track{ID: id, Name: name, Duration: 600000, ShowID: "5", Description: name + "的简介"}
	}
	a.shows["5"] = &showData{
		show:     &music.Show{ID: "5", Name: "夜航", Publisher: "主播"},
		episodes: []music.Track{ep("1", "第三期"), ep("2", "第二期"), ep("3", "第一期")},
		page:     paged{loaded: true},
	}
	a.open(route{page: pageShow, id: "5"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("第一期"); err != nil {
		t.Fatal(err)
	}
	cur := a.pl.Current()
	if cur == nil || cur.ID != "3" || cur.ShowID != "5" || len(a.pl.Queue()) != 3 {
		t.Fatalf("playing %+v of %d", cur, len(a.pl.Queue()))
	}
	a.pl.SetLyrics(nil)
	tt.Advance(time.Second)
	tt.Frame()
	if err := tt.Click("About This Episode"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if !a.nowPlaying || a.npPane != paneShow || !tt.HasText("第一期的简介") {
		t.Fatalf("Now Playing open %v on pane %d", a.nowPlaying, a.npPane)
	}
}

// An episode's host in Now Playing opens its show's page, closing Now
// Playing, though that is the page it covers: it did nothing, as the page
// was open.
func TestEpisodeOpensShow(t *testing.T) {
	a := catalogApp()
	a.open(route{page: pageShow, id: "5"})
	d := a.shows["5"]
	a.playEpisodes(d)
	a.pl.SetLyrics(nil)
	a.nowPlaying, a.npPane = true, paneShow
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Advance(time.Second)
	tt.Frame()
	r, ok := tt.Find(d.show.Publisher)
	if !ok {
		t.Fatal("no host")
	}
	tt.ClickAt(r.X+4, r.Y+r.H/2)
	tt.Frame()
	if a.nowPlaying || a.route != (route{page: pageShow, src: "fake", id: "5"}) {
		t.Fatalf("Now Playing open %v over %+v", a.nowPlaying, a.route)
	}
}

// Who publishes a show, on its page, opens the page the source has of them.
func TestShowOpensPublisher(t *testing.T) {
	a := newTestApp()
	a.shows["5"] = &showData{
		show: &music.Show{ID: "5", Name: "夜航", Publisher: "主播", PublisherPage: "user:7", EpisodeCount: 3},
		page: paged{loaded: true},
	}
	a.open(route{page: pageShow, id: "5"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	r, ok := tt.Find("主播")
	if !ok {
		t.Fatal("no publisher")
	}
	tt.ClickAt(r.X+4, r.Y+r.H/2)
	tt.Frame()
	if want := (route{page: pageBrowse, src: "fake", id: "user:7"}); a.route != want {
		t.Fatalf("route %+v", a.route)
	}
}

// The sidebar's Podcasts opens the shows the listener subscribes to, which
// it asks of the source then.
func TestPodcastsPageLoads(t *testing.T) {
	asked := make(chan struct{}, 4)
	a := newTestApp()
	fake(a).shows = func() ([]music.Show, error) { asked <- struct{}{}; return nil, errors.New("offline") }
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Podcasts"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.route.page != pageShows {
		t.Fatalf("opened %+v", a.route)
	}
	wait(t, asked, "the shows")
}

// A show's card opens its page; the button on its cover plays its first
// episode instead, and leaves the page open.
func TestShowCard(t *testing.T) {
	a := catalogApp()
	a.open(route{page: pageShows})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	r, ok := tt.Find("夜航电台")
	if !ok {
		t.Fatal("no card")
	}
	// The button shows under the pointer, in the corner of the square
	// cover.
	x, y := r.X+r.W-29, r.Y+r.W-29
	tt.Move(x, y)
	tt.Advance(time.Second)
	tt.Frame()
	tt.ClickAt(x, y)
	tt.Frame()
	if cur := a.pl.Current(); cur == nil || cur.ID != "40" || a.route.page != pageShows {
		t.Fatalf("playing %+v on %+v", cur, a.route)
	}
	tt.ClickAt(r.X+r.W/2, r.Y+r.W/2)
	tt.Frame()
	if a.route != (route{page: pageShow, src: "fake", id: "5"}) {
		t.Fatalf("opened %+v", a.route)
	}
}

// Subscribe subscribes to a show at once and asks the source to, listing it
// with those subscribed once it took it; a change it refuses goes back.
func TestSubscribeShow(t *testing.T) {
	type call struct {
		id string
		on bool
	}
	calls := make(chan call, 4)
	var answer error
	a := catalogApp()
	ch := posts(a)
	fake(a).subscribe = func(id string, on bool) error { calls <- call{id, on}; return answer }
	without(a, "isLiked")
	a.account = &music.Account{ID: "1"}
	a.subscribed = nil
	a.open(route{page: pageShow, id: "5"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Subscribe"); err != nil {
		t.Fatal(err)
	}
	show := a.shows["5"].show
	if c := wait(t, calls, "the subscription"); c != (call{"5", true}) || !show.Subscribed {
		t.Fatalf("asked for %+v, subscribed %v", c, show.Subscribed)
	}
	runPost(t, ch)
	if len(a.subscribed) != 1 || a.subscribed[0].ID != "5" {
		t.Errorf("subscribed to %+v", a.subscribed)
	}
	tt.Frame()

	answer = errors.New("offline")
	if err := tt.Click("Subscribed"); err != nil {
		t.Fatal(err)
	}
	if c := wait(t, calls, "the end of it"); c != (call{"5", false}) || show.Subscribed {
		t.Fatalf("asked for %+v, subscribed %v", c, show.Subscribed)
	}
	runPost(t, ch)
	if !show.Subscribed || len(a.subscribed) != 1 {
		t.Errorf("a change the source refused stayed: subscribed %v, listed %+v", show.Subscribed, a.subscribed)
	}
}

// Subscribing signed out asks the listener to sign in, not the source.
func TestSubscribeSignedOut(t *testing.T) {
	a := catalogApp()
	fake(a).subscribe = func(string, bool) error { t.Error("asked the source"); return nil }
	a.open(route{page: pageShow, id: "5"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Subscribe"); err != nil {
		t.Fatal(err)
	}
	if !a.signIn.open || a.shows["5"].show.Subscribed {
		t.Errorf("dialog open %v, subscribed %v", a.signIn.open, a.shows["5"].show.Subscribed)
	}
}

// A song row's album opens its page, not playing the song as the row's
// click would.
func TestAlbumLinkInRow(t *testing.T) {
	a := newTestApp()
	tr := music.Track{ID: "2", Name: "起风了", Artists: []music.Artist{{ID: "13", Name: "丙"}}, Album: music.Album{ID: "8", Name: "迷跡波"}, Duration: 200000}
	a.artists["13"] = &artistData{page: &music.ArtistPage{Artist: music.Artist{ID: "13", Name: "丙"}, Tracks: []music.Track{tr}}}
	a.open(route{page: pageArtist, id: "13"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	// Its name, at the start of the column it fills.
	r, ok := tt.Find("迷跡波")
	if !ok {
		t.Fatal("no album")
	}
	tt.ClickAt(r.X+8, r.Y+r.H/2)
	tt.Frame()
	if a.route != (route{page: pageAlbum, src: "fake", id: "8"}) || a.pl.Current() != nil {
		t.Fatalf("opened %+v, playing %+v", a.route, a.pl.Current())
	}
}

// Save on an album saves it at once and asks the source to, and the list of
// those saved loads anew once it took it.
func TestSaveAlbum(t *testing.T) {
	asked := make(chan bool, 4)
	a := catalogApp()
	ch := posts(a)
	fake(a).saveAlbum = func(id string, on bool) error { asked <- on; return nil }
	without(a, "isLiked")
	a.account = &music.Account{ID: "1"}
	a.savedAlbums, a.savedAlbumsPaged = []music.Album{{ID: "20"}}, paged{loaded: true}
	a.open(route{page: pageAlbum, id: "8"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if !wait(t, asked, "the saving") || !a.albums["8"].album.Album.Saved {
		t.Fatal("not saved")
	}
	runPost(t, ch)
	if a.savedAlbums != nil || a.savedAlbumsPaged.loaded {
		t.Errorf("the albums saved stayed as they were: %+v", a.savedAlbums)
	}
}

// catalogApp is an app with an artist, album and show loaded, and searches
// for each kind, to look at.
func catalogApp() *app {
	a := newTestApp()
	band := music.Artist{ID: "7", Name: "MyGO!!!!!"}
	al := music.Album{ID: "8", Name: "迷跡波"}
	var songs []music.Track
	for i, name := range []string{"詩超絆", "迷星叫", "壱雫空", "碧天伴走", "音一会", "栞"} {
		songs = append(songs, music.Track{ID: fmt.Sprint(i + 1), Name: name, Artists: []music.Artist{band}, Album: al, Duration: int64(210000 + 16000*i)})
	}
	var albums []music.Album
	for i, name := range []string{"迷跡波", "跡暖空", "壱雫空", "音一会", "迷星叫", "焚音打"} {
		albums = append(albums, music.Album{ID: fmt.Sprint(20 + i), Name: name, Artists: []music.Artist{band}, Released: "2026-10-01"})
	}
	a.artists["7"] = &artistData{page: &music.ArtistPage{Artist: band, Tracks: songs, Albums: albums}}
	a.albums["8"] = &albumData{album: &music.AlbumPage{Album: music.Album{ID: "8", Name: "迷跡波", Artists: []music.Artist{band}, Released: "2026-10-01"}, Tracks: songs}}
	show := &music.Show{ID: "5", Name: "夜航电台", Publisher: "主播", EpisodeCount: 120, Description: "每晚陪你说说话。"}
	var episodes []music.Track
	for i := range 4 {
		episodes = append(episodes, music.Track{
			ID: fmt.Sprint(40 + i), Name: fmt.Sprintf("第 %d 期：晚安", 120-i), Duration: 1800000, ShowID: "5",
			Artists: []music.Artist{{Name: show.Publisher}}, Album: music.Album{Name: show.Name}, Published: "2026-10-01",
		})
	}
	a.shows["5"] = &showData{show: show, episodes: episodes, page: paged{loaded: true}}
	a.subscribed, a.subscribedOnce = []music.Show{*show}, true
	for _, res := range []struct {
		kind music.SearchKind
		res  music.SearchResult
	}{
		{music.SearchTracks, music.SearchResult{Tracks: songs}},
		{music.SearchArtists, music.SearchResult{Artists: []music.Artist{band, {ID: "10", Name: "Ave Mujica"}}}},
		{music.SearchAlbums, music.SearchResult{Albums: albums}},
		{music.SearchShows, music.SearchResult{Shows: []music.Show{*show}}},
	} {
		a.searches[route{page: pageSearch, src: "fake", query: "MyGO", kind: res.kind}] = &searchData{res: res.res, page: paged{loaded: true}}
	}
	return a
}

// catalogScenes are the pages of catalogApp.
var catalogScenes = []struct {
	name string
	r    route
	tab  int
}{
	{"search-songs", route{page: pageSearch, query: "MyGO", kind: music.SearchTracks}, 0},
	{"search-artists", route{page: pageSearch, query: "MyGO", kind: music.SearchArtists}, 0},
	{"search-albums", route{page: pageSearch, query: "MyGO", kind: music.SearchAlbums}, 0},
	{"search-shows", route{page: pageSearch, query: "MyGO", kind: music.SearchShows}, 0},
	{"artist", route{page: pageArtist, id: "7"}, artistSongs},
	{"artist-albums", route{page: pageArtist, id: "7"}, artistAlbums},
	{"album", route{page: pageAlbum, id: "8"}, 0},
	{"show", route{page: pageShow, id: "5"}, 0},
	{"shows", route{page: pageShows}, 0},
}

// With MM_FRAMES set, saves the pages of searches, artists, albums and shows
// in the light and the dark.
func TestCatalogFrames(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, s := range catalogScenes {
		for _, dark := range []bool{false, true} {
			a := catalogApp()
			a.artists["7"].tab = s.tab
			a.open(s.r)
			tt := ui.NewTester(a.view, 1200, 800)
			tt.SetDark(dark)
			tt.Advance(time.Second)
			tt.Frame()
			name := s.name
			if dark {
				name += "-dark"
			}
			saveFrame(t, dir, "catalog-"+name+".png", tt.Image())
		}
	}
}

// The pages of searches, artists, albums and shows ask for no frames once
// shown.
func TestCatalogFrameBudget(t *testing.T) {
	for _, s := range catalogScenes {
		a := catalogApp()
		a.artists["7"].tab = s.tab
		a.open(s.r)
		tt := ui.NewTester(a.view, 1200, 800)
		tt.Advance(time.Second)
		tt.Frame()
		tt.Advance(time.Second)
		if tt.FramePending() {
			t.Errorf("%s asks for frames while idle", s.name)
		}
	}
}
