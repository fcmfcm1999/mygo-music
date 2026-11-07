package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// Now Playing closes as one layer, covering the page and the sidebar alike on
// its way out, and is gone once it has.
func TestNowPlayingClosesAsOneLayer(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]music.Track{song("1", "宝船", "DOUDOU")})
	a.pl.SetLyrics(nil)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetDark(false)
	a.nowPlaying = true
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if !tt.HasText("Up Next") {
		t.Fatal("now playing did not open")
	}
	// Mid-way out, it still covers the page and the sidebar alike.
	a.nowPlaying = false
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	tt.Frame()
	img := tt.Image()
	sx, sy := img.Bounds().Dx(), img.Bounds().Dy()
	dark := func(x, y float32) bool {
		r, g, b, _ := img.At(int(x*float32(sx)/1200), int(y*float32(sy)/800)).RGBA()
		return (r+g+b)/3 < 0x9000
	}
	if !dark(900, 400) || !dark(100, 400) {
		t.Errorf("page %v, sidebar %v: the closing layer should cover both", dark(900, 400), dark(100, 400))
	}
	tt.Advance(2 * time.Second)
	tt.Frame()
	if tt.HasText("Up Next") {
		t.Error("now playing still shows after closing")
	}
}

// A click on a song of the queue plays it, the songs before it staying listed.
func TestQueueStaysAsATrackIsPlayed(t *testing.T) {
	a := newTestApp()
	var tracks []music.Track
	for i := range 12 {
		tracks = append(tracks, song(fmt.Sprint(i+1), fmt.Sprintf("歌%02d", i)))
	}
	a.pl.Cue(tracks)
	a.nowPlaying, a.npPane = true, paneQueue
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("歌05"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if got := a.pl.Current().Name; got != "歌05" {
		t.Fatalf("playing %q, want 歌05", got)
	}
	// The tracks before it are still listed, as they were.
	for _, s := range []string{"歌01", "歌04", "歌06", "歌09"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q in %q", s, tt.Texts())
		}
	}
}

// The app chooses Now Playing's pane before it makes the player: it
// crashed on start when the choice asked the player what played.
func TestLyricsPaneBeforePlayer(t *testing.T) {
	a := &app{settings: settings{LyricsStage: true}}
	if pane := a.lyricsPaneChosen(); pane != paneStage {
		t.Errorf("pane %d", pane)
	}
}

// Previous plays the song before, however far this one has played: a few
// seconds in, it went back to this one's start, and took a second click.
func TestPrevGoesBackASong(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]music.Track{song("1", "一"), song("2", "二")})
	a.pl.SetLyrics(nil)
	a.pl.Hold(30 * time.Second)
	a.nowPlaying = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if err := tt.Click("Previous"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if id := a.pl.Current().ID; id != "2" {
		t.Errorf("playing %s, want the one before, 2", id)
	}
}

// Now Playing holds still as the quality's badge goes, while the next song
// loads, and comes back: the row of times grew with it, moving the column.
func TestNowPlayingHoldsStillWithoutBadge(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]music.Track{song("1", "一")})
	a.pl.SetLyrics(nil)
	a.nowPlaying = true
	tt := ui.NewTester(a.view, 1200, 800)
	var at []float32
	for _, st := range []*music.Stream{{Quality: "lossless"}, nil} {
		a.pl.SetStream(st)
		tt.Frame()
		tt.Advance(time.Second)
		tt.Frame()
		r, ok := tt.Find("Album Cover")
		if !ok {
			t.Fatal("no cover")
		}
		at = append(at, r.Y)
	}
	if at[0] != at[1] {
		t.Errorf("the cover moved from %v to %v as the badge went", at[0], at[1])
	}
}

