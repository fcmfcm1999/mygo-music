package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/i18n"
	"mygo-music/internal/local"
	"mygo-music/internal/music"
	"mygo-music/internal/player"
)

const pagePad = 40

// columns returns how many cards of at least min DIPs fit across the page.
func (a *app) columns(c *ui.Context, min float32) int {
	w, _ := c.Size()
	inner := w - sidebarWidth - 2*pagePad
	return max(2, int((inner+20)/(min+20)))
}

func (a *app) heading(c *ui.Context, title, sub string) {
	ui.Column(c).Gap(4).Margin(0, 0, 18, 0).Children(func() {
		if sub != "" {
			ui.Text(c, sub).FontSize(12).FontWeight(600).TextColor(a.pal.muted).LetterSpacing(0.4)
		}
		ui.Text(c, title).FontSize(30).FontWeight(700).LetterSpacing(-0.6)
	})
}

func (a *app) section(c *ui.Context, title string) ui.Element {
	return ui.Text(c, title).FontSize(19).FontWeight(700).LetterSpacing(-0.3).Margin(34, 0, 14, 0)
}

// homeHello is the heading of Home: a greeting, to whoever is signed in, and
// the day.
func (a *app) homeHello(c *ui.Context, now time.Time) {
	hello := greeting(now)
	if a.account != nil && a.account.Name != "" {
		hello = Tf("%s, %s", hello, a.account.Name)
	}
	a.heading(c, hello, i18n.Today(now))
}

// homePage is what the source makes up itself, if it does; else what it played
// lately and its playlists; else, with nothing yet, a way to get started.
func (a *app) homePage(c *ui.Context) {
	now := c.Now()
	lib := a.library
	if lib.home != nil && len(lib.home.Sections) > 0 {
		a.browseRows(c, lib.home, "", func(c *ui.Context) { a.homeHello(c, now) })
		return
	}
	loading := lib.homeLoading || !lib.started || lib.accountUnknown()
	// A source that makes up its own Home shows none of this one until it said
	// it has none: its playlists, loaded first, would show here, then make way.
	nothing := len(lib.recent) == 0 && len(lib.mine) == 0 && len(lib.saved) == 0 || lib.can("home") && loading
	if nothing && !loading {
		if lib.homeErr != "" {
			a.centered(c, lib.homeErr, func() { a.loadHome(lib) })
			return
		}
		a.emptyHome(c)
		return
	}
	top, bottom := a.insets(c)
	ui.Scroll(c.Key(a.route)).TrackScroll(&a.pageScroll).Grow(1).Padding(top+pageGap, pagePad-12, bottom+48, pagePad-12).ScrollbarInsets(top, 0, bottom, 0).Children(func() {
		a.startMark(c)
		defer a.endMark(c)
		ui.Box(c).PaddingX(12).Children(func() { a.homeHello(c, now) })
		if nothing {
			a.homeSkeleton(c, a.pageShimmer(c), lib.can("home"))
			return
		}
		if len(lib.recent) > 0 {
			ui.Box(c).PaddingX(12).Children(func() { a.section(c, T("Recently Played")).Margin(0, 0, 8, 0) })
			recent := lib.recent[:min(len(lib.recent), 8)]
			for i := range recent {
				a.trackRow(c, recent, i, true, "")
			}
		}
		lists := append(append([]music.Playlist(nil), lib.mine...), lib.saved...)
		if len(lists) > 0 {
			ui.Column(c).PaddingX(12).Children(func() {
				a.section(c, T("Playlists"))
				ui.Grid(c).Columns(a.columns(c, 168)).GapX(20).GapY(28).Children(func() {
					for i := range lists {
						a.playlistCard(c.Key(lists[i].ID), &lists[i])
					}
				})
			})
		}
	})
}

// pickFolders asks the listener for folders with a native dialog, on a
// goroutine, for it blocks; tests replace it.
var pickFolders = func(a *app, got func([]string)) {
	go func() {
		paths, err := mygoOpenFolders(a)
		if err == nil && len(paths) > 0 {
			a.post(func() { got(paths) })
		}
	}()
}

