package app

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// editTestApp is signed in, with two playlists of their own and one saved,
// and shows their playlist 10.
func editTestApp() *app {
	a := newTestApp()
	a.account = &music.Account{ID: "1", Name: "小明"}
	a.mine = []music.Playlist{
		{ID: "10", Name: "通勤", Mine: true},
		{ID: "11", Name: "睡前", Mine: true},
	}
	a.saved = []music.Playlist{{ID: "20", Name: "飙升榜", Saved: true}}
	a.playlists["10"] = &music.Playlist{ID: "10", Name: "通勤", Mine: true, Tracks: []music.Track{
		song("1", "一张相片", "林宥嘉"),
		song("2", "宝船", "DOUDOU"),
	}}
	a.route = route{page: pagePlaylist, src: "fake", id: "10"}
	return a
}

// Choosing one of the listener's playlists from a song's menu adds the
// song to it, and the menu doesn't offer the playlist the song is listed in.
func TestAddSongToPlaylist(t *testing.T) {
	type add struct{ pid, song string }
	calls := make(chan add, 1)
	a := editTestApp()
	fake(a).addTo = func(pid string, songs []string) error { calls <- add{pid, songs[0]}; return nil }
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Add to Playlist", "通勤"); err == nil {
		t.Error("offered to add the song to the playlist it is in")
	}
	if err := tt.ChooseMenuItem("Add to Playlist", "睡前"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	tt.Frame()
	if got := wait(t, calls, "the adding"); got != (add{"11", "2"}) {
		t.Errorf("added %+v, want song 2 to playlist 11", got)
	}
}

// Removing a song from the listener's playlist takes its row away at once,
// and asks the source to take it out.
func TestRemoveSongFromPlaylist(t *testing.T) {
	type del struct{ pid, song string }
	calls := make(chan del, 1)
	a := editTestApp()
	fake(a).removeFrom = func(pid string, songs []string) error { calls <- del{pid, songs[0]}; return nil }
	// Playing the id: the queue keeps the song.
	a.pl.Cue(a.playlists["10"].Tracks)
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Remove from Playlist"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	tt.Frame()
	if got := wait(t, calls, "the removing"); got != (del{"10", "2"}) {
		t.Errorf("removed %+v, want song 2 from playlist 10", got)
	}
	if _, ok := tt.Find("宝船"); ok {
		t.Error("the song's row is still listed")
	}
	if q := a.pl.Queue(); len(q) != 2 || q[1].ID != "2" {
		t.Errorf("removing the song changed the queue playing: %v", q)
	}
}

// A playlist the listener saved, not made, offers no removing.
func TestNoRemovingFromSavedPlaylist(t *testing.T) {
	a := editTestApp()
	a.playlists["20"] = &music.Playlist{ID: "20", Name: "飙升榜", Tracks: a.playlists["10"].Tracks}
	a.route = route{page: pagePlaylist, src: "fake", id: "20"}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Remove from Playlist"); err == nil {
		t.Error("offered to remove a song from a saved playlist")
	}
}

// The sidebar's plus asks for a name, and Enter makes the playlist so
// named.
func TestCreatePlaylist(t *testing.T) {
	names := make(chan string, 1)
	a := editTestApp()
	fake(a).create = func(name string) (*music.Playlist, error) {
		names <- name
		return &music.Playlist{ID: "12", Name: name}, nil
	}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("New Playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	tt.Type("  周末  ")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := wait(t, names, "the playlist"); got != "周末" {
		t.Errorf("made a playlist named %q, want 周末", got)
	}
}

// A playlist made from a song's menu opens once the source made it, and the
// song is added to it.
func TestCreatePlaylistForSong(t *testing.T) {
	added := make(chan string, 1)
	a := editTestApp()
	ch := posts(a)
	without(a, "isLiked")
	fake(a).create = func(name string) (*music.Playlist, error) { return &music.Playlist{ID: "12", Name: name}, nil }
	fake(a).addTo = func(pid string, songs []string) error { added <- pid + ":" + songs[0]; return nil }
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Add to Playlist", "New Playlist…"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if !a.naming.open || a.naming.add == nil || a.naming.add.ID != "2" {
		t.Fatalf("the dialog is %+v, want it open to add song 2", a.naming)
	}
	tt.Type("周末")
	tt.Key(0, ui.KeyEnter)
	runPost(t, ch)
	if a.route != (route{page: pagePlaylist, src: "fake", id: "12"}) || a.naming.open || a.editable("12") == nil {
		t.Fatalf("on %+v, dialog open %v, listed %+v", a.route, a.naming.open, a.mine)
	}
	if got := wait(t, added, "the adding"); got != "12:2" {
		t.Errorf("added %s", got)
	}
}