// ⌘ and a number choose Now Playing's tab in that place, for a song and
// for an episode, whose tabs are others: its last is the comments.
func TestCmdNumberChoosesTab(t *testing.T) {
	for _, c := range []struct {
		track music.Track
		keys  []int // from 1, 0 for the last tab
		panes []int
	}{
		{song("1", "宝船"), []int{4, 2, 1}, []int{paneWall, paneStage, paneLyrics}},
		{music.Track{ID: "2", Name: "节目", ShowID: "9", Duration: 200000}, []int{0, 1}, []int{paneComments, paneShow}},
	} {
		a := newTestApp()
		a.pl.Cue([]music.Track{c.track})
		a.pl.SetLyrics(nil)
		a.nowPlaying = true
		tt := ui.NewTester(a.view, 1200, 800)
		tt.Frame()
		for i, n := range c.keys {
			if n == 0 {
				n = len(a.npTabs())
			}
			tt.Key(ui.Cmd, ui.Key1+ui.Key(n-1))
			tt.Frame()
			if a.npPane != c.panes[i] {
				t.Errorf("%s: ⌘%d chose pane %d, want %d", c.track.Name, n, a.npPane, c.panes[i])
			}
		}
	}
}

// Now Playing's tabs stay where they are going from one pane to another,
// and as the comments' count changes: the lyrics, the queue and the
// comments had them over the pane, and the stage and the wall at the top
// on the right, where a longer count pushed the others along.
func TestNowPlayingTabsStayPut(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]music.Track{song("1", "一")})
	a.pl.SetLyrics(nil)
	a.nowPlaying = true
	tt := ui.NewTester(a.view, 1200, 800)
	settle := func() {
		tt.Frame()
		tt.Advance(time.Second)
		tt.Frame()
	}
	settle()
	first, ok := tt.Find("Lyrics")
	if !ok {
		t.Fatal("no Lyrics tab")
	}
	for _, tab := range []string{"Stage", "Up Next", "Wall", "Lyrics"} {
		tt.Click(tab)
		settle()
		if a.npPane == paneLyrics && tab != "Lyrics" {
			t.Fatalf("%s was not chosen", tab)
		}
		if r, _ := tt.Find("Lyrics"); r.X != first.X || r.Y != first.Y {
			t.Errorf("on %s the Lyrics tab is at %v,%v, was at %v,%v", tab, r.X, r.Y, first.X, first.Y)
		}
	}
	for _, pane := range []int{paneStage, paneLyrics} {
		a.npPane = pane
		a.comments.loaded, a.comments.total = true, 0
		settle()
		a.comments.total = 12345678
		settle()
		if r, _ := tt.Find("Lyrics"); r.X != first.X || r.Y != first.Y {
			t.Errorf("in pane %d, with the count, the Lyrics tab is at %v,%v, was at %v,%v", pane, r.X, r.Y, first.X, first.Y)
		}
	}
}

// pullNowPlaying opens Now Playing on the queue and pulls it down from the
// gap between the cover and the queue, by dy over steps moves 16 ms apart,
// and lets go after rest: the pointer still for that long, it throws
// nothing.
func pullNowPlaying(t *testing.T, dy float32, steps int, rest time.Duration) (*app, *ui.Tester) {
	t.Helper()
	a := newTestApp()
	a.pl.Cue([]music.Track{song("1", "宝船", "DOUDOU")})
	a.pl.SetLyrics(nil)
	a.nowPlaying, a.npPane = true, paneQueue
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Advance(time.Second)
	x, y := float32(450), float32(300)
	tt.Press(x, y)
	tt.Frame()
	for i := 1; i <= steps; i++ {
		tt.Advance(16 * time.Millisecond)
		tt.Move(x, y+dy*float32(i)/float32(steps))
		tt.Frame()
	}
	tt.Advance(rest)
	tt.Release(x, y+dy)
	tt.Frame()
	tt.Advance(time.Second)
	return a, tt
}

// Pulled down far and let go, Now Playing closes.
func TestPullNowPlayingDownCloses(t *testing.T) {
	a, tt := pullNowPlaying(t, 320, 12, 300*time.Millisecond)
	if a.nowPlaying || tt.HasText("Up Next") {
		t.Error("still open after a long pull")
	}
}

