package app

import (
	"runtime"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

const sidebarWidth = 232

func (a *app) view(c *ui.Context) {
	t := *c.Theme()
	a.pal = paletteFor(&t)
	p := a.pal
	useAccent(&t, p)
	t.Selection = p.accent.Alpha(0.3)
	c.SetTheme(&t)

	a.shortcuts(c)

	// Back to the front, after the listener may have changed their
	// playlists elsewhere.
	active := c.WindowActive()
	if active && a.wasBehind {
		a.freshen()
	}
	a.wasBehind = !active

	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	root := ui.Box(c).Fill()
	if !c.Focused(a.searchBox) {
		// Space plays and pauses wherever the focus is, as in Music. On
		// the container around everything, it comes before a button the
		// pointer focused, which would take Space as a click; on the
		// window, it would come after.
		root.OnShortcut(0, ui.KeySpace, a.pl.TogglePlay)
		// And where nothing has the focus, as once Now Playing covers the
		// button that had it.
		c.OnShortcut(0, ui.KeySpace, a.pl.TogglePlay)
	}
	npOpen, npSettled := a.npState(c)
	root.Children(func() {
		page := ui.Row(c).Fill().AlignItems(ui.Stretch)
		page.Children(func() {
			if npOpen && npSettled {
				// Covered by Now Playing: not built, which would cost each
				// of its frames as much again as the stage itself. Where
				// it is scrolled is the app's (pageScroll, navScroll,
				// trackList), so it shows again as it was.
				return
			}
			side := ui.Column(c).Width(sidebarWidth)
			if !c.Vibrancy() {
				side.Background(p.sidebar)
			}
			side.Children(func() { a.sidebar(c) })
			// Keyed, so that a popover of the sidebar opening or closing
			// keeps what the page and its bars hold, their edges' fades.
			ui.Box(c.Key("main")).Grow(1).Background(p.bg).Children(func() {
				a.content(c)
				// The bars float over the page, which scrolls under them:
				// positioned, they paint over it.
				a.pageTop(c)
				a.playerBar(c)
			})
		})
		a.nowPlayingLayer(c, npOpen, npSettled)
	})
	a.signInDialog(c, a.pal, false)
	a.playlistNameDialog(c)
	a.deletePlaylistDialog(c)
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}
	a.askLikes()
	a.syncWindows()
}

func (a *app) shortcuts(c *ui.Context) {
	// The side buttons of a mouse, the keys of keyboards that have them
	// and swipes between pages, and the keys of Music.
	c.OnShortcut(0, ui.KeyBack, a.back)
	c.OnShortcut(0, ui.KeyForward, a.goForward)
	c.OnShortcut(ui.Cmd, ui.KeyBracketLeft, a.back)
	c.OnShortcut(ui.Cmd, ui.KeyBracketRight, a.goForward)
	c.OnShortcut(ui.Cmd, ui.KeyF, func() {
		a.nowPlaying = false
		a.searchBox.Focus(c)
	})
	if a.nowPlaying {
		c.OnShortcut(0, ui.KeyEscape, func() { a.nowPlaying = false })
		// ⌘ and a number choose Now Playing's tab of that place.
		if a.pl.Current() != nil {
			for i, t := range a.npTabs() {
				c.OnShortcut(ui.Cmd, ui.Key1+ui.Key(i), func() { a.choosePane(t.pane) })
			}
		}
	}
}

// open opens a page, remembering the one before for the back button, and
// closes Now Playing over it, though it is the page open. A page of another
// source makes that source the active one.
func (a *app) open(r route) {
	a.nowPlaying = false
	if r.src == "" {
		r.src = a.route.src
	}
	if r == a.route {
		return
	}
	a.history, a.forward = append(a.history, a.route), nil
	a.show(r)
}

// show shows the page of r.
func (a *app) show(r route) {
	if r.src == "" {
		r.src = a.route.src
	}
	lib := a.libOf(r.src)
	if lib == nil {
		// Its source went away since: the active one's Home instead.
		r = route{page: pageHome, src: a.library.id}
		lib = a.library
	}
	a.activate(lib)
	a.route = r
	if r.page == pageSearch {
		a.query, a.searched = r.query, r
	}
	a.load(r)
}

