package app

import (
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// showData is what the app has of a show: what it is, and its episodes, as far
// as they have loaded. An episode is a track, which plays like any other.
type showData struct {
	show        *music.Show
	episodes    []music.Track
	page        paged
	subscribing bool // a subscription or its end is on its way
	thenPlay    bool // its card's play button waits for it to load
}

// loadShow loads a show's page, unless it has.
func (a *app) loadShow(lib *library, id string) {
	d := lib.shows[id]
	if d == nil {
		d = &showData{}
		lib.shows[id] = d
	}
	if !d.page.loaded {
		a.loadEpisodes(lib, id)
	}
}

// loadEpisodes loads the next page of a show's episodes.
func (a *app) loadEpisodes(lib *library, id string) {
	d := lib.shows[id]
	if d.page.loading {
		return
	}
	d.page.loading, d.page.err = true, ""
	offset := len(d.episodes)
	go func() {
		page, err := answered(lib.src.Show(id, offset))
		a.post(func() {
			d.page.loading = false
			if err != nil {
				d.page.err, d.thenPlay = a.problem(lib, err, T("Couldn't load this podcast")), false
				return
			}
			show := page.Show
			d.show = &show
			d.episodes = append(d.episodes, page.Episodes...)
			d.page.loaded, d.page.more = true, page.More && len(page.Episodes) > 0
			a.playLoaded(d)
		})
	}()
}

// playEpisodes plays the episodes loaded, from the first that can play.
func (a *app) playEpisodes(d *showData) {
	if len(d.episodes) > 0 {
		a.pl.PlayQueue(d.episodes, 0)
	}
}

// playShow plays a show's episodes from its first, from its card, once it and
// they have loaded.
func (a *app) playShow(lib *library, id string) {
	a.loadShow(lib, id)
	d := lib.shows[id]
	d.thenPlay = true
	a.playLoaded(d)
}

// playLoaded plays a show its card asked to play, once it can.
func (a *app) playLoaded(d *showData) {
	if d.thenPlay && d.show != nil && d.page.loaded {
		d.thenPlay = false
		a.playEpisodes(d)
	}
}

// toggleSubscribe subscribes the listener to a show, or unsubscribes them, at
// once, going back if the source refuses. Signed out, it asks the listener to
// sign in.
func (a *app) toggleSubscribe(lib *library, d *showData) {
	if d.show == nil || d.subscribing {
		return
	}
	if lib.signedOut() {
		a.promptSignIn(lib)
		return
	}
	show := d.show
	sub := !show.Subscribed
	show.Subscribed, d.subscribing = sub, true
	id := show.ID
	go func() {
		err := lib.src.SubscribeShow(id, sub)
		a.post(func() {
			d.subscribing = false
			switch {
			case err != nil:
				show.Subscribed = !sub
				a.actionFailed(lib, err, T("That didn't work. Try again later."))
			case sub:
				lib.subscribed = append([]music.Show{*show}, slices.DeleteFunc(lib.subscribed, func(s music.Show) bool { return s.ID == id })...)
				a.toast = T("Subscribed")
			default:
				lib.subscribed = slices.DeleteFunc(lib.subscribed, func(s music.Show) bool { return s.ID == id })
				a.toast = T("Unsubscribed")
			}
		})
	}()
}

func (a *app) showPage(c *ui.Context) {
	p := a.pal
	id := a.route.id
	lib := a.library
	d := lib.shows[id]
	if d == nil {
		a.loadShow(lib, id)
		d = lib.shows[id]
	}
	if d.show == nil {
		if !d.page.loading {
			a.centered(c, d.page.err, func() { a.loadShow(lib, id) })
			return
		}
		s := a.pageShimmer(c)
		a.trackSkeleton(c, s, true, func(c *ui.Context) { a.playlistHeaderSkeleton(c, s) })
		return
	}
	show := d.show
	cur := a.pl.Current()
	a.scrollPage(c, func() {
		ui.Row(c).Gap(28).AlignItems(ui.End).Padding(0, 0, 8, 0).Children(func() {
			ui.Box(c).Size(212, 212).Radius(14).Shadow(0, 18, 40, -10, p.cardShade).Children(func() {
				a.coverFill(c, show.ImageURL, 600, 14)
			})
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Children(func() {
				ui.Text(c, T("Podcast")).FontSize(12).FontWeight(600).TextColor(p.accent)
				ui.Text(c, show.Name).FontSize(30).FontWeight(800).LetterSpacing(-0.6).MaxLines(2).LineHeight(1.2)
				// Who publishes it opens their page, if the source has one.
				ui.RichText(c).FontSize(13).TextColor(p.muted).Children(func() {
					if show.Publisher != "" {
						if show.PublisherPage != "" {
							a.link(c.Key("publisher"), show.Publisher, p.muted, p.text, route{page: pageBrowse, id: show.PublisherPage})
						} else {
							ui.Text(c, show.Publisher)
						}
						if show.EpisodeCount > 0 {
							ui.Text(c, " · ")
						}
					}
					if n := show.EpisodeCount; n > 0 {
						ui.Text(c, episodeCount(n))
					}
				})
				if desc := strings.TrimSpace(show.Description); desc != "" {
					ui.Text(c, strings.ReplaceAll(desc, "\n", " ")).FontSize(12.5).TextColor(p.muted).MaxLines(2).LineHeight(1.45).Margin(2, 0, 0, 0)
				}
				ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Children(func() {
					if pillButton(c.Key("play"), iconPlay, T("Play"), p.accent, p.onAccent).Clicked() {
						a.playEpisodes(d)
					}
					if a.can("subscribeShow") {
						icon, label := iconPlus, T("Subscribe")
						if show.Subscribed {
							icon, label = iconCheck, T("Subscribed")
						}
						if pillButton(c.Key("subscribe"), icon, label, p.hover.Over(p.bg), p.text).Clicked() {
							a.toggleSubscribe(lib, d)
						}
					}
				})
			})
		})
		a.section(c, T("Episodes"))
		for i := range d.episodes {
			a.episodeRow(c.Key(d.episodes[i].ID), d, i, cur)
		}
		a.pageFoot(c, &d.page, func() { a.loadEpisodes(lib, id) })
		if d.page.loaded && len(d.episodes) == 0 {
			a.empty(c, T("No episodes yet"))
		}
	})
}

// episodeCount says how many episodes there are.
func episodeCount(n int) string {
	if n == 1 {
		return T("1 episode")
	}
	return Tf("%d episodes", n)
}

// episodeRow is an episode in its show's list, which a click plays.
func (a *app) episodeRow(c *ui.Context, d *showData, i int, cur *music.Track) {
	p := a.pal
	tr := &d.episodes[i]
	playing := sameTrack(cur, tr)
	row := ui.ButtonBase(c).Height(60).PaddingX(12).Gap(14).Radius(8).AlignItems(ui.Center).FocusRing(false).
		Label(tr.Name).Disabled(tr.Unavailable).ContextMenu(a.songItems(tr, ""))
	switch {
	case playing:
		row.Background(p.accent.Alpha(0.08))
	case row.Hovered():
		row.Background(p.hover)
	}
	a.wantLike(tr)
	row.Children(func() {
		cover := tr.Album.ImageURL
		if cover == "" {
			cover = d.show.ImageURL
		}
		a.cover(c, cover, 96, 40, 6)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(3).Children(func() {
			title := ui.Text(c, tr.Name).FontSize(13.5).FontWeight(500).SingleLine()
			if playing {
				title.TextColor(p.accent)
			}
			if tr.Published != "" {
				ui.Text(c, tr.Published).FontSize(12).TextColor(p.muted)
			}
		})
		if playing {
			a.nowPlayingBars(c)
		}
		// A liked episode's heart shows; the others' as the pointer is on them.
		ui.Box(c).Size(32, 32).Center().Children(func() {
			if a.isLiked(tr) || row.Hovered() {
				a.likeButton(c.Key("like"), tr, 15, p.muted)
			}
		})
		ui.Text(c, clock(time.Duration(tr.Duration)*time.Millisecond)).FontSize(12).FontFeatures("tnum").TextColor(p.muted)
	})
	if row.Clicked() {
		a.pl.PlayTrack(d.episodes, i)
	}
}

// showRow is a show in a list, opening its page.
func (a *app) showRow(c *ui.Context, show *music.Show) {
	p := a.pal
	row := ui.ButtonBase(c).Padding(10, 12).Gap(14).Radius(10).AlignItems(ui.Center).FocusRing(false).Label(show.Name)
	if row.Hovered() {
		row.Background(p.hover)
	}
	row.Children(func() {
		a.cover(c, show.ImageURL, 160, 64, 8)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, show.Name).FontSize(14).FontWeight(600).SingleLine()
			var meta []string
			if show.Publisher != "" {
				meta = append(meta, show.Publisher)
			}
			if show.EpisodeCount > 0 {
				meta = append(meta, episodeCount(show.EpisodeCount))
			}
			ui.Text(c, strings.Join(meta, " · ")).FontSize(12.5).TextColor(p.muted).SingleLine()
			if show.Description != "" {
				ui.Text(c, show.Description).FontSize(12.5).TextColor(p.muted).SingleLine()
			}
		})
	})
	if row.Clicked() {
		a.open(route{page: pageShow, id: show.ID})
	}
}