// Renaming a playlist from the sidebar's menu shows its new name at once,
// and asks the source to rename it.
func TestRenamePlaylist(t *testing.T) {
	type rename struct{ id, name string }
	calls := make(chan rename, 1)
	a := editTestApp()
	fake(a).rename = func(id, name string) error { calls <- rename{id, name}; return nil }
	a.route = route{page: pageHome, src: "fake"}
	a.homeLoading = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("睡前"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Rename…"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("助眠")
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := wait(t, calls, "the renaming"); got != (rename{"11", "助眠"}) {
		t.Errorf("renamed %+v, want playlist 11 to 助眠", got)
	}
	if a.mine[1].Name != "助眠" {
		t.Errorf("the sidebar names it %q", a.mine[1].Name)
	}
	if _, ok := tt.Find("助眠"); !ok {
		t.Error("the new name does not show")
	}
}

// Deleting the playlist shown, once confirmed, takes it from the sidebar
// and goes back to the page before, with no way forward to it.
func TestDeletePlaylist(t *testing.T) {
	ids := make(chan string, 1)
	a := editTestApp()
	fake(a).remove = func(id string) error { ids <- id; return nil }
	a.homeLoading = true
	a.route = route{page: pageHome, src: "fake"}
	a.open(route{page: pagePlaylist, id: "10"})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("More"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Delete Playlist…"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	select {
	case <-ids:
		t.Fatal("deleted before it was confirmed")
	default:
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := wait(t, ids, "the deleting"); got != "10" {
		t.Errorf("deleted playlist %s, want 10", got)
	}
	if a.editable("10") != nil {
		t.Error("the sidebar still lists it")
	}
	if a.route.page != pageHome || len(a.history) != 0 || len(a.forward) != 0 {
		t.Errorf("on %+v, history %v, forward %v; want Home with nowhere to go", a.route, a.history, a.forward)
	}
}

// Cancelling the confirmation deletes nothing.
func TestCancelDeletingPlaylist(t *testing.T) {
	a := editTestApp()
	fake(a).remove = func(string) error { t.Error("deleted the playlist"); return nil }
	a.route = route{page: pageHome, src: "fake"}
	a.homeLoading = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("睡前"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Delete Playlist…"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if a.editable("11") == nil {
		t.Error("the playlist left the sidebar")
	}
}

// Move Up and Move Down in a song's menu move it at once and ask the source
// to, and aren't offered at the end they can't go past.
func TestMoveSongInPlaylist(t *testing.T) {
	type move struct {
		id       string
		from, to int
	}
	calls := make(chan move, 2)
	a := editTestApp()
	fake(a).move = func(id string, from, to int) error { calls <- move{id, from, to}; return nil }
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	first := func() string { return a.playlists["10"].Tracks[0].ID }

	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Move Down"); err == nil {
		t.Error("offered to move the last song down")
	}
	if err := tt.ChooseMenuItem("Move Up"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	if first() != "2" {
		t.Fatalf("moved up, the first is %s", first())
	}
	if got := wait(t, calls, "the move up"); got != (move{"10", 1, 0}) {
		t.Errorf("asked the source for %+v", got)
	}
	tt.Frame()

	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Move Up"); err == nil {
		t.Error("offered to move the first song up")
	}
	if err := tt.ChooseMenuItem("Move Down"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	if first() != "1" {
		t.Fatalf("moved down, the first is %s", first())
	}
	if got := wait(t, calls, "the move down"); got != (move{"10", 0, 1}) {
		t.Errorf("asked the source for %+v", got)
	}
}

// A move the source refuses goes back, and says so.
func TestMoveSongGoesBack(t *testing.T) {
	a := editTestApp()
	ch := posts(a)
	without(a, "isLiked")
	fake(a).move = func(string, int, int) error { return errors.New("offline") }
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Move Up"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	if a.playlists["10"].Tracks[0].ID != "2" {
		t.Fatal("not moved at once")
	}
	runPost(t, ch)
	if a.playlists["10"].Tracks[0].ID != "1" || a.toast == "" {
		t.Errorf("the first is %s, toast %q", a.playlists["10"].Tracks[0].ID, a.toast)
	}
}

// What the source refuses of a change to the listener's playlists goes back:
// a new name, a playlist deleted and a song taken out.
func TestPlaylistEditsGoBack(t *testing.T) {
	offline := errors.New("offline")

	a := editTestApp()
	ch := posts(a)
	fake(a).rename = func(string, string) error { return offline }
	a.naming = nameDialog{open: true, name: "助眠", rename: "11"}
	a.submitName()
	if a.mine[1].Name != "助眠" {
		t.Fatal("not renamed at once")
	}
	runPost(t, ch)
	if a.mine[1].Name != "睡前" {
		t.Errorf("a new name the source refused stayed: %q", a.mine[1].Name)
	}

	a = editTestApp()
	ch = posts(a)
	fake(a).remove = func(string) error { return offline }
	a.deleting = deleteDialog{open: true, id: "11", name: "睡前"}
	a.deleteChosen()
	if a.editable("11") != nil {
		t.Fatal("not deleted at once")
	}
	runPost(t, ch)
	if a.editable("11") == nil {
		t.Error("a playlist the source refused to delete stayed gone")
	}

	a = editTestApp()
	ch = posts(a)
	fake(a).removeFrom = func(string, []string) error { return offline }
	a.removeFrom(a.library, "10", &a.playlists["10"].Tracks[1])
	if len(a.playlists["10"].Tracks) != 1 {
		t.Fatal("not taken out at once")
	}
	runPost(t, ch)
	if len(a.playlists["10"].Tracks) != 2 {
		t.Error("a song the source refused to take out stayed gone")
	}
}

// With MM_FRAMES set, saves the listener's playlist with its sidebar, and
// the dialogs that name and delete playlists, to look at.
func TestPlaylistEditFrames(t *testing.T) {
	dir := os.Getenv("MM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	a := editTestApp()
	tt := ui.NewTester(a.view, 1200, 800)
	save := func(name string) {
		tt.Advance(400 * time.Millisecond)
		tt.Frame()
		saveFrame(t, dir, name+".png", tt.Image())
	}
	for _, dark := range []bool{false, true} {
		suffix := "-light"
		if dark {
			suffix = "-dark"
		}
		tt.SetDark(dark)
		save("playlist" + suffix)
		a.newPlaylist(nil)
		save("create" + suffix)
		a.naming.open = false
		a.startRename(a.editable("11"))
		save("rename" + suffix)
		a.naming.open = false
		a.confirmDelete(a.editable("11"))
		save("delete" + suffix)
		a.deleting.open = false
	}
}

// A playlist of the listener's that the source keeps itself, such as the
// songs they liked, is listed as theirs, but offers no editing, no saving,
// and isn't a playlist to add songs to.
func TestFixedPlaylist(t *testing.T) {
	a := editTestApp()
	a.mine = append([]music.Playlist{{ID: "5", Name: "我喜欢的音乐", Mine: true, Fixed: true}}, a.mine...)
	a.playlists["5"] = &music.Playlist{ID: "5", Name: "我喜欢的音乐", Tracks: []music.Track{
		song("3", "晴天", "周杰伦"),
	}}
	if a.editable("5") != nil {
		t.Error("the fixed playlist is editable")
	}
	if a.savable(a.playlists["5"]) {
		t.Error("offered to save the listener's own playlist")
	}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.RightClick("宝船"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Add to Playlist", "我喜欢的音乐"); err == nil {
		t.Error("offered to add the song to the fixed playlist")
	}
	tt.Frame()
	if err := tt.RightClick("我喜欢的音乐"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Delete Playlist…"); err == nil {
		t.Error("the sidebar offered to delete the fixed playlist")
	}
	tt.Frame()
	a.route = route{page: pagePlaylist, src: "fake", id: "5"}
	tt.Frame()
	if err := tt.RightClick("晴天"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Remove from Playlist"); err == nil {
		t.Error("offered to take a song out of the fixed playlist")
	}
}