// load loads what the page of r shows, unless it has.
func (a *app) load(r route) {
	lib := a.libOf(r.src)
	if lib == nil {
		return
	}
	switch r.page {
	case pageHome:
		a.freshenHome()
	case pagePlaylist:
		a.loadPlaylist(lib, r.id, false)
	case pageSearch:
		a.loadSearch(r, false)
	case pageArtist:
		a.loadArtist(lib, r.id)
	case pageAlbum:
		a.loadAlbum(lib, r.id)
	case pageShow:
		a.loadShow(lib, r.id)
	case pageShows:
		a.loadSubscribed(lib)
	case pageStations:
		a.loadStations(lib)
	case pageLiked:
		if !lib.likedPaged.loaded {
			a.loadLikedPage(lib)
		} else {
			a.freshenLiked(lib)
		}
	case pageSavedAlbums:
		if !lib.savedAlbumsPaged.loaded {
			a.loadSavedAlbums(lib)
		}
	case pageBrowse:
		a.loadBrowse(lib, r.id, false)
	case pageSection:
		if r.id != "" {
			a.loadBrowse(lib, r.id, false)
		}
	}
}

// back goes to the page before, as the back button of a mouse does, or
// closes Now Playing, which covers the pages.
func (a *app) back() {
	if a.nowPlaying {
		a.nowPlaying = false
		return
	}
	if len(a.history) == 0 {
		return
	}
	a.forward = append(a.forward, a.route)
	r := a.history[len(a.history)-1]
	a.history = a.history[:len(a.history)-1]
	a.show(r)
}

// goForward goes to the page back left, as the forward button of a mouse
// does.
func (a *app) goForward() {
	if len(a.forward) == 0 {
		return
	}
	a.history = append(a.history, a.route)
	r := a.forward[len(a.forward)-1]
	a.forward = a.forward[:len(a.forward)-1]
	a.nowPlaying = false
	a.show(r)
}

func (a *app) sidebar(c *ui.Context) {
	// Under the window buttons where they sit over the sidebar, as the
	// traffic lights do; elsewhere, as close to the top as to the sides.
	top := float32(14)
	if c.TitleBar().Left > 0 {
		top = topBar(c) + 4
	}
	// Where nothing in it takes the press, the sidebar moves the window, as
	// the title bar it reaches up into would.
	ui.Column(c).Fill().Padding(top, 14, 14, 14).Gap(4).DragWindow().Children(func() {
		a.sourceSwitcher(c)
		if a.can("search") {
			a.searchField(c)
		}

		ui.Scroll(c.Key("nav")).TrackScroll(&a.navScroll).Grow(1).Margin(0, -6).Padding(0, 6).Gap(4).Children(func() {
			for _, n := range a.sidebarItems() {
				a.navItem(c, n.icon, n.label, n.route)
			}
			if a.searched.query != "" && a.searched.src == a.id {
				a.navItem(c, iconSearch, T("Search Results"), a.searched)
			}
			if a.can("playlists") {
				a.myPlaylists(c, T("My Playlists"), a.mine, true, &a.mineCollapsed)
				a.myPlaylists(c, T("Saved Playlists"), a.saved, false, &a.savedCollapsed)
			}
		})

		a.accountRow(c)
	})
}

func (a *app) navItem(c *ui.Context, icon *ui.SVG, label string, r route) {
	p := a.pal
	selected := a.route.page == r.page && !a.nowPlaying && (r.page != pageBrowse || a.route.id == r.id)
	b := ui.ButtonBase(c.Key(label)).Height(34).PaddingX(10).Gap(10).Radius(8).AlignItems(ui.Center).Justify(ui.Start).FocusRing(false)
	bg := ui.Transparent
	switch {
	case selected:
		bg = p.press
	case b.Hovered():
		bg = p.hover
	}
	b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
	color := p.text
	if selected {
		color = p.accent
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(17, 17).TextColor(color)
		ui.Text(c, label).FontSize(13.5).FontWeight(500).TextColor(p.text)
	})
	if b.Clicked() {
		a.open(r)
	}
}

