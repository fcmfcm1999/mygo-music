package app

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/i18n"
	"mygo-music/internal/music"
)

// albumData is what the app has of an album.
type albumData struct {
	album   *music.AlbumPage
	loading bool
	saving  bool
	err     string
}

func (a *app) loadAlbum(lib *library, id string) {
	d := lib.albums[id]
	if d == nil {
		d = &albumData{}
		lib.albums[id] = d
	}
	if d.album != nil || d.loading {
		return
	}
	d.loading, d.err = true, ""
	go func() {
		al, err := answered(lib.src.Album(id))
		a.post(func() {
			d.loading = false
			if err != nil {
				d.err = a.problem(lib, err, T("Couldn't load this album"))
				return
			}
			d.album = al
		})
	}()
}

func (a *app) albumPage(c *ui.Context) {
	id := a.route.id
	lib := a.library
	d := a.albums[id]
	if d == nil {
		a.loadAlbum(lib, id)
		d = a.albums[id]
	}
	if d.album == nil {
		if !d.loading {
			a.centered(c, d.err, func() { a.loadAlbum(lib, id) })
			return
		}
		s := a.pageShimmer(c)
		a.trackSkeleton(c, s, false, func(c *ui.Context) { a.playlistHeaderSkeleton(c, s) })
		return
	}
	a.trackRows(c, d.album.Tracks, false, func(c *ui.Context) { a.albumHeader(c, d) })
}

func (a *app) albumHeader(c *ui.Context, d *albumData) {
	p := a.pal
	al := &d.album.Album
	tracks := d.album.Tracks
	ui.Row(c).Gap(28).AlignItems(ui.End).Padding(0, 0, 26, 0).Children(func() {
		ui.Box(c).Size(212, 212).Radius(14).Shadow(0, 18, 40, -10, p.cardShade).Children(func() {
			a.coverFill(c, al.ImageURL, 600, 14)
		})
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Children(func() {
			ui.Text(c, T("Album")).FontSize(12).FontWeight(600).TextColor(p.accent)
			ui.Text(c, al.Name).FontSize(30).FontWeight(800).LetterSpacing(-0.6).MaxLines(2).LineHeight(1.2)
			ui.RichText(c).FontSize(13).TextColor(p.muted).Children(func() {
				for i, ar := range al.Artists {
					if i > 0 {
						ui.Text(c, ", ")
					}
					if ar.ID != "" && a.can("artist") {
						a.link(c.Key(i), ar.Name, p.muted, p.text, route{page: pageArtist, id: ar.ID})
					} else {
						ui.Text(c, ar.Name)
					}
				}
				meta := []string{i18n.Songs(len(tracks))}
				if al.Released != "" {
					meta = append([]string{al.Released}, meta...)
				}
				sep := ""
				if len(al.Artists) > 0 {
					sep = " · "
				}
				ui.Text(c, sep+strings.Join(meta, " · "))
			})
			ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Children(func() {
				a.playButtons(c, tracks)
				if a.can("saveAlbum") {
					icon, label := iconPlus, TC("Save", "library")
					if al.Saved {
						icon, label = iconCheck, T("Saved")
					}
					if pillButton(c.Key("save"), icon, label, p.hover.Over(p.bg), p.text).Clicked() {
						a.toggleAlbumSaved(a.library, d)
					}
				}
			})
		})
	})
}

// toggleAlbumSaved saves an album or lets it go, at once in the app and then
// with its source, going back if it refuses.
func (a *app) toggleAlbumSaved(lib *library, d *albumData) {
	if lib.signedOut() {
		a.promptSignIn(lib)
		return
	}
	if d.saving {
		return
	}
	al := &d.album.Album
	save := !al.Saved
	al.Saved, d.saving = save, true
	id := al.ID
	go func() {
		err := lib.src.SaveAlbum(id, save)
		a.post(func() {
			d.saving = false
			if err != nil {
				al.Saved = !save
				a.actionFailed(lib, err, T("That didn't work. Try again later."))
				return
			}
			// Their page lists it, or doesn't, once it loads anew.
			lib.savedAlbums, lib.savedAlbumsPaged = nil, paged{}
			if a.route.page == pageSavedAlbums && a.route.src == lib.id {
				a.loadSavedAlbums(lib)
			}
		})
	}()
}

// loadSavedAlbums loads the next page of the albums the listener saved.
func (a *app) loadSavedAlbums(lib *library) {
	if lib.savedAlbumsPaged.loading {
		return
	}
	lib.savedAlbumsPaged.loading, lib.savedAlbumsPaged.err = true, ""
	offset := len(lib.savedAlbums)
	go func() {
		page, err := answered(lib.src.SavedAlbums(offset))
		a.post(func() {
			lib.savedAlbumsPaged.loading = false
			if err != nil {
				lib.savedAlbumsPaged.err = a.problem(lib, err, T("Couldn't load your albums"))
				return
			}
			lib.savedAlbums = append(lib.savedAlbums, page.Albums...)
			lib.savedAlbumsPaged.loaded, lib.savedAlbumsPaged.more = true, page.More && len(page.Albums) > 0
		})
	}()
}

// savedAlbumsPage lists the albums the listener saved.
func (a *app) savedAlbumsPage(c *ui.Context) {
	lib := a.library
	a.scrollPage(c, func() {
		ui.Box(c).PaddingX(12).Children(func() { a.heading(c, T("Saved Albums"), "") })
		ui.Box(c).PaddingX(12).Children(func() { a.albumGrid(c, lib.savedAlbums) })
		a.pageFoot(c, &lib.savedAlbumsPaged, func() { a.loadSavedAlbums(lib) })
		if lib.savedAlbumsPaged.loaded && len(lib.savedAlbums) == 0 {
			a.empty(c, T("Albums you save will show here"))
		}
	})
}

// albumGrid shows albums as cards, each opening its page.
func (a *app) albumGrid(c *ui.Context, albums []music.Album) {
	ui.Grid(c).Columns(a.columns(c, 156)).GapX(20).GapY(28).Children(func() {
		for i := range albums {
			a.albumCard(c.Key(albums[i].ID), &albums[i])
		}
	})
}

// artistNames joins the names of an album's artists.
func artistNames(artists []music.Artist) string {
	names := make([]string, len(artists))
	for i, ar := range artists {
		names[i] = ar.Name
	}
	return strings.Join(names, ", ")
}

// albumCard is an album's cover, lifting under the pointer, over its name
// and the year it came out.
func (a *app) albumCard(c *ui.Context, al *music.Album) {
	p := a.pal
	card := ui.ButtonBase(c).Column().Gap(10).AlignItems(ui.Stretch).FocusRing(false).Label(al.Name)
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
		art.Children(func() { a.coverFill(c, al.ImageURL, 400, 10) })
		ui.Text(c, al.Name).FontSize(13).FontWeight(500).MaxLines(2).LineHeight(1.35)
		var meta []string
		if a.route.page != pageArtist && len(al.Artists) > 0 {
			meta = append(meta, artistNames(al.Artists))
		}
		if len(al.Released) >= 4 {
			meta = append(meta, al.Released[:4])
		}
		ui.Text(c, strings.Join(meta, " · ")).FontSize(12).TextColor(p.muted).SingleLine().Margin(-6, 0, 0, 0)
	})
	if card.Clicked() && a.can("album") {
		a.open(route{page: pageAlbum, id: al.ID})
	}
}
