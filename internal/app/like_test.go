package app

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/lyrics"
	"mygo-music/internal/music"
)

// The player bar's heart likes the song at once and asks the source to, and
// again unlikes it.
func TestLikeFromPlayerBar(t *testing.T) {
	calls := make(chan bool, 4)
	a := newTestApp()
	fake(a).like = func(id string, like bool) error { calls <- like; return nil }
	a.account = &music.Account{ID: "1", Name: "小明"}
	a.pl.Cue([]music.Track{song("7", "宝船", "DOUDOU")})
	a.pl.SetLyrics(nil)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Advance(time.Second)
	tt.Frame()

	if err := tt.Click("Like"); err != nil {
		t.Fatal(err)
	}
	if !a.liked["7"] {
		t.Fatal("the heart did not fill at once")
	}
	if like := wait(t, calls, "the like"); !like {
		t.Error("asked the source to unlike")
	}
	tt.Frame()
	if err := tt.Click("Unlike"); err != nil {
		t.Fatal(err)
	}
	if a.liked["7"] {
		t.Error("still liked after unliking")
	}
	if like := wait(t, calls, "the unlike"); like {
		t.Error("asked the source to like again")
	}
}

// The heart of an episode playing likes the episode, once the source was
// asked, once, whether the listener likes it already.
func TestLikeEpisode(t *testing.T) {
	asked := make(chan []string, 4)
	type call struct {
		id   string
		like bool
	}
	calls := make(chan call, 4)
	a := newTestApp()
	fake(a).isLiked = func(ids []string) (map[string]bool, error) { asked <- ids; return nil, nil }
	fake(a).like = func(string, bool) error { t.Error("liked the episode as a song"); return nil }
	fake(a).likeEpisode = func(id string, like bool) error { calls <- call{id, like}; return nil }
	a.account = &music.Account{ID: "1"}
	a.pl.Cue([]music.Track{{ID: "13", Name: "第一期", ShowID: "5", Duration: 600000}})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Advance(time.Second)
	tt.Frame()
	if ids := wait(t, asked, "whether the episode is liked"); !slices.Equal(ids, []string{"13"}) {
		t.Fatalf("asked of %v", ids)
	}
	if err := tt.Click("Like"); err != nil {
		t.Fatal(err)
	}
	if !a.liked["13"] {
		t.Fatal("the heart did not fill at once")
	}
	if c := wait(t, calls, "the like"); c != (call{"13", true}) {
		t.Errorf("asked the source for %+v", c)
	}
	tt.Frame()
	if err := tt.Click("Unlike"); err != nil {
		t.Fatal(err)
	}
	if c := wait(t, calls, "the unlike"); c != (call{"13", false}) || a.liked["13"] {
		t.Errorf("asked the source for %+v, liked %v", c, a.liked["13"])
	}
	if len(asked) > 0 {
		t.Error("asked again whether the episode is liked")
	}
}

// An episode of a source that likes songs but not episodes has no heart, and
// ⌘D leaves it be: a service's liked songs may refuse episodes, so every like failed.
func TestNoLikeForEpisodeSourceCantLike(t *testing.T) {
	a := newTestApp()
	without(a, "likeEpisode")
	fake(a).like = func(string, bool) error { t.Error("liked the episode as a song"); return nil }
	a.account = &music.Account{ID: "1"}
	a.pl.Cue([]music.Track{{ID: "13", Name: "第一期", ShowID: "5", Duration: 600000}})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Advance(time.Second)
	tt.Frame()
	if err := tt.Click("Like"); err == nil {
		t.Error("the episode has a heart")
	}
	a.likeCurrent()
	if a.liked["13"] || a.toast != "" {
		t.Errorf("liked %v, toast %q", a.liked["13"], a.toast)
	}
}

// A heart clicked while the source still answers whether the listener likes
// the song keeps what the click made it.
func TestLikeClickBeatsAnswer(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	fake(a).isLiked = func([]string) (map[string]bool, error) { return map[string]bool{"4": true}, nil }
	a.account = &music.Account{ID: "1"}
	a.liked = map[string]bool{}
	a.askLiked(a.library, []string{"3", "4"})
	a.toggleLike(&music.Track{ID: "3"})
	runPost(t, ch)
	runPost(t, ch)
	if !a.liked["3"] || !a.liked["4"] {
		t.Fatalf("liked %v", a.liked)
	}
}