// clearSearch empties the field and forgets the search last shown, which
// the sidebar goes back to; on its page, it goes Home.
func (a *app) clearSearch() {
	a.searched, a.query = route{}, ""
	a.dismissSuggestions()
	if a.route.page == pageSearch && !a.nowPlaying {
		a.open(route{page: pageHome})
	}
}

// playerBarHeight is the height of the player along the foot of the pages.
const playerBarHeight = 84

// pageGap is the room between the bar over a page and its content.
const pageGap = 12

// insets returns the room the bars over a page take at its top and its
// bottom: pages pad their content by it, and scroll under the bars.
func (a *app) insets(c *ui.Context) (top, bottom float32) {
	top = topBar(c)
	if runtime.GOOS == "darwin" {
		// The edge reaches a little below the traffic lights' strip.
		top += topEdgePad
	}
	return top, playerBarHeight
}

// topEdgePad is the room the edge over a page leaves below the strip, on
// macOS.
const topEdgePad = 6

// buttonsHeight is the height of the window buttons off macOS, which fill
// the edge over a page.
const buttonsHeight = topBarHeight + topEdgePad

// topBar returns the height of the strip along the top of the window, as
// tall as the window buttons and the room above and below them, which
// what sits in it centers on.
func topBar(c *ui.Context) float32 {
	if runtime.GOOS != "darwin" {
		// The whole edge, which the buttons fill: what sits in the strip
		// centers on their glyphs.
		return buttonsHeight
	}
	return max(c.TitleBar().Height, topBarHeight)
}

// topBarHeight is the least height of the strip along the top.
const topBarHeight = 40

// startMark marks where a page's content starts, first in what scrolls:
// once it has moved up, content is under the bar at the top.
func (a *app) startMark(c *ui.Context) {
	b := ui.Box(c.Key("start-mark")).Height(0).Bounds()
	if b.W > 0 {
		top, _ := a.insets(c)
		a.underTop = b.Y < top+pageGap-0.5
	}
}

// endMark marks where a page's content ends, last in what scrolls: while it
// is below the top of the player, content is under the player.
func (a *app) endMark(c *ui.Context) {
	b := ui.Box(c.Key("end-mark")).Height(0).Bounds()
	if b.W > 0 {
		_, h := c.Size()
		_, bottom := a.insets(c)
		a.underBottom = b.Y > h-bottom+0.5
	}
}

// edgeShown returns how much of a scroll edge shows over el: fully while
// content is under its bar or the pointer is over it, and none at rest, as
// AppKit shows its edges.
func edgeShown(el ui.Element, under bool) float32 {
	target := float32(0)
	if under {
		target = 1
	}
	return el.AnimateTo("shown", target, ui.Animation{Duration: 180 * time.Millisecond})
}

// pageTop is the strip over the top of a page, frosting what scrolls under
// it as macOS 26's hard scroll edge does: it drags the window, and holds
// the back button once there is a page to go back to.
func (a *app) pageTop(c *ui.Context) {
	top, _ := a.insets(c)
	edge := ui.Box(c.Key("top-edge")).Absolute().Top(0).Left(0).Right(0).Height(top).DragWindow().
		Material(glass.ScrollEdge{Hard: true, Background: a.pal.bg})
	// The pointer over it brings the edge up too, as AppKit's does.
	edge.Opacity(edgeShown(edge, a.underTop || edge.Hovered()))
	if len(a.history) > 0 || len(a.forward) > 0 {
		ui.Row(c).Absolute().Top(0).Left(20).Height(topBar(c)).AlignItems(ui.Center).Children(func() {
			back := iconButton(c.Key("back"), iconBack, 18, a.pal.muted, T("Back")).Disabled(len(a.history) == 0)
			if len(a.history) == 0 {
				back.Opacity(0.35)
			}
			if back.Clicked() {
				a.back()
			}
			if len(a.forward) > 0 && iconButton(c.Key("forward"), iconForward, 18, a.pal.muted, T("Forward")).Clicked() {
				a.goForward()
			}
		})
	}
}

