package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// featureApp is Home led by a feature card of three songs.
func featureApp(more string) *app {
	a := newTestApp()
	a.home = &music.Browse{Sections: []music.Section{{
		Title:    "For You",
		Tracks:   sourced(song("1", "一", "甲"), song("2", "二", "乙"), song("3", "三", "丙")),
		MorePage: more,
	}}}
	return a
}

// The feature card opens all of its songs: their section's page, back from
// which is Home, or the page its source says has the rest.
func TestFeatureCardOpens(t *testing.T) {
	a := featureApp("")
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("For You"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.route != (route{page: pageSection, src: "fake", query: "0"}) {
		t.Fatalf("opened %+v", a.route)
	}
	if _, ok := tt.Find("三"); !ok {
		t.Error("its page doesn't list the songs")
	}
	tt.Key(0, ui.KeyBack)
	tt.Frame()
	if a.route.page != pageHome {
		t.Errorf("back went to %+v", a.route)
	}

	a = featureApp("more")
	tt = ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("For You"); err != nil {
		t.Fatal(err)
	}
	if a.route != (route{page: pageBrowse, src: "fake", id: "more"}) {
		t.Errorf("opened %+v", a.route)
	}
}

// The feature card's button plays its songs without opening them, and pauses
// them once they play.
func TestFeatureCardPlays(t *testing.T) {
	a := featureApp("")
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Play"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if cur := a.pl.Current(); cur == nil || cur.ID != "1" || len(a.pl.Queue()) != 3 || a.route.page != pageHome {
		t.Fatalf("playing %+v of %d on %+v", cur, len(a.pl.Queue()), a.route)
	}
	// Under the pointer, it pauses what plays.
	if err := tt.Click("Pause"); err != nil {
		t.Fatal(err)
	}
	if a.pl.Playing() || a.pl.Current().ID != "1" {
		t.Errorf("playing %v, %+v", a.pl.Playing(), a.pl.Current())
	}
}

// On a wide page, a pick on the feature card plays its songs from it.
func TestFeatureCardPicks(t *testing.T) {
	a := featureApp("")
	tt := ui.NewTester(a.view, 1500, 800)
	tt.Frame()
	if err := tt.Click("二"); err != nil {
		t.Fatal(err)
	}
	if cur := a.pl.Current(); cur == nil || cur.ID != "2" || len(a.pl.Queue()) != 3 {
		t.Fatalf("playing %+v of %d", cur, len(a.pl.Queue()))
	}
}

// Playlists in the feature card's own section keep off it as rows of cards keep
// apart: they came right under it, as its section's title, which would have
// kept them off, is the card's.
func TestFeatureCardKeepsItsSectionOff(t *testing.T) {
	a := featureApp("")
	a.home.Sections[0].Playlists = []music.Playlist{{ID: "p1", Name: "Coding BGM"}}
	tt := ui.NewTester(a.view, 1200, 900)
	settled(tt)
	card, ok := tt.Find("For You")
	list, ok2 := tt.Find("Coding BGM")
	if !ok || !ok2 {
		t.Fatalf("shows %q", tt.Texts())
	}
	if gap := list.Y - (card.Y + card.H); gap < cardRowGap-0.5 {
		t.Errorf("the playlists are %.1f under the card", gap)
	}
}

// The page the feature card opens is headed by the section's pageTitle, if
// it has one, and its subtitle follows the count of songs.
func TestSectionPageHeading(t *testing.T) {
	a := featureApp("")
	a.home.Sections[0].PageTitle = "Daily Song Picks"
	a.home.Sections[0].Subtitle = "New every day"
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("For You"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("Daily Song Picks"); !ok {
		t.Error("the page isn't headed by its pageTitle")
	}
	if _, ok := tt.Find("3 songs · New every day"); !ok {
		t.Error("the subtitle doesn't follow the count")
	}
}