// addMusicFolder asks for folders of music and starts looking through them.
func (a *app) addMusicFolder() {
	pickFolders(a, func(paths []string) {
		if a.local != nil {
			a.local.AddFolders(paths...)
		}
	})
}

// emptyHome invites the listener to do what gets the source going.
func (a *app) emptyHome(c *ui.Context) {
	p := a.pal
	lib := a.library
	top, bottom := a.insets(c)
	ui.Column(c.Key("empty-home")).Grow(1).Padding(top, 40, bottom, 40).Center().Gap(10).Children(func() {
		ui.Box(c).Size(72, 72).Radius(20).Center().Background(p.hover).Margin(0, 0, 8, 0).Children(func() {
			icon := iconNote
			if lib.id == local.ID {
				icon = iconFolder
			}
			ui.Icon(c, icon).Size(32, 32).TextColor(p.muted)
		})
		switch {
		case lib.id == local.ID && a.local != nil && a.local.Scanning():
			ui.Text(c, T("Looking for music…")).FontSize(22).FontWeight(700).LetterSpacing(-0.3)
			ui.Text(c, T("Songs will show here as they're found.")).FontSize(13.5).TextColor(p.muted).TextAlign(ui.Center)
		case lib.id == local.ID:
			title, sub := T("Add a music folder"), T("Choose where your music lives, and it will all be here: MP3, FLAC, Ogg, WAV and M4A files, with their tags, covers and lyrics.")
			if a.local != nil && len(a.local.Folders()) > 0 {
				title, sub = T("No music found"), T("There are no music files in the folders you chose. Try another.")
			}
			ui.Text(c, title).FontSize(22).FontWeight(700).LetterSpacing(-0.3)
			ui.Text(c, sub).FontSize(13.5).TextColor(p.muted).TextAlign(ui.Center).MaxWidth(420).LineHeight(1.45)
			if pillButton(c.Key("add-folder"), iconPlus, T("Add a Music Folder"), p.accent, p.onAccent).Margin(10, 0, 0, 0).Clicked() {
				a.addMusicFolder()
			}
		case lib.signedOut():
			ui.Text(c, Tf("Sign in to %s", lib.name())).FontSize(22).FontWeight(700).LetterSpacing(-0.3)
			if pillButton(c.Key("sign-in"), iconUser, T("Sign In"), p.accent, p.onAccent).Margin(10, 0, 0, 0).Clicked() {
				a.promptSignIn(lib)
			}
		default:
			ui.Text(c, T("Nothing here yet")).FontSize(22).FontWeight(700).LetterSpacing(-0.3)
			if a.can("search") {
				ui.Text(c, T("Search for something to play.")).FontSize(13.5).TextColor(p.muted)
			}
		}
	})
}

// playlistCard is a square cover with the playlist's name below: it lifts
// under the pointer, where a play button shows on it.
func (a *app) playlistCard(c *ui.Context, pl *music.Playlist) {
	lib := a.library
	id := pl.ID
	if a.coverCard(c, pl.Name, pl.ImageURL, playlistLine(pl), func() { a.loadPlaylist(lib, id, true) }) {
		a.open(route{page: pagePlaylist, id: id})
	}
}

// playlistLine is what a playlist's card says under its name: how many songs
// it has and who made it, as strangers' playlists are told apart by their
// makers.
func playlistLine(pl *music.Playlist) string {
	var meta []string
	if pl.TrackCount > 0 {
		meta = append(meta, i18n.Songs(pl.TrackCount))
	}
	if pl.Owner != "" {
		meta = append(meta, pl.Owner)
	}
	return strings.Join(meta, " · ")
}

// coverCard is a playlist's card, or a show's or a station's: its cover over
// its name and a line under that. The button on the cover, if there's something
// for it to call, calls play; it reports whether the rest was clicked.
func (a *app) coverCard(c *ui.Context, name, cover, sub string, play func()) bool {
	return a.coverCardWith(c, name, cover, iconNote, sub, play)
}