func (a *app) content(c *ui.Context) {
	// A new page starts at its top. Not as it is opened: a control in a
	// list, as a page's tabs in its header, opens it as the list builds.
	if a.scrolled != a.route {
		a.trackList, a.pageScroll, a.scrolled = ui.ListState{}, ui.ScrollState{}, a.route
	}
	// Pages mark where their content is, as they scroll.
	a.underTop, a.underBottom = false, false
	switch a.route.page {
	case pageHome:
		a.homePage(c)
	case pagePlaylist:
		a.playlistPage(c)
	case pageSearch:
		a.searchPage(c)
	case pageStations:
		a.stationsPage(c)
	case pageArtist:
		a.artistPage(c)
	case pageAlbum:
		a.albumPage(c)
	case pageShow:
		a.showPage(c)
	case pageShows:
		a.showsPage(c)
	case pageLiked:
		a.likedPage(c)
	case pageSavedAlbums:
		a.savedAlbumsPage(c)
	case pageBrowse:
		a.browsePage(c)
	case pageSection:
		a.sectionPage(c)
	}
}

// centered shows a message, with a retry button if retry is set: what a
// page shows in place of what failed to load. Where the source wants the
// listener to sign in or set it up, it offers that.
func (a *app) centered(c *ui.Context, err string, retry func()) {
	top, bottom := a.insets(c)
	ui.Column(c).Grow(1).Padding(top, 0, bottom, 0).Center().Gap(12).Children(func() {
		ui.Text(c, err).TextColor(a.pal.muted).FontSize(13).TextAlign(ui.Center).MaxWidth(520)
		ui.Row(c).Gap(10).Children(func() {
			a.remedy(c)
			if retry != nil && ui.Button(c.Key("retry"), T("Retry")).Clicked() {
				retry()
			}
		})
	})
}

// remedy is a button that takes the listener where the source's error can be
// fixed, if it said one.
func (a *app) remedy(c *ui.Context) {
	lib := a.library
	switch {
	case lib.needsSignIn && lib.signedOut():
		if ui.PrimaryButton(c.Key("sign-in"), T("Sign In")).Clicked() {
			a.promptSignIn(lib)
		}
	case lib.needsSetup:
		if ui.PrimaryButton(c.Key("setup"), T("Open Settings")).Clicked() {
			a.openSourceSettings()
		}
	}
}

// Loading what the pages show.

// homeFresh is how long what Home shows stays good. A plugin's picks may change
// by the hour; the app may stay open overnight.
const homeFresh = time.Hour

// loadHome loads what the Home of a source shows: its own page if it makes one
// up, else what it played lately, the playlists being the sidebar's.
func (a *app) loadHome(lib *library) {
	lib.homeLoading, lib.homeErr = true, ""
	at := time.Now()
	go func() {
		var home *music.Browse
		var recent []music.Track
		var homeErr, recentErr error
		if lib.can("home") {
			home, homeErr = lib.src.Home()
		}
		if home == nil || len(home.Sections) == 0 {
			home = nil
			if lib.can("recent") {
				recent, recentErr = lib.src.Recent()
			}
		}
		a.post(func() {
			lib.homeLoading = false
			lib.home, lib.recent = home, recent
			lib.homeAt = at
			switch {
			case homeErr != nil:
				lib.homeErr = a.problem(lib, homeErr, T("Couldn't load this page"))
			case recentErr != nil:
				lib.homeErr = a.problem(lib, recentErr, T("Couldn't load this page"))
			}
		})
	}()
}

// freshenHome loads the home page again a while after it last did.
func (a *app) freshenHome() {
	lib := a.library
	if !lib.homeLoading && !lib.homeAt.IsZero() && time.Since(lib.homeAt) > homeFresh {
		a.loadHome(lib)
	}
}

