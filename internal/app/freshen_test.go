package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// Opening a playlist again, a while after it loaded, shows it as it was and
// then as the source has it now: songs added on its website show up
// (issue #9).
func TestReopenedPlaylistLoadsAnew(t *testing.T) {
	a := editTestApp()
	ch := posts(a)
	a.plAt["10"] = time.Now().Add(-time.Minute)
	asked := make(chan string, 4)
	fake(a).playlist = func(id string) (*music.Playlist, error) {
		asked <- id
		return &music.Playlist{ID: id, Name: "通勤", Mine: true, Tracks: []music.Track{
			song("1", "一张相片", "林宥嘉"),
			song("2", "宝船", "DOUDOU"),
			song("3", "新歌", "某人"),
		}}, nil
	}
	a.route = route{page: pageHome, src: "fake"}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	if err := tt.Click("通勤"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("宝船"); !ok {
		t.Fatal("the playlist didn't show as it was while it loads anew")
	}
	if id := <-asked; id != "10" {
		t.Fatalf("asked for playlist %q", id)
	}
	runPost(t, ch)
	tt.Frame()
	if _, ok := tt.Find("新歌"); !ok {
		t.Error("the song added elsewhere didn't show")
	}

	// Opened again at once, it isn't asked for again.
	if err := tt.Click("睡前"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	<-asked
	runPost(t, ch)
	if err := tt.Click("通勤"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	select {
	case id := <-asked:
		t.Errorf("playlist %q was asked for again right after it loaded", id)
	case <-time.After(50 * time.Millisecond):
	}
}

// Coming back to the window asks the source again for the listener's
// playlists and the one showing, so that what changed elsewhere shows.
func TestRefocusLoadsPlaylistsAnew(t *testing.T) {
	a := editTestApp()
	ch := posts(a)
	a.mineAt, a.plAt["10"] = time.Now(), time.Now()
	fake(a).playlists = func() ([]music.Playlist, error) {
		return []music.Playlist{
			{ID: "10", Name: "通勤", Mine: true},
			{ID: "12", Name: "网站上新建", Mine: true},
		}, nil
	}
	fake(a).playlist = func(id string) (*music.Playlist, error) {
		return &music.Playlist{ID: id, Name: "通勤", Mine: true, Tracks: []music.Track{song("3", "新歌", "某人")}}, nil
	}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	tt.SetWindowActive(false)

	a.mineAt, a.plAt["10"] = time.Now().Add(-time.Minute), time.Now().Add(-time.Minute)
	tt.SetWindowActive(true)
	runPosts(ch)
	tt.Frame()
	if _, ok := tt.Find("网站上新建"); !ok {
		t.Error("the playlist made elsewhere didn't show in the sidebar")
	}
	if _, ok := tt.Find("新歌"); !ok {
		t.Error("the playlist showing didn't load anew")
	}
}

// A playlist's songs loaded anew don't undo the app's own change to them
// made meanwhile, which the source's answer may be from before of.
func TestFreshPlaylistKeepsChangeMadeMeanwhile(t *testing.T) {
	a := editTestApp()
	ch := posts(a)
	a.plAt["10"] = time.Now().Add(-time.Minute)
	fake(a).playlist = func(id string) (*music.Playlist, error) {
		return &music.Playlist{ID: id, Name: "通勤", Mine: true, Tracks: []music.Track{
			song("1", "一张相片", "林宥嘉"),
			song("2", "宝船", "DOUDOU"),
		}}, nil
	}
	fake(a).removeFrom = func(string, []string) error { return nil }
	a.loadPlaylist(a.library, "10", false)
	tr := a.playlists["10"].Tracks[1]
	a.removeFrom(a.library, "10", &tr)
	// Whichever answers first, the song stays out.
	runPost(t, ch)
	runPost(t, ch)
	if got := len(a.playlists["10"].Tracks); got != 1 {
		t.Errorf("the playlist has %d songs, want the one left", got)
	}
}

// Liked Songs opened again loads anew: a song liked elsewhere shows, and one
// unliked elsewhere goes, its heart asked about again.
func TestReopenedLikedLoadsAnew(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	lib := a.library
	lib.likedList = []music.Track{song("1", "一张相片", "林宥嘉"), song("2", "宝船", "DOUDOU")}
	lib.likedPaged.loaded = true
	lib.liked = map[string]bool{"1": true, "2": true}
	lib.likedAt = time.Now().Add(-time.Minute)
	fake(a).liked = func(offset int) (*music.Page, error) {
		return &music.Page{Tracks: []music.Track{song("3", "新歌", "某人"), song("1", "一张相片", "林宥嘉")}}, nil
	}
	a.open(route{page: pageLiked, src: "fake"})
	runPost(t, ch)
	if len(lib.likedList) != 2 || lib.likedList[0].ID != "3" {
		t.Errorf("liked songs are %v, want the new list", lib.likedList)
	}
	if _, known := lib.liked["2"]; known {
		t.Error("the song unliked elsewhere still shows liked")
	}
	if !lib.liked["3"] {
		t.Error("the song liked elsewhere doesn't show liked")
	}
}

// runPosts runs what's posted until nothing more comes for a while.
func runPosts(ch chan func()) {
	for {
		select {
		case fn := <-ch:
			fn()
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}