// An answer about the last listener's likes is dropped once they signed out: it
// crashed, writing to the likes signing out forgot.
func TestLikeAnswerAfterSignOut(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	fake(a).isLiked = func([]string) (map[string]bool, error) { return map[string]bool{"5": true}, nil }
	a.account = &music.Account{ID: "1"}
	a.liked = map[string]bool{}
	a.askLiked(a.library, []string{"5"})
	a.signOut(a.library)
	// The answer, the playlists and Home.
	for range 3 {
		runPost(t, ch)
	}
	if a.liked["5"] {
		t.Error("took the last listener's answer")
	}
}

// An answer about the last listener's likes doesn't count for the next, who
// signed in before it came.
func TestLikeAnswerForTheNextListener(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	fake(a).isLiked = func([]string) (map[string]bool, error) { return map[string]bool{"6": true}, nil }
	a.account = &music.Account{ID: "1"}
	a.liked = map[string]bool{}
	a.askLiked(a.library, []string{"6"})
	a.account = &music.Account{ID: "2"}
	a.liked = map[string]bool{}
	runPost(t, ch)
	if a.liked["6"] {
		t.Error("took the last listener's answer for the next one")
	}
}

// A heart the source refuses after the listener signed out goes nowhere: it
// crashed, putting it back in the likes signing out forgot.
func TestLikeRefusedAfterSignOut(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	fake(a).like = func(string, bool) error { return errors.New("offline") }
	a.account = &music.Account{ID: "1"}
	a.toggleLike(&music.Track{ID: "3"})
	a.signOut(a.library)
	// The refusal, the playlists and Home.
	for range 3 {
		runPost(t, ch)
	}
	if a.liked["3"] {
		t.Error("the heart stayed")
	}
}

// A heart the source refuses goes back, and says so.
func TestLikeGoesBack(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	without(a, "isLiked")
	fake(a).like = func(string, bool) error { return errors.New("offline") }
	a.account = &music.Account{ID: "1"}
	a.pl.Cue([]music.Track{song("7", "宝船")})
	a.pl.SetLyrics(nil)
	tt := ui.NewTester(a.view, 1200, 800)
	settled(tt)
	if err := tt.Click("Like"); err != nil {
		t.Fatal(err)
	}
	if !a.liked["7"] {
		t.Fatal("the heart did not fill at once")
	}
	runPost(t, ch)
	if a.liked["7"] || a.toast == "" {
		t.Errorf("liked %v, toast %q", a.liked["7"], a.toast)
	}
}

// On a show's page, a liked episode's heart shows, and another's as the
// pointer is on it, which likes that one.
func TestLikeEpisodeRow(t *testing.T) {
	liked := make(chan string, 4)
	a := catalogApp()
	fake(a).likeEpisode = func(id string, like bool) error { liked <- id; return nil }
	a.account = &music.Account{ID: "1"}
	a.liked = map[string]bool{"40": true}
	a.open(route{page: pageShow, id: "5"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if _, ok := tt.Find("Unlike"); !ok {
		t.Fatal("the liked episode shows no heart")
	}
	if _, ok := tt.Find("Like"); ok {
		t.Fatal("a heart shows with the pointer on no episode")
	}
	r, _ := tt.Find(a.shows["5"].episodes[2].Name)
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("Like"); err != nil {
		t.Fatal(err)
	}
	if id := wait(t, liked, "the like"); id != "42" || !a.liked["42"] {
		t.Errorf("liked %s", id)
	}
	if a.pl.Current() != nil {
		t.Error("the heart played the episode")
	}
}

// ⌘D likes the song playing, and again unlikes it, saying so in a toast.
func TestLikeCurrent(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]music.Track{song("7", "宝船")})
	a.account = &music.Account{ID: "1"}
	a.likeCurrent()
	liking := a.toast
	if !a.liked["7"] || liking == "" {
		t.Fatalf("liked %v, toast %q", a.liked["7"], liking)
	}
	a.likeCurrent()
	if a.liked["7"] || a.toast == "" || a.toast == liking {
		t.Fatalf("unliked: liked %v, toast %q", a.liked["7"], a.toast)
	}
}