// showPane is what the episode playing is about, in Now Playing, in place of a
// song's lyrics.
func (a *app) showPane(c *ui.Context, tr *music.Track) {
	ui.Scroll(c.Key(tr.ID)).Grow(1).Padding(4, 12, 48, 0).Children(func() {
		desc := strings.TrimSpace(tr.Description)
		if desc == "" {
			desc = T("This episode has no description")
		}
		ui.Text(c, desc).FontSize(15).LineHeight(1.7).TextColor(white.Alpha(0.75))
	})
}

// loadSubscribed loads the shows the listener subscribes to, once.
func (a *app) loadSubscribed(lib *library) {
	if lib.subscribedOnce || lib.subscribedBusy {
		return
	}
	lib.subscribedBusy, lib.subscribedErr = true, ""
	go func() {
		shows, err := lib.src.Shows()
		a.post(func() {
			lib.subscribedBusy = false
			if err != nil {
				lib.subscribedErr = a.problem(lib, err, T("Couldn't load your podcasts"))
				return
			}
			lib.subscribed, lib.subscribedOnce = shows, true
		})
	}()
}

// showsPage lists the shows the listener subscribes to.
func (a *app) showsPage(c *ui.Context) {
	lib := a.library
	if !lib.subscribedOnce && !lib.subscribedBusy {
		a.centered(c, lib.subscribedErr, func() { a.loadSubscribed(lib) })
		return
	}
	top, bottom := a.insets(c)
	ui.Scroll(c.Key(a.route)).TrackScroll(&a.pageScroll).Grow(1).Padding(top+pageGap, pagePad, bottom+48, pagePad).ScrollbarInsets(top, 0, bottom, 0).Children(func() {
		a.startMark(c)
		defer a.endMark(c)
		a.heading(c, a.builtinTitle("shows"), "")
		cols := a.columns(c, 150)
		if !lib.subscribedOnce {
			a.playlistCardsSkeleton(c, a.pageShimmer(c), cols*2, 150, true)
			return
		}
		if len(lib.subscribed) == 0 {
			a.empty(c, T("Podcasts you subscribe to will show here"))
			return
		}
		ui.Grid(c).Columns(cols).GapX(20).GapY(28).Children(func() {
			for i := range lib.subscribed {
				a.showCard(c.Key(lib.subscribed[i].ID), &lib.subscribed[i])
			}
		})
	})
}

// showCard is a show's cover over its name and who publishes it.
func (a *app) showCard(c *ui.Context, show *music.Show) {
	id := show.ID
	lib := a.library
	if a.coverCardWith(c, show.Name, show.ImageURL, iconPodcast, show.Publisher, func() { a.playShow(lib, id) }) {
		a.open(route{page: pageShow, id: id})
	}
}