// coverCardWith is a card that shows icon if there's no cover to show, as a
// source needn't have one.
func (a *app) coverCardWith(c *ui.Context, name, cover string, icon *ui.SVG, sub string, play func()) bool {
	p := a.pal
	// From the top: a grid stretches the cards of a row as tall as the
	// tallest, whose name or line under it takes two lines.
	card := ui.ButtonBase(c).Column().Gap(10).AlignItems(ui.Stretch).Justify(ui.Start).FocusRing(false).Label(name)
	hovered := card.Hovered()
	card.Children(func() {
		art := ui.Box(c).AspectRatio(1).Radius(10)
		lift := ui.Pose{}
		if hovered {
			lift = ui.Pose{Y: -3}
			art.Shadow(0, 12, 28, -6, p.cardShade)
		} else {
			art.Shadow(0, 2, 8, -2, p.cardShade)
		}
		art.AnimatePose(lift, ui.Snappy)
		art.Children(func() {
			a.coverFill(c, cover, 400, 10)
			if cover == "" {
				ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icon).Size(40, 40).TextColor(p.faint)
				})
			}
			if play != nil {
				a.coverPlayButton(c, hovered, play)
			}
		})
		ui.Text(c, name).FontSize(13).FontWeight(500).MaxLines(2).LineHeight(1.35)
		if sub != "" {
			ui.Text(c, sub).FontSize(12).TextColor(p.muted).SingleLine().Margin(-6, 0, 0, 0)
		}
	})
	return card.Clicked()
}

// coverFill fills its parent with a cover, its corners rounded by radius.
// The cover rounds itself rather than its parent clipping it: a clip moved
// by a transform, as a card lifting under the pointer, is drawn on the CPU.
func (a *app) coverFill(c *ui.Context, url string, px int, radius float32) {
	bmp := bitmap(a.art.get(sized(url, px), false))
	box := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(radius)
	in := artIn(box, bmp)
	if in < 1 {
		box.Background(a.pal.hover)
	}
	box.Children(func() { artImage(c, bmp, radius, in) })
}

// coverPlayButton plays a playlist or a show from its card, without
// opening it.
func (a *app) coverPlayButton(c *ui.Context, shown bool, play func()) {
	b := ui.ButtonBase(c.Key("play")).Absolute().Bottom(10).Right(10).Size(38, 38).Radius(999).Center().
		Background(a.pal.accent).TextColor(ui.RGB(255, 255, 255)).Label(T("Play")).FocusRing(false).
		Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.3))
	pose := ui.Pose{Fade: 1, Scale: 0.8, Y: 6}
	if shown || b.Hovered() {
		pose = ui.Pose{Scale: 1}
		if b.Hovered() {
			pose.Scale = 1.05
		}
	}
	b.AnimatePose(pose, ui.Snappy)
	b.Children(func() { ui.Icon(c, iconPlay).Size(15, 15).Margin(0, 0, 0, 2) })
	if b.Clicked() {
		play()
	}
}

func (a *app) playlistPage(c *ui.Context) {
	id := a.route.id
	pl := a.playlists[id]
	if pl == nil {
		if !a.loadingPl[id] {
			a.centered(c, a.plErr[id], func() { a.loadPlaylist(a.library, id, false) })
			return
		}
		s := a.pageShimmer(c)
		a.trackSkeleton(c, s, true, func(c *ui.Context) { a.playlistHeaderSkeleton(c, s) })
		return
	}
	a.trackRows(c, pl.Tracks, true, func(c *ui.Context) { a.playlistHeader(c, pl) })
}