// The liked songs load a page at a time from where the last ended, and a song
// unliked leaves them once its source took it.
func TestLikedPagePaging(t *testing.T) {
	asked := make(chan int, 4)
	a := newTestApp()
	ch := posts(a)
	f := fake(a)
	f.liked = func(offset int) (*music.Page, error) {
		asked <- offset
		if offset == 0 {
			return &music.Page{Tracks: []music.Track{song("1", "一"), song("2", "二"), song("3", "三")}, More: true}, nil
		}
		return &music.Page{Tracks: []music.Track{song("4", "四")}}, nil
	}
	a.account = &music.Account{ID: "1"}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Liked Songs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := wait(t, asked, "the first page"); got != 0 {
		t.Fatalf("asked from %d", got)
	}
	runPost(t, ch)
	tt.Frame()
	tt.Frame()
	if got := wait(t, asked, "the next page"); got != 3 {
		t.Fatalf("asked from %d", got)
	}
	runPost(t, ch)
	tt.Frame()
	tt.Frame()
	if len(a.likedList) != 4 || !a.liked["4"] || len(asked) > 0 {
		t.Fatalf("listed %d, liked %v, %d more asked", len(a.likedList), a.liked, len(asked))
	}
	if _, ok := tt.Find("四"); !ok {
		t.Error("the last page's song isn't listed")
	}

	a.toggleLike(&a.likedList[0])
	runPost(t, ch)
	tt.Frame()
	if _, ok := tt.Find("一"); ok || len(a.likedList) != 3 {
		t.Errorf("the song unliked stayed: %d listed", len(a.likedList))
	}
}

// With MM_FRAMES set to a directory, saves the like burst at moments after
// the click there, side by side, to look at.
func TestLikeBurstFrames(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	a := newTestApp()
	a.account = &music.Account{ID: "1"}
	a.pl.Cue([]music.Track{song("7", "宝船")})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetDark(true)
	tt.Advance(time.Second)
	tt.Frame()
	r, ok := tt.Find("Like")
	if !ok {
		t.Fatal("no heart")
	}
	tt.Click("Like")
	moments := []time.Duration{0, 60, 120, 200, 300, 420, 600}
	img := tt.Image()
	scale := float32(img.Bounds().Dx()) / 1200
	cell := int(90 * scale)
	strip := image.NewRGBA(image.Rect(0, 0, cell*len(moments), cell))
	at := time.Duration(0)
	for i, m := range moments {
		tt.Advance(m*time.Millisecond - at)
		at = m * time.Millisecond
		tt.Frame()
		img := tt.Image()
		cx, cy := int((r.X+r.W/2)*scale), int((r.Y+r.H/2)*scale)
		draw.Draw(strip, image.Rect(i*cell, 0, (i+1)*cell, cell), img, image.Pt(cx-cell/2, cy-cell/2), draw.Src)
	}
	saveFrame(t, dir, "burst.png", strip)
}

// With MM_FRAMES set, saves the lyrics stage in each of its styles at
// moments through a line.
func TestStageFrames(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, s := range stageStyles {
		name := "stage.png"
		if s.id != "" {
			name = "stage-" + s.id + ".png"
		}
		stageFrames(t, s.id, filepath.Join(dir, name))
	}
}