// listsFresh is how long the listener's playlists and liked songs, as last
// loaded, are taken to be what the source has. They change elsewhere too, in
// its own apps and on its website: opened again after it, or as the window
// comes back to the front, they are asked for anew.
const listsFresh = 30 * time.Second

// loadPlaylist loads a playlist's page, and plays it once it has, if thenPlay.
// Loaded a while ago, it shows as it was while it's asked for anew.
func (a *app) loadPlaylist(lib *library, id string, thenPlay bool) {
	if pl := lib.playlists[id]; pl != nil {
		if thenPlay {
			a.pl.PlayQueue(pl.Tracks, firstPlayable(pl.Tracks))
		}
		if !lib.freshPl[id] && time.Since(lib.plAt[id]) > listsFresh {
			a.freshenPlaylist(lib, id)
		}
		return
	}
	if lib.loadingPl[id] {
		return
	}
	lib.loadingPl[id] = true
	delete(lib.plErr, id)
	at := time.Now()
	go func() {
		pl, err := answered(lib.src.Playlist(id))
		a.post(func() {
			delete(lib.loadingPl, id)
			if err != nil {
				lib.plErr[id] = a.problem(lib, err, T("Couldn't load this playlist"))
				return
			}
			lib.playlists[id], lib.plAt[id] = pl, at
			if thenPlay {
				a.pl.PlayQueue(pl.Tracks, firstPlayable(pl.Tracks))
			}
		})
	}()
}

// freshenPlaylist asks for a loaded playlist anew, which shows as it was until
// the answer takes its place. Failing, it stays as it was.
func (a *app) freshenPlaylist(lib *library, id string) {
	was := lib.playlists[id]
	before := *was
	lib.freshPl[id] = true
	at := time.Now()
	go func() {
		pl, err := answered(lib.src.Playlist(id))
		a.post(func() {
			delete(lib.freshPl, id)
			// Changed in the app meanwhile, its songs or name, or forgotten:
			// what's there is newer than what was asked for.
			if err != nil || lib.playlists[id] != was || !sameList(was.Tracks, before.Tracks) ||
				was.Name != before.Name || was.Saved != before.Saved {
				return
			}
			lib.playlists[id], lib.plAt[id] = pl, at
		})
	}()
}

// sameList reports whether x and y are one list, not two alike: the app's
// changes to a playlist's songs make a new one.
func sameList(x, y []music.Track) bool {
	return len(x) == len(y) && (len(x) == 0 || &x[0] == &y[0])
}

// freshen asks anew for what the listener keeps of the active source, as
// the window comes back to the front: their playlists, and the page showing
// if it's one of them.
func (a *app) freshen() {
	lib := a.library
	if lib == nil || !lib.started {
		return
	}
	if time.Since(lib.mineAt) > listsFresh {
		a.loadMine(lib)
	}
	if r := a.route; r.src == lib.id && (r.page == pagePlaylist || r.page == pageLiked) {
		a.load(r)
	}
}

func firstPlayable(tracks []music.Track) int {
	for i := range tracks {
		if !tracks[i].Unavailable {
			return i
		}
	}
	return 0
}

// sidebarItem is an entry of the sidebar.
type sidebarItem struct {
	icon  *ui.SVG
	label string
	route route
}

// builtinPages are the app's pages a source's sidebar can have, each with what
// it needs the source to do, and how it's called and looks by default.
var builtinPages = map[string]struct {
	page  pageKind
	needs string
	label string
	icon  func() *ui.SVG
}{
	"home":        {pageHome, "", "Home", func() *ui.SVG { return iconHome }},
	"liked":       {pageLiked, "liked", "Liked Songs", func() *ui.SVG { return iconHeart }},
	"savedAlbums": {pageSavedAlbums, "savedAlbums", "Saved Albums", func() *ui.SVG { return iconAlbum }},
	"stations":    {pageStations, "stations", "Radio", func() *ui.SVG { return iconRadio }},
	"shows":       {pageShows, "shows", "Podcasts", func() *ui.SVG { return iconPodcast }},
}