func (a *app) playlistHeader(c *ui.Context, pl *music.Playlist) {
	p := a.pal
	ui.Row(c).Gap(28).AlignItems(ui.End).Padding(0, 0, 26, 0).Children(func() {
		ui.Box(c).Size(212, 212).Radius(14).Shadow(0, 18, 40, -10, p.cardShade).Children(func() {
			a.coverFill(c, pl.ImageURL, 600, 14)
		})
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Children(func() {
			ui.Text(c, T("Playlist")).FontSize(12).FontWeight(600).TextColor(p.accent)
			ui.Text(c, pl.Name).FontSize(30).FontWeight(800).LetterSpacing(-0.6).MaxLines(2).LineHeight(1.2)
			var meta []string
			if pl.Owner != "" {
				meta = append(meta, pl.Owner)
			}
			meta = append(meta, i18n.Songs(len(pl.Tracks)))
			ui.Text(c, strings.Join(meta, " · ")).FontSize(13).TextColor(p.muted)
			if d := strings.TrimSpace(pl.Description); d != "" {
				ui.Text(c, strings.ReplaceAll(d, "\n", " ")).FontSize(12.5).TextColor(p.muted).MaxLines(2).LineHeight(1.45).Margin(2, 0, 0, 0)
			}
			ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Children(func() {
				a.playButtons(c, pl.Tracks)
				if a.savable(pl) {
					icon, label := iconPlus, TC("Save", "library")
					if a.isSaved(pl) {
						icon, label = iconCheck, T("Saved")
					}
					if pillButton(c.Key("save"), icon, label, p.hover.Over(p.bg), p.text).Clicked() {
						a.toggleSaved(*pl)
					}
				}
				if mine := a.editable(pl.ID); mine != nil && (a.can("renamePlaylist") || a.can("deletePlaylist")) {
					more := iconButton(c.Key("more"), iconMore, 18, p.muted, T("More"))
					more.Menu(func(m *ui.Menu) { a.playlistMenu(m, mine) })
				}
			})
		})
	})
}

// playButtons are the buttons of a page that play what it lists, in order or
// shuffled.
func (a *app) playButtons(c *ui.Context, tracks []music.Track) {
	p := a.pal
	if pillButton(c.Key("play"), iconPlay, T("Play"), p.accent, p.onAccent).Clicked() {
		if a.pl.Shuffle() {
			a.pl.ToggleShuffle()
		}
		a.pl.PlayQueue(tracks, firstPlayable(tracks))
	}
	if pillButton(c.Key("shuffle"), iconShuffle, T("Shuffle"), p.hover.Over(p.bg), p.text).Clicked() {
		if !a.pl.Shuffle() {
			a.pl.ToggleShuffle()
		}
		a.pl.PlayQueue(tracks, firstPlayable(tracks))
	}
}

// likedPage lists the songs the listener likes, loaded a page at a time.
func (a *app) likedPage(c *ui.Context) {
	lib := a.library
	header := func(c *ui.Context) {
		p := a.pal
		ui.Row(c).Gap(28).AlignItems(ui.End).Padding(0, 0, 26, 0).Children(func() {
			ui.Box(c).Size(212, 212).Radius(14).Center().Shadow(0, 18, 40, -10, p.cardShade).TextColor(white).
				LinearGradient(ui.LinearGradient{Angle: 135, Oklab: true, From: ui.Oklch(0.72, 0.17, 10), To: ui.Oklch(0.58, 0.22, 350)}).
				Children(func() { ui.Icon(c, iconHeartFill).Size(84, 84) })
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Children(func() {
				ui.Text(c, T("Playlist")).FontSize(12).FontWeight(600).TextColor(p.accent)
				ui.Text(c, T("Liked Songs")).FontSize(30).FontWeight(800).LetterSpacing(-0.6)
				if lib.likedPaged.loaded {
					ui.Text(c, i18n.Songs(len(lib.likedList))).FontSize(13).TextColor(p.muted)
				}
				if len(lib.likedList) > 0 {
					ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Children(func() { a.playButtons(c, lib.likedList) })
				}
			})
		})
	}
	if !lib.likedPaged.loaded {
		if lib.likedPaged.err != "" {
			a.centered(c, lib.likedPaged.err, func() { a.loadLikedPage(lib) })
			return
		}
		a.trackSkeleton(c, a.pageShimmer(c), true, header)
		return
	}
	a.trackRows(c, lib.likedList, true, header, func(c *ui.Context) {
		a.pageFoot(c, &lib.likedPaged, func() { a.loadLikedPage(lib) })
		if len(lib.likedList) == 0 {
			a.empty(c, T("Songs you like will show here"))
		}
	})
}