// A short, slow pull springs back, Now Playing still open and at rest.
func TestShortPullSpringsBack(t *testing.T) {
	a, tt := pullNowPlaying(t, 60, 6, 300*time.Millisecond)
	if !a.nowPlaying || !tt.HasText("Up Next") {
		t.Fatal("closed after a short pull")
	}
	if a.npShift != 0 || a.npPull != 0 {
		t.Errorf("not back in place: shift %v, pull %v", a.npShift, a.npPull)
	}
}

// A flick down closes Now Playing, though it went only a little way.
func TestFlickNowPlayingDownCloses(t *testing.T) {
	a, _ := pullNowPlaying(t, 80, 3, 0)
	if a.nowPlaying {
		t.Error("still open after a flick")
	}
}

// The bar's cover shows in every frame as Now Playing shrinks back into
// it: in the frame its cover got there, Now Playing was gone and the bar
// still hid its own, which blinked.
func TestClosingCoverLandsWithoutABlink(t *testing.T) {
	a := newTestApp()
	a.settings.GrowFromCover = true
	a.pl.Cue(framesTracks(a))
	a.pl.SetLyrics(nil)
	a.nowPlaying, a.npPane = true, paneQueue
	tt := ui.NewTester(a.view, 1180, 780)
	tt.Advance(time.Second)
	a.nowPlaying = false
	tt.Frame()
	// A millisecond at a time, to look at every frame.
	for i := range 1500 {
		tt.Advance(time.Millisecond)
		if a.npT > 0.05 {
			continue // not near the bar yet
		}
		img := tt.Image()
		sc := float32(img.Bounds().Dx()) / 1180
		at := a.npFromRect(1180, 780)
		// The cover is colored, where the bar is gray, light or dark.
		r, g, b, _ := img.At(int((at.X+at.W/2)*sc), int((at.Y+at.H/2)*sc)).RGBA()
		if max(r, g, b)-min(r, g, b) < 0x2000 {
			t.Fatalf("at %d ms: no cover where the bar's is", i)
		}
	}
}

// Now Playing slides up from below the window as it opens, as it is,
// rather than fading in, unless it grows out of the bar's cover, which
// leaves the top of the window alone at first.
func TestNowPlayingSlidesUpUnlessItGrows(t *testing.T) {
	// opening is the window's top right and bottom right corners, before and
	// a little way into opening, and how far below its place the queue's
	// title is then.
	opening := func(grow bool) (before, during [2][3]uint32, below float32) {
		a := newTestApp()
		a.settings.GrowFromCover = grow
		a.pl.Cue([]music.Track{song("1", "宝船", "DOUDOU")})
		a.pl.SetLyrics(nil)
		a.npPane = paneQueue
		tt := ui.NewTester(a.view, 1200, 800)
		tt.SetDark(false)
		tt.Frame()
		at := func() (px [2][3]uint32) {
			img := tt.Image()
			sc := float32(img.Bounds().Dx()) / 1200
			for i, y := range []float32{60, 780} {
				r, g, b, _ := img.At(int(1100*sc), int(y*sc)).RGBA()
				px[i] = [3]uint32{r, g, b}
			}
			return px
		}
		before = at()
		a.nowPlaying = true
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
		tt.Advance(60 * time.Millisecond)
		tt.Frame()
		during = at()
		mid, _ := tt.Find("Up Next")
		tt.Advance(time.Second)
		tt.Frame()
		rest, ok := tt.Find("Up Next")
		if !ok {
			t.Fatal("now playing did not open")
		}
		return before, during, mid.Y - rest.Y
	}
	if before, during, _ := opening(true); before[0] != during[0] {
		t.Fatalf("growing, the top right changed at once: %v to %v", before[0], during[0])
	}
	before, during, below := opening(false)
	// Its backdrop is dark, where the bar under it is light: half faded
	// in, it would be between the two.
	light := func(px [3]uint32) uint32 { return (px[0] + px[1] + px[2]) / 3 }
	if light(during[1]) > light(before[1])/2 {
		t.Errorf("sliding, the bottom right went from %v to only %v: not covered, or not yet all there", before[1], during[1])
	}
	if below < 100 {
		t.Errorf("sliding, the queue was only %v below its place", below)
	}
}