func stageFrames(t *testing.T, style, path string) {
	a := newTestApp()
	a.settings.StageStyle = style
	a.pl.Cue([]music.Track{song("7", "海屿你", "马也_Crabbit")})
	a.pl.SetLyrics(lyrics.FromSource(&music.Lyrics{Lines: []music.LyricLine{
		{Start: 13010, End: 17790, Translation: "I never show weakness first", Words: []music.LyricWord{
			{Start: 13010, End: 13670, Text: "从"}, {Start: 13670, End: 14340, Text: "不 "},
			{Start: 14370, End: 14850, Text: "主"}, {Start: 14850, End: 15460, Text: "动 "},
			{Start: 15490, End: 15820, Text: "示"}, {Start: 15820, End: 16890, Text: "弱"},
		}},
		{Start: 17790, End: 19560, Words: []music.LyricWord{
			{Start: 17790, End: 17960, Text: "我"}, {Start: 17960, End: 18410, Text: "们"},
			{Start: 18410, End: 18700, Text: "的"}, {Start: 18700, End: 19170, Text: "过"}, {Start: 19170, End: 19560, Text: "去"},
		}},
		{Start: 30560, End: 37140, Text: "曾经的那些发生过的开心和难过", Translation: "The joy and sorrow that once happened"},
	}}))
	a.nowPlaying, a.npPane = true, paneStage
	tt := ui.NewTester(a.view, 1200, 800)
	tt.SetDark(true)
	tt.Advance(time.Second)
	moments := []time.Duration{12900, 13300, 13800, 14600, 15900, 17300, 17900, 18500, 26000}
	img := tt.Image()
	cw, ch := img.Bounds().Dx()/3, img.Bounds().Dy()/3
	grid := image.NewRGBA(image.Rect(0, 0, cw*3, ch*3))
	for i, m := range moments {
		a.pl.Seek(m * time.Millisecond)
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
		shot := tt.Image()
		small := image.NewRGBA(image.Rect(0, 0, cw, ch))
		for y := range ch {
			for x := range cw {
				small.Set(x, y, shot.At(x*3, y*3))
			}
		}
		draw.Draw(grid, image.Rect((i%3)*cw, (i/3)*ch, (i%3+1)*cw, (i/3+1)*ch), small, image.Point{}, draw.Src)
	}
	saveFrame(t, filepath.Dir(path), filepath.Base(path), grid)
}

// coveredSong is a song whose cover is ready, for a look at what its colors
// do.
func coveredSong(a *app) music.Track {
	tr := song("7", "Northern Lights", "Solveig")
	tr.Album = music.Album{ID: "8", Name: "Polar Night", ImageURL: "cover:polar"}
	giveCover(a, tr.Album.ImageURL, coverImage(ui.RGB(40, 90, 170), ui.RGB(220, 120, 160)))
	return tr
}

// With MM_FRAMES set, saves Now Playing with a cover, to look at its backdrop.
func TestBackdropFrame(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	a := newTestApp()
	a.pl.Cue([]music.Track{coveredSong(a)})
	a.pl.SetLyrics(lyrics.Build("[00:00.00]Written by Solveig\n[00:15.00]The sky goes green at the edges\n[00:20.00]And everyone stops to look\n[00:25.00]Nobody says a word\n[00:30.00]Nobody needs to",
		"[00:15.00]天边泛起了绿光\n[00:20.00]所有人都停下来看\n[00:25.00]没有人说话\n[00:30.00]也不需要说话"))
	a.pl.Seek(2500 * time.Millisecond)
	a.nowPlaying = true
	tt := ui.NewTester(a.view, 1000, 680)
	tt.SetDark(true)
	tt.Advance(2 * time.Second)
	tt.Frame()
	saveFrame(t, dir, "backdrop.png", tt.Image())
}

// With MM_FRAMES set, saves the progress bar's comet at moments, in Now
// Playing and in the player bar.
func TestSmokeFrames(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	a := newTestApp()
	tr := coveredSong(a)
	a.pl.Cue([]music.Track{tr})
	a.pl.SetLyrics(nil)
	a.pl.Hold(75 * time.Second)
	a.settings.Smoke = true
	shots := func(name string, dark bool) {
		tt := ui.NewTester(a.view, 1000, 680)
		tt.SetDark(dark)
		tt.Advance(2 * time.Second)
		tt.Frame()
		r, ok := tt.Find("Progress")
		if !ok {
			t.Fatal("no progress bar")
		}
		img := tt.Image()
		scale := float32(img.Bounds().Dx()) / 1000
		frac := float32(75.0 / tr.Length().Seconds())
		cx, cy := int((r.X+r.W*frac)*scale), int((r.Y+r.H/2)*scale)
		w, h := int(200*scale), int(70*scale)
		moments := 4
		strip := image.NewRGBA(image.Rect(0, 0, w, h*moments))
		for i := range moments {
			tt.Advance(170 * time.Millisecond)
			tt.Frame()
			draw.Draw(strip, image.Rect(0, i*h, w, (i+1)*h), tt.Image(), image.Pt(cx-w*3/4, cy-h*2/3), draw.Src)
		}
		saveFrame(t, dir, name+".png", strip)
	}
	a.nowPlaying = true
	shots("smoke-np", true)
	a.nowPlaying = false
	shots("smoke-bar", false)
}