// loadLikedPage loads the next page of the songs the listener likes.
func (a *app) loadLikedPage(lib *library) {
	if lib.likedPaged.loading {
		return
	}
	lib.likedPaged.loading, lib.likedPaged.err = true, ""
	offset := len(lib.likedList)
	if offset == 0 {
		lib.likedAt = time.Now()
	}
	go func() {
		page, err := answered(lib.src.Liked(offset))
		a.post(func() {
			lib.likedPaged.loading = false
			if err != nil {
				lib.likedPaged.err = a.problem(lib, err, T("Couldn't load your liked songs"))
				return
			}
			lib.likedList = append(lib.likedList, page.Tracks...)
			lib.likedPaged.loaded, lib.likedPaged.more = true, page.More && len(page.Tracks) > 0
			if lib.liked == nil {
				lib.liked = map[string]bool{}
			}
			for i := range page.Tracks {
				lib.liked[page.Tracks[i].ID] = true
			}
		})
	}()
}

// freshenLiked asks for the first page of the liked songs anew, a while after
// it was: it takes the place of what's listed, which shows until it comes.
// Songs listed before and not now are asked about again as their hearts show.
func (a *app) freshenLiked(lib *library) {
	if lib.likedFresh || lib.likedPaged.loading || time.Since(lib.likedAt) <= listsFresh {
		return
	}
	lib.likedFresh, lib.likedAt = true, time.Now()
	was := lib.likedList
	who := lib.listener()
	go func() {
		page, err := answered(lib.src.Liked(0))
		a.post(func() {
			lib.likedFresh = false
			// Liked or not in the app meanwhile, or more of it loaded, or
			// forgotten: what's there is newer than what was asked for.
			if err != nil || !sameList(lib.likedList, was) || lib.likedPaged.loading || lib.listener() != who {
				return
			}
			lib.likedList = page.Tracks
			lib.likedPaged.more = page.More && len(page.Tracks) > 0
			if lib.liked == nil {
				lib.liked = map[string]bool{}
			}
			now := make(map[string]bool, len(page.Tracks))
			for i := range page.Tracks {
				now[page.Tracks[i].ID] = true
				lib.liked[page.Tracks[i].ID] = true
			}
			for i := range was {
				if !now[was[i].ID] {
					delete(lib.liked, was[i].ID)
				}
			}
		})
	}()
}

// trackRows lists tracks under a header, which scrolls with them; a click
// plays the list from the track clicked.
func (a *app) trackRows(c *ui.Context, tracks []music.Track, covers bool, header func(c *ui.Context), footer ...func(c *ui.Context)) {
	// A song's menu can take it out of the playlist it is listed in.
	var pid string
	if a.route.page == pagePlaylist {
		pid = a.route.id
	}
	top, bottom := a.insets(c)
	// Rows out of view are not built, their marks with them: a list
	// scrolled away from its start or end has content under the bars.
	a.underTop, a.underBottom = true, true
	rows := len(tracks) + 1 + len(footer)
	ui.List(c.Key(a.route), &a.trackList, rows, func(i int) {
		if i == 0 {
			ui.Column(c).Children(func() {
				a.startMark(c)
				header(c)
			})
			return
		}
		if i == rows-1 {
			defer a.endMark(c)
		}
		if i > len(tracks) {
			footer[i-len(tracks)-1](c)
			return
		}
		a.trackRow(c, tracks, i-1, covers, pid)
	}).Grow(1).Padding(top+pageGap, pagePad-12, bottom+32, pagePad-12).ScrollbarInsets(top, 0, bottom, 0)
}