// sidebarItems is what the source's Navigation says the sidebar has, or else an
// entry for each of the app's pages it can do.
func (a *app) sidebarItems() []sidebarItem {
	var items []sidebarItem
	nav := a.src.Info().Navigation
	if len(nav) == 0 {
		for _, name := range []string{"home", "liked", "savedAlbums", "stations", "shows"} {
			nav = append(nav, music.NavItem{Builtin: name})
		}
	}
	for _, n := range nav {
		if n.Builtin == "" {
			if a.can("browse") {
				items = append(items, sidebarItem{navIcon(n.Icon), n.Title, route{page: pageBrowse, id: n.Page}})
			}
			continue
		}
		b, ok := builtinPages[n.Builtin]
		if !ok || b.needs != "" && !a.can(b.needs) {
			continue
		}
		item := sidebarItem{b.icon(), n.Title, route{page: b.page}}
		if item.label == "" {
			item.label = T(b.label)
		}
		if n.Icon != "" {
			item.icon = navIcon(n.Icon)
		}
		items = append(items, item)
	}
	return items
}

// builtinTitle is what the source calls one of the app's pages, or else what the
// app does.
func (a *app) builtinTitle(name string) string {
	for _, n := range a.src.Info().Navigation {
		if n.Builtin == name && n.Title != "" {
			return n.Title
		}
	}
	return T(builtinPages[name].label)
}

// sourceSwitcher chooses the active source, once there is another. It takes no
// room of its own: beside the window buttons where they sit over the sidebar, in
// the strip they leave empty, and elsewhere just a line of text atop it. It
// drops down a list of the sources, in the colors of the account menu, with a
// check by the active one.
func (a *app) sourceSwitcher(c *ui.Context) {
	if len(a.libs) < 2 {
		return
	}
	p := a.pal
	const height = 24
	chosen := a.library.id
	sel := ui.SelectBase(c.Key("source"), &chosen)
	b := sel.Trigger.Height(height).PaddingX(7).Gap(4).Radius(7).AlignItems(ui.Center).
		FocusRing(false).Label(T("Plugin"))
	if left := c.TitleBar().Left; left > 0 {
		b.Absolute().Top((topBar(c) - height) / 2).Left(left + 12).MaxWidth(sidebarWidth - left - 22)
	} else {
		b.AlignSelf(ui.Start).Margin(0, 0, 4, -7).MaxWidth(sidebarWidth - 28)
	}
	color := p.muted
	if b.Hovered() || sel.Open() {
		b.Background(p.hover)
		color = p.text
	}
	b.Children(func() {
		ui.Text(c, a.name()).FontSize(12.5).FontWeight(600).TextColor(color).SingleLine().Shrink(1).MinWidth(0)
		ui.Icon(c, iconUpDown).Size(11, 11).TextColor(p.faint)
	})
	pop := popoverColors(p.dark)
	sel.Popup(func(panel ui.Element) {
		panel.Margin(6, 0, 0, 0).Padding(5).Gap(1).MinWidth(180).MaxWidth(sidebarWidth+60).
			Radius(12).Background(pop.fill).Border(0.5, pop.edge).Shadow(0, 8, 24, -4, ui.RGBA(0, 0, 0, 0.2))
		for _, lib := range a.libs {
			name := lib.name()
			item := sel.Item(lib.id).Label(name).Height(30).PaddingX(8).Gap(8).Radius(7).AlignItems(ui.Center)
			if item.Highlighted() {
				item.Background(p.press)
			}
			item.Children(func() {
				check := ui.Icon(c, iconCheck).Size(14, 14).TextColor(p.text).Shrink(0)
				if lib != a.library {
					check.Opacity(0)
				}
				ui.Text(c, name).FontSize(13).TextColor(p.text).SingleLine().Shrink(1).MinWidth(0)
			})
		}
	})
	if b.Changed() {
		a.switchSource(chosen)
	}
}