// With MM_FRAMES set, saves the player bar at window widths from the
// narrowest up.
func TestPlayerBarWidths(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	a := newTestApp()
	tr := music.Track{ID: "7", Name: "那些你很冒险的梦 (Live)", Artists: []music.Artist{{Name: "林俊杰"}, {Name: "JJ Lin"}}, Duration: 199000}
	a.pl.Cue([]music.Track{tr})
	a.pl.Seek(25 * time.Second)
	widths := []int{880, 1000, 1180}
	var strip *image.RGBA
	y := 0
	for i, w := range widths {
		tt := ui.NewTester(a.view, w, 600)
		tt.Frame()
		img := tt.Image()
		s := float32(img.Bounds().Dx()) / float32(w)
		bar := image.Rect(int(float32(sidebarWidth)*s), int(float32(600-playerBarHeight)*s), img.Bounds().Dx(), img.Bounds().Dy())
		if i == 0 {
			strip = image.NewRGBA(image.Rect(0, 0, int(float32(1180-sidebarWidth)*s), bar.Dy()*len(widths)))
			draw.Draw(strip, strip.Bounds(), image.NewUniform(color.RGBA{60, 60, 60, 255}), image.Point{}, draw.Src)
		}
		draw.Draw(strip, image.Rect(0, y, bar.Dx(), y+bar.Dy()), img, bar.Min, draw.Src)
		y += bar.Dy()
	}
	saveFrame(t, dir, "bar-widths.png", strip)
}

// With MM_FRAMES set, saves the volume popover open, light and dark.
func TestVolumePopoverFrames(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, dark := range []bool{false, true} {
		a := newTestApp()
		a.pl.Cue([]music.Track{song("7", "迷星叫", "MyGO!!!!!")})
		a.volume = 0.63
		tt := ui.NewTester(a.view, 1000, 600)
		tt.SetDark(dark)
		tt.Frame()
		r, ok := tt.Find("Volume")
		if !ok {
			t.Fatal("no volume button")
		}
		tt.Click("Volume")
		tt.Advance(time.Second)
		tt.Frame()
		if !a.volumeOpen || !tt.HasText("63%") {
			t.Fatalf("popover not open: %v %q", a.volumeOpen, tt.Texts())
		}
		img := tt.Image()
		s := float32(img.Bounds().Dx()) / 1000
		cx := int((r.X + r.W/2) * s)
		crop := image.Rect(cx-int(70*s), int((r.Y-240)*s), cx+int(70*s), int((r.Y+r.H+6)*s))
		out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
		draw.Draw(out, out.Bounds(), img, crop.Min, draw.Src)
		name := "volume-light.png"
		if dark {
			name = "volume-dark.png"
		}
		saveFrame(t, dir, name, out)
	}
}

// With MM_FRAMES set, saves the segmented control of the settings, light
// and dark, at twice the scale.
func TestSegmentedFrames(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, dark := range []bool{false, true} {
		a := newTestApp()
		a.settings.Appearance = "light"
		tt := ui.NewTester(a.settingsView, 760, 1200)
		tt.SetScale(2)
		tt.SetDark(dark)
		tt.Frame()
		r, ok := tt.Find("System")
		if !ok {
			t.Fatal("no segmented control")
		}
		img := tt.Image()
		crop := image.Rect(int((r.X-30)*2), int((r.Y-14)*2), int((r.X+250)*2), int((r.Y+r.H+14)*2))
		out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
		draw.Draw(out, out.Bounds(), img, crop.Min, draw.Src)
		name := "segmented-light.png"
		if dark {
			name = "segmented-dark.png"
		}
		saveFrame(t, dir, name, out)
	}
}