// sameTrack reports whether two tracks are one: IDs are their source's.
func sameTrack(x, y *music.Track) bool {
	return x != nil && y != nil && x.ID == y.ID && x.Source == y.Source
}

// trackRow is a row of the list tracks, its track i, which plays the list from
// there as it's clicked.
func (a *app) trackRow(c *ui.Context, tracks []music.Track, i int, covers bool, pid string) {
	p := a.pal
	tr := &tracks[i]
	w, _ := c.Size()
	// An album's page has no column of the album its songs are on.
	wide := w-sidebarWidth > 820 && a.route.page != pageAlbum
	playing := sameTrack(a.pl.Current(), tr)
	row := ui.ButtonBase(c.Key(tr.ID)).Height(54).PaddingX(12).Gap(14).Radius(10).AlignItems(ui.Center).FocusRing(false).Label(tr.Name).
		ContextMenu(a.songItems(tr, pid))
	hovered := row.Hovered()
	bg := ui.Transparent
	switch {
	case playing:
		bg = p.accent.Alpha(0.09)
	case hovered:
		bg = p.hover
	}
	row.Background(row.AnimateColor("bg", bg, ui.Animation{Duration: 100 * time.Millisecond}))
	if tr.Unavailable {
		row.Opacity(0.42)
	}
	a.wantLike(tr)
	row.Children(func() {
		ui.Box(c).Width(26).Center().Children(func() {
			switch {
			case playing:
				a.nowPlayingBars(c)
			case hovered && !tr.Unavailable:
				ui.Icon(c, iconPlay).Size(13, 13).TextColor(p.text)
			default:
				ui.Textf(c, "%d", i+1).FontSize(13).FontFeatures("tnum").TextColor(p.faint)
			}
		})
		if covers {
			a.cover(c, tr.Album.ImageURL, 96, 38, 6)
		}
		ui.Column(c).Grow(3).Basis(0).MinWidth(0).Gap(3).Children(func() {
			name := p.text
			if playing {
				name = p.accent
			}
			ui.Text(c, tr.Name).FontSize(14).FontWeight(500).TextColor(name).SingleLine()
			a.artistLinks(c, tr, p.muted, p.text).FontSize(12.5).SingleLine().Shrink(1).MinWidth(0)
		})
		if wide {
			album := ui.RichText(c).Grow(2).Basis(0).MinWidth(0).FontSize(12.5).TextColor(p.muted).SingleLine()
			album.Children(func() {
				if tr.Album.ID == "" || !a.trackLib(tr).can("album") {
					ui.Text(c, tr.Album.Name)
				} else {
					a.link(c.Key("album"), tr.Album.Name, p.muted, p.text, route{page: pageAlbum, src: tr.Source, id: tr.Album.ID})
				}
			})
		}
		// A liked song's heart shows; the others' as the pointer is on them.
		ui.Box(c).Size(32, 32).Center().Children(func() {
			if a.isLiked(tr) || hovered {
				a.likeButton(c.Key("like"), tr, 15, p.muted)
			}
		})
		ui.Text(c, clock(tr.Length())).FontSize(12.5).FontFeatures("tnum").TextColor(p.faint).Width(44).TextAlign(ui.End)
	})
	if row.Clicked() && !tr.Unavailable {
		a.pl.PlayTrack(tracks, i)
	}
}

// nowPlayingBars dance in the row of the track playing.
func (a *app) nowPlayingBars(c *ui.Context) {
	color := a.pal.accent
	ui.Box(c).Size(16, 14).Draw(func(p *ui.Painter, r ui.Rect) {
		lv, live := a.pl.Levels()
		a.rowBars.step(p.Now(), lv, live)
		drawBars(p, r, &a.rowBars, 4, color, 2.5)
		if live || a.rowBars.group(0, player.Bands) > 0.01 {
			p.AnimationFrame()
		}
	})
}

// sized names a cover by the size it shows at: the cache keeps it no bigger.
func sized(url string, px int) string {
	if url == "" {
		return ""
	}
	return fmt.Sprintf("%s#%d", url, px)
}
