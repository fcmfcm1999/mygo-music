package app

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-music/internal/lyrics"
	"mygo-music/internal/music"
)

var white = ui.RGB(255, 255, 255)

// npSpring is how Now Playing comes and goes, sliding or growing out of the
// player bar's cover and shrinking back into it, and how it springs back
// from a pull too short to close it.
var npSpring = ui.SpringOptions{Duration: 340 * time.Millisecond}

// npFade is how Now Playing comes and goes while the desktop asks for less
// motion: it fades, in place.
var npFade = ui.Animation{Duration: 200 * time.Millisecond, NonMotion: true}

// npState tracks Now Playing opening and closing, and reports whether it is
// open and whether it has come to rest, open or closed, as it last showed.
// Call it once a build, before the window's content.
func (a *app) npState(c *ui.Context) (open, settled bool) {
	open = a.nowPlaying && a.pl.Current() != nil
	if open != a.npOpen {
		a.npOpen = open
		// The window buttons light over its dark backdrop, and as the
		// theme over the pages (Windows).
		if a.win != nil {
			buttons := mygo.ThemeSystem
			if open {
				buttons = mygo.ThemeDark
			}
			a.win.SetWindowButtonsTheme(buttons)
		}
	}
	return open, a.npAtRest(open)
}

// npAtRest reports whether Now Playing showed at rest: all open, with
// nothing pulling on it, or all gone.
func (a *app) npAtRest(open bool) bool {
	if open {
		return a.npT >= 1 && a.npShift == 0 && !a.npHeld
	}
	return a.npT <= 0
}

// nowPlayingLayer holds Now Playing over the whole window. It slides up
// from below the window as it opens, as it is, and back down out of it as
// it closes; with less motion, it only fades. Growing out of the cover, an
// experiment the listener turns on, it grows out of the cover in the
// player bar as it opens, a card that takes the window, the cover flying
// out of the bar to its place, and shrinks back into it as it closes.
// Pulled down, it follows the pointer, growing out of the cover smaller and
// rounder the farther it goes, and let go far or fast enough it falls the
// rest of the way; otherwise it springs back. The layer stays on top all
// the way, rather than leaving as an exit transition would, under the rest
// of the window: that would show it going over the sidebar's material
// while the page above it hid it at once.
func (a *app) nowPlayingLayer(c *ui.Context, open, settled bool) {
	w, h := c.Size()
	layer := ui.Box(c.Key("now-playing-layer")).Absolute().Top(0).Left(0).Right(0).Bottom(0)
	// Unless it grows out of the cover, an experiment, it slides up, or
	// with less motion only fades.
	reduce := c.Preferences().ReduceMotion
	a.npStill = reduce || !a.settings.GrowFromCover
	slide := a.npStill && !reduce
	spring := npSpring
	_, dy, held := layer.Dragged()
	held = held && open
	if held {
		a.npPull += dy
	}
	if _, vy, ok := layer.DragEnded(); ok && open {
		if a.npPull > h*0.2 || a.npPull > 24 && vy > 700 {
			// Let go far enough, or thrown: it falls the rest of the way
			// as fast as it was thrown.
			a.nowPlaying, open = false, false
			spring.Velocity = -max(vy, 0) / h
		}
	}
	if !held {
		a.npPull = 0
	}
	a.npHeld = held

	target := float32(0)
	if open {
		target = 1
	}
	var t, shift float32
	if reduce {
		t = layer.AnimateTo("open", target, npFade)
	} else {
		t = layer.AnimateTo("open", target, ui.Animation{Spring: spring})
	}
	if held {
		// Pulled up, it gives less and less.
		shift = layer.Jump("pull", ui.Rubberband(a.npPull, 0, h, h))
	} else {
		shift = layer.AnimateTo("pull", 0, ui.Animation{Spring: npSpring})
	}
	fresh, was := a.npT <= 0 && t > 0, a.npT
	a.npT, a.npShift = t, shift
	if a.npAtRest(open) != settled {
		// Another build, for the page under it to come or go, and the
		// bar's cover with it.
		c.AnimationFrame()
	}
	// Back in the bar, it shows a frame more, its cover over the bar's:
	// the bar, built before it, hid its own cover in this frame, and
	// shows it from the next.
	if t <= 0 && !open && was <= 0 || a.pl.Current() == nil {
		// Closed: nothing to paint or click.
		layer.Invisible()
		a.queueAt, a.wallState.track = -1, ""
		return
	}
	switch {
	case fresh:
		// Its first frame lays it out, for what flies to its place to know
		// where that is; it shows from the next.
		layer.Opacity(0)
		c.AnimationFrame()
	case slide:
		// It follows a pull down, as it does growing out of the cover.
		if k := 1 - clamp01(t); k > 0 || shift != 0 {
			layer.Translate(0, h*k+shift)
		}
	case a.npStill:
		if t < 1 {
			layer.Opacity(max(t, 0))
		}
	default:
		a.npCard(layer, t, shift, w, h)
	}
	if !open {
		// On its way out, the window under it takes the clicks.
		layer.PassThrough()
	}
	layer.Children(func() { a.nowPlayingView(c) })
}

// npCard clips the layer to the card Now Playing is, t of the way open,
// from the player bar's cover to the window, and moves it shift down, a
// little smaller and rounder for it.
func (a *app) npCard(layer ui.Element, t, shift, w, h float32) {
	k := 1 - clamp01(t) // how near the bar's cover it is
	pulled := clamp01(shift / h)
	if k == 0 && shift == 0 {
		return
	}
	from := a.npFromRect(w, h)
	radius := npFromRadius*k + 28*clamp01(shift/120)
	layer.ClipInset(from.Y*k, (w-from.X-from.W)*k, (h-from.Y-from.H)*k, from.X*k, radius)
	if shift != 0 {
		s := 1 - 0.12*pulled
		layer.TransformOrigin(0.5, 0).Scale(s, s).Translate(0, shift)
	}
}

// npFromRect is the player bar's cover, in a window w by h, that Now
// Playing grows out of: where the bar last showed it, or where the bar
// would, before it has.
func (a *app) npFromRect(w, h float32) ui.Rect {
	if a.npFrom.W > 0 {
		return a.npFrom
	}
	return ui.Rect{X: sidebarWidth + 21, Y: h - (playerBarHeight+48)/2, W: 48, H: 48}
}

// npMorph moves e, a cover of Now Playing, out of the player bar's cover
// to its place as Now Playing opens, and back into it as it closes: the
// song in the bar grows into the view.
func (a *app) npMorph(c *ui.Context, e ui.Element) ui.Element {
	if a.npT >= 1 || a.npStill {
		return e
	}
	b := e.LayoutBounds()
	if b.W <= 0 {
		// Not laid out yet: the next frame places it.
		c.AnimationFrame()
		return e.Opacity(0)
	}
	w, h := c.Size()
	from := a.npFromRect(w, h)
	k := 1 - a.npT
	s := 1 + (from.W/b.W-1)*k
	return e.TransformOrigin(0, 0).Scale(s, s).Translate((from.X-b.X)*k, (from.Y-b.Y)*k)
}

// npBreathe scales e, a cover that breathes, to scale: there as a pose
// moves it, while Now Playing is open, and at once on its way to or from
// the bar, coming to its full size as it gets there, the size of the
// bar's cover it takes the place of.
func (a *app) npBreathe(e ui.Element, scale float32) {
	if a.npT < 1 && !a.npStill {
		s := lerp(1, scale, clamp01(a.npT))
		e.Scale(s, s)
		return
	}
	e.AnimatePose(ui.Pose{Scale: scale}, ui.Animation{Spring: ui.SpringOptions{Duration: 600 * time.Millisecond, Bounce: 0.25}})
}

// npRadius is the corner radius, r at rest, of a cover size across that
// npMorph moves: its corners, shrinking with it, stay as round on screen
// as they are on its way to the bar's cover's, and are that round as it
// gets there, rather than going sharp and rounding at once as the bar's
// cover takes its place.
func (a *app) npRadius(c *ui.Context, r, size float32) float32 {
	if a.npT >= 1 || a.npStill {
		return r
	}
	w, h := c.Size()
	k := 1 - clamp01(a.npT)
	s := 1 + (a.npFromRect(w, h).W/size-1)*k // as npMorph scales it
	return min(lerp(r, npFromRadius, k)/s, size/2)
}

// npFromRadius is the corner radius of the player bar's cover.
const npFromRadius = 8

// npRise fades e in and lifts it into place as Now Playing opens, once the
// cover is on its way: the nth of the things that do, one after another,
// going again the other way as it closes.
func (a *app) npRise(e ui.Element, n int) ui.Element {
	if a.npT >= 1 || a.npStill {
		return e
	}
	from := 0.3 + 0.07*float32(n)
	k := smoothstep(from, from+0.4, a.npT)
	return e.Opacity(k).Translate(0, 24*(1-k))
}

// nowPlayingView fills the window with what plays: its cover over a slow
// drift of its own colors, the controls, and lyrics that follow the song.
func (a *app) nowPlayingView(c *ui.Context) {
	pl := a.pl
	tr := pl.Current()
	w, h := c.Size()
	art := a.art.get(sized(tr.Album.ImageURL, 600), true)
	// The colors stay the last song's while its cover loads, rather than
	// going gray between the two.
	shown := art
	if art == nil && tr.Album.ImageURL != "" {
		shown = a.npArt
	}
	a.npArt = shown
	tint := ui.RGB(120, 120, 130)
	if shown != nil && !shown.failed {
		tint = shown.tint
	}
	accent := tint.Mix(white, 0.45)

	// Near the bar, the card is only its cover, the rest gone clear.
	bg := ui.Hex("#0b0b0d")
	if t := a.npT; t < 1 && !a.npStill {
		bg = bg.Alpha(smoothstep(0, 0.35, t))
	}
	root := ui.Box(c.Key("now-playing")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(bg).TextColor(white)
	// The pointer is over the view whenever it is in the window.
	pointerIn := root.Hovered()
	root.Children(func() {
		// Its colors bloom out of the bar's cover as it opens.
		bloom := ui.Box(c.Key("bloom")).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough()
		if t := a.npT; t < 1 && !a.npStill {
			from := a.npFromRect(w, h)
			s := 1 + 0.3*(1-clamp01(t))
			bloom.Opacity(smoothstep(0.05, 0.55, t)).
				TransformOrigin((from.X+from.W/2)/w, (from.Y+from.H/2)/h).Scale(s, s)
		}
		bloom.Children(func() { a.backdrop(c, shown, tint) })
		// Over the backdrop, which is positioned too: positioned elements
		// paint over those in the flow.
		ui.Column(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Children(func() { a.nowPlayingBody(c, tr, art, accent, w, h, pointerIn) })
	})
}

func (a *app) nowPlayingBody(c *ui.Context, tr *music.Track, art *artwork, accent ui.Color, w, h float32, pointerIn bool) {
	pl := a.pl
	a.commentsFor(tr)
	// An episode has what it is about and its captions in place of lyrics; a
	// song, lyrics in place of those. Comments are for a source that has them.
	switch episode := tr.ShowID != ""; {
	case episode && a.npPane == paneWall:
		a.npPane = paneQueue
	case episode && (a.npPane == paneLyrics || a.npPane == paneStage):
		a.npPane = paneShow
		if a.captionsOn() {
			a.npPane = paneCaptions
		}
	case episode && a.npPane == paneCaptions && !a.captionsShown():
		a.npPane = paneShow
	case !episode && (a.npPane == paneShow || a.npPane == paneCaptions):
		a.npPane = a.lyricsPaneChosen()
	}
	if a.npPane == paneComments && !a.trackLib(tr).can("comments") {
		a.npPane = paneQueue
	}
	if a.npPane != paneQueue {
		a.queueAt = -1
	}
	if a.npPane != paneWall {
		a.wallState.track = ""
	}
	{
		// The top strip drags the window; the chevron closes the view,
		// showing only while the pointer is in the window, as the pane tabs do.
		a.npRise(ui.Row(c).Height(topBar(c)).Justify(ui.Center).AlignItems(ui.Center).DragWindow(), 0).Children(func() {
			reveal(ui.Box(c.Key("close-reveal")), pointerIn).Children(func() {
				if iconButton(c.Key("close"), iconDown, 22, white.Alpha(0.7), T("Close")).Clicked() {
					a.nowPlaying = false
				}
			})
		})
		if a.npPane == paneStage {
			// The stage takes the whole view: the lyrics are the show, the
			// controls a slim bar at the foot.
			a.npRise(reveal(npTabsRow(c, w), pointerIn), 0).Children(func() {
				a.paneTabs(c)
				a.stageStyleButton(c)
			})
			a.stage(c, tr, accent)
			a.stageBar(c, tr, art, accent)
			return
		}
		if a.npPane == paneWall {
			// The wall takes the whole view too, under the song playing.
			a.wallPane(c, tr, art, accent, w, h, pointerIn)
			return
		}

		side := float32(math.Min(float64(w)*0.36, 440))
		// The cover leaves room above it for the tabs, starting as far
		// under them as the wall's song does.
		coverSize := min(side, h-436)
		// The column is as wide as the cover, for the cover, what plays
		// and the tabs over it to start at one edge, but wide enough for
		// the controls.
		side = max(coverSize, 280)
		padX := npSideX(w)
		a.npRise(reveal(npTabsRow(c, w), pointerIn), 0).Children(func() { a.paneTabs(c) })
		ui.Row(c).Grow(1).Padding(0, padX, 44, padX).Gap(72).AlignItems(ui.Stretch).Children(func() {
			var coverTop float32
			ui.Column(c).Width(side).Justify(ui.Center).Gap(0).Children(func() {
				coverTop = ui.Box(c.Key("cover-top")).Height(0).LayoutBounds().Y
				// The cover flies out of the bar's as Now Playing opens.
				a.npMorph(c, ui.Box(c.Key("cover-morph")).Size(coverSize, coverSize).AlignSelf(ui.Start)).Children(func() {
					a.npCover(c, tr, art, coverSize)
				})

				a.npRise(ui.Row(c).Gap(8).AlignItems(ui.Center).Margin(36, 0, 18, 0), 1).Children(func() {
					ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(4).Children(func() {
						if r := pl.Radio(); r != "" {
							a.radioChip(c, r)
						}
						ui.Text(c, tr.Name).FontSize(22).FontWeight(700).LetterSpacing(-0.3).SingleLine()
						a.artistLinks(c, tr, white.Alpha(0.6), white, true).FontSize(15).SingleLine()
					})
					a.likeButton(c.Key("like"), tr, 22, white.Alpha(0.7))
					a.songMenu(c.Key("more"), tr, 22, white.Alpha(0.7)).Margin(0, -8, 0, 0)
				})
				a.npRise(a.scrubber(c.Key("np-scrub"), &a.npScrub, white.Alpha(0.18), white.Alpha(0.9), 16, accent.Mix(white, 0.35)), 2)
				// As tall with the quality's badge as without it: the badge goes
				// while the next song loads, and the column would move with it.
				a.npRise(ui.Row(c).Height(16).AlignItems(ui.Center), 2).Children(func() {
					ui.Text(c, clock(pl.Position())).FontSize(11).FontFeatures("tnum").TextColor(white.Alpha(0.5))
					ui.Spacer(c)
					qualityBadge(c, pl.Stream())
					ui.Spacer(c)
					status := "-" + clock(pl.Duration()-pl.Position())
					if pl.Loading() {
						status = T("Buffering…")
					}
					ui.Text(c, status).FontSize(11).FontFeatures("tnum").TextColor(white.Alpha(0.5))
				})
				a.npRise(ui.Box(c).Margin(14, 0, 18, 0), 3).Children(func() {
					a.transport(c, white, white.Alpha(0.45), accent, 28)
				})
				a.npRise(ui.Row(c).Justify(ui.Center), 4).Children(func() {
					a.volumeSlider(c, white.Alpha(0.7), white.Alpha(0.18), 180)
				})
			})

			pane := ui.Column(c.Key("pane")).Grow(1).Basis(0).MinWidth(0)
			a.npRise(pane, 2)
			pane.Children(func() {
				// The pane starts level with the cover beside it, but below the
				// tabs, which start level with the pane's top: they come and go
				// with the pointer, so they sit out of the flow, never over it.
				b := pane.LayoutBounds()
				if b.H > 0 && coverTop > 0 {
					if top := max(coverTop-b.Y, npTabsHeight+8); top != a.npPaneTop {
						a.npPaneTop = top
						c.AnimationFrame()
					}
				} else {
					c.AnimationFrame() // not laid out yet: the next frame places it
				}
				ui.Box(c).Height(a.npPaneTop)
				switch a.npPane {
				case paneLyrics:
					a.lyricsPane(c, tr, h)
				case paneComments:
					a.commentsPane(c, tr)
				case paneShow:
					a.showPane(c, tr)
				case paneCaptions:
					a.captionsPane(c, tr, h)
				default:
					a.queuePane(c)
				}
			})
		})
	}
}

// npMaxWidth is how wide the cover and the pane beside it go, at most.
const npMaxWidth = 1280

// npSideX is how far in from the sides of a view w wide the cover and the
// pane beside it are: in a wide window they keep together in the middle,
// rather than the pane taking all the width the cover leaves.
func npSideX(w float32) float32 {
	return max(72, (w-npMaxWidth)/2)
}

// npTabsHeight is how tall the row of Now Playing's tabs is, for what is
// under it to start below it.
const npTabsHeight = 28

// npTabsRow is the row of Now Playing's tabs, in the same place for every
// pane, so that they stay put going from one to another: at the top, level
// with the cover's left. They start from the left, so that only the last,
// the comments, moves as its count changes.
func npTabsRow(c *ui.Context, w float32) ui.Element {
	return ui.Row(c.Key("tabs")).Absolute().Top(topBar(c)).Left(npSideX(w)).Height(npTabsHeight).Gap(6).AlignItems(ui.Center)
}

// reveal fades e in while the pointer is in the window, and out when it
// leaves.
func reveal(e ui.Element, shown bool) ui.Element {
	pose := ui.Pose{Fade: 1}
	if shown {
		pose = ui.Pose{}
	}
	return e.AnimatePose(pose, ui.Animation{Duration: 200 * time.Millisecond})
}

// lyricsPaneChosen is the pane of lyrics the listener chose last: what
// the app starts on, before the player is made. Now Playing shows what an
// episode is about in place of either.
func (a *app) lyricsPaneChosen() int {
	if a.settings.LyricsStage {
		return paneStage
	}
	return paneLyrics
}

// The panes of Now Playing.
const (
	paneLyrics = iota
	paneQueue
	paneStage
	paneComments
	paneShow
	paneCaptions
	paneWall
)

// npTab is a tab of Now Playing: its pane, and what it says.
type npTab struct {
	pane  int
	label string
}

// npTabs returns the tabs of Now Playing, in order, for what plays: ⌘1
// chooses the first, ⌘2 the second, and on.
func (a *app) npTabs() []npTab {
	tr := a.pl.Current()
	var tabs []npTab
	if tr != nil && tr.ShowID != "" {
		tabs = []npTab{{paneShow, T("About")}}
		if a.captionsShown() {
			tabs = append(tabs, npTab{paneCaptions, T("Captions")})
		}
		tabs = append(tabs, npTab{paneQueue, T("Up Next")})
	} else {
		tabs = []npTab{{paneLyrics, T("Lyrics")}, {paneStage, T("Stage")}, {paneQueue, T("Up Next")}, {paneWall, T("Wall")}}
	}
	if tr != nil && a.trackLib(tr).can("comments") {
		tabs = append(tabs, npTab{paneComments, T("Comments")})
	}
	return tabs
}

func (a *app) paneTabs(c *ui.Context) {
	for _, t := range a.npTabs() {
		a.paneTab(c, t.pane, t.label)
	}
}

// choosePane shows pane i in Now Playing, remembering lyrics or the stage,
// whichever was chosen, for the next song.
func (a *app) choosePane(i int) {
	a.npPane = i
	if (i == paneLyrics || i == paneStage) && a.settings.LyricsStage != (i == paneStage) {
		a.settings.LyricsStage = i == paneStage
		a.settings.save()
	}
}

// radioChip says which radio plays, over the title: a click on it, or on
// its cross, leaves the radio, playing on the song.
func (a *app) radioChip(c *ui.Context, name string) {
	exit := Tf("Leave %s", radioLabel(name))
	chip := ui.ButtonBase(c.Key("radio-chip")).Gap(5).Padding(3, 6, 3, 8).Margin(0, 0, 4, -8).Radius(999).
		AlignItems(ui.Center).AlignSelf(ui.Start).FocusRing(false).Label(exit).Tooltip(exit)
	bg, fg, cross := ui.Transparent, white.Alpha(0.6), float32(0.5)
	if chip.Hovered() {
		bg, fg, cross = white.Alpha(0.12), white.Alpha(0.85), 1
	}
	chip.Background(chip.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond})).TextColor(fg)
	chip.Children(func() {
		ui.Icon(c, iconRadio).Size(12, 12)
		ui.Text(c, radioLabel(name)).FontSize(11.5).FontWeight(700).LetterSpacing(0.3)
		ui.Icon(c, iconX).Size(11, 11).Opacity(cross)
	})
	if chip.Clicked() {
		a.exitRadio()
	}
}

// qualityBadge marks a lossless stream, as it plays.
func qualityBadge(c *ui.Context, st *music.Stream) {
	if st == nil || st.Quality != "lossless" && st.Type != "flac" && st.Type != "wav" {
		return
	}
	ui.Text(c, T("Lossless")).FontSize(9.5).FontWeight(800).LetterSpacing(0.6).TextColor(ui.Hex("#f5d58a")).
		Padding(1, 6).Radius(4).Border(1, ui.Hex("#f5d58a").Alpha(0.55))
}

// stageBar is the slim bar along the foot of the stage: what plays, where
// it is, and the controls.
func (a *app) stageBar(c *ui.Context, tr *music.Track, art *artwork, accent ui.Color) {
	pl := a.pl
	ui.Column(c).Padding(0, 48, 28, 48).Gap(10).Children(func() {
		a.npRise(a.scrubber(c.Key("stage-scrub"), &a.npScrub, white.Alpha(0.18), white.Alpha(0.9), 14, accent.Mix(white, 0.35)), 2)
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).Basis(0).MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
				if bmp := bitmap(art); bmp != nil {
					cov := a.npMorph(c, ui.Box(c.Key(tr.ID)).Size(44, 44))
					in := artIn(cov, bmp)
					cov.Children(func() { artImage(c, bmp, a.npRadius(c, 8, 44), in) })
				}
				// All but the cover, which flies to the bar's, fade as the
				// rest of Now Playing does.
				a.npRise(ui.Row(c).Shrink(1).MinWidth(0).Gap(12).AlignItems(ui.Center), 3).Children(func() {
					ui.Column(c).Shrink(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, tr.Name).FontSize(14).FontWeight(700).SingleLine()
						a.artistLinks(c, tr, white.Alpha(0.55), white).FontSize(12).SingleLine()
					})
					a.likeButton(c.Key("like"), tr, 17, white.Alpha(0.6))
					a.songMenu(c.Key("more"), tr, 17, white.Alpha(0.6)).Margin(0, 0, 0, -10)
				})
			})
			a.npRise(ui.Row(c), 3).Children(func() { a.transport(c, white, white.Alpha(0.45), white, 22) })
			a.npRise(ui.Row(c).Grow(1).Basis(0).MinWidth(0).Justify(ui.End), 4).Children(func() {
				ui.Text(c, clock(pl.Position())+" / "+clock(pl.Duration())).FontSize(11.5).FontFeatures("tnum").TextColor(white.Alpha(0.5))
			})
		})
	})
}

func (a *app) paneTab(c *ui.Context, i int, label string) {
	b := ui.ButtonBase(c.Key(i)).Padding(6, 14).Radius(999).FocusRing(false).Label(label)
	bg, fg := ui.Transparent, white.Alpha(0.55)
	if a.npPane == i {
		bg, fg = white.Alpha(0.14), white
	} else if b.Hovered() {
		bg = white.Alpha(0.06)
	}
	b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
	b.Children(func() {
		if i != paneComments {
			ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(fg)
			return
		}
		// The count rolls as it changes, from one song's to the next.
		n, text := a.commentCount(), ""
		if n > 0 {
			text = " " + count(n)
		}
		ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(fg)
		rollCount(c, &a.tabCount, n, text, fg)
	})
	if b.Clicked() {
		a.choosePane(i)
	}
}

func (a *app) lyricsPane(c *ui.Context, tr *music.Track, h float32) {
	pl := a.pl
	lines, loaded := pl.Lyrics()
	if len(lines) == 0 {
		col := ui.Column(c).Grow(1)
		if loaded {
			col.Center()
		}
		dim, _ := a.quietShown(c, col, tr, lines, loaded)
		quietUnder(col, dim)
		col.Children(func() {
			if !loaded {
				lyricsSkeleton(c, npShimmer(c))
				return
			}
			ui.Text(c, T("No lyrics. Enjoy the music.")).FontSize(24).FontWeight(700).TextColor(white.Alpha(0.4))
		})
		a.quietCard(c, dim, h*0.18)
		return
	}
	pos := pl.Position()
	cur := lyrics.Index(lines, pos)
	// Redraw as the next line begins.
	if pl.Playing() && cur+1 < len(lines) {
		c.After(lines[cur+1].At - pos + 20*time.Millisecond)
	}
	if key := tr.Source + "\x00" + tr.ID; a.lyricTrack != key {
		a.lyricTrack, a.lyricAt = key, -2
		a.lyricScroll, a.lyricGlide = ui.ScrollState{}, lyricGlide{}
	}
	lag := a.lyricGlide.step(c, &a.lyricScroll)

	scroll := ui.Scroll(c.Key("lyrics")).Grow(1).TrackScroll(&a.lyricScroll).Padding(0, 8, h*0.5, 0)
	dim, quiet := a.quietShown(c, scroll, tr, lines, loaded)
	if dim > 0.98 {
		// Under the hot comment, gone, not drawn faint.
		scroll.Invisible()
	}
	defer a.quietCard(c, dim, h*0.18)
	a.timedLines(c, scroll, lines, pos, cur, lag, linesStyle{size: 30, weight: 800, lineHeight: 1.25, quiet: quiet}, nil)
}

// linesStyle is how timedLines sets its lines: the size, weight and
// leading of their type; quiet nearly hides them, under a hot comment.
type linesStyle struct {
	size       float32
	weight     int
	lineHeight float32
	quiet      bool
}

// timedLines fills scroll with lines that follow the track playing at
// pos: the line at cur lit, and brought to a third of the way down once
// it changes, but none with cur -1 before the first; a click on one seeks
// to it. The lines are held back by lag, how far the glide to the line
// lit has still to go (lyricGlide). foot, if set, follows the lines.
func (a *app) timedLines(c *ui.Context, scroll ui.Element, lines []lyrics.Line, pos time.Duration, cur int, lag float32, st linesStyle, foot func()) {
	pl := a.pl
	var active ui.Element
	scroll.Children(func() {
		ui.Column(c).Gap(6).Translate(0, -lag).Children(func() {
			for i, l := range lines {
				line := ui.ButtonBase(c.Key(i)).Column().AlignItems(ui.Start).Padding(10, 12).Radius(12).Gap(6).FocusRing(false).Label(l.Text)
				on := i == cur
				alpha := float32(0.28)
				if on {
					alpha = 1
				} else if line.Hovered() {
					alpha = 0.55
				}
				if st.quiet {
					// Nearly gone under a hot comment.
					alpha *= quietFaded
				}
				if line.Hovered() {
					line.Background(white.Alpha(0.06))
				}
				scale := float32(0.94)
				if on {
					scale = 1
				}
				line.TransformOrigin(0, 0.5).AnimatePose(ui.Pose{Scale: scale}, ui.Animation{Spring: ui.SpringOptions{Duration: 600 * time.Millisecond, Bounce: 0.1}})
				line.Children(func() {
					// The line fades as a pose, in frames that only paint, rather
					// than building the view at the display's rate as it does.
					text := ui.Column(c).AlignItems(ui.Start).Gap(6)
					text.AnimatePose(ui.Pose{Fade: 1 - alpha}, ui.Animation{Duration: 380 * time.Millisecond})
					text.Children(func() {
						if on && len(l.Words) > 0 {
							// The line sung lights up word by word as it is sung.
							ui.RichText(c.Key("words")).FontSize(st.size).FontWeight(st.weight).LineHeight(st.lineHeight).LetterSpacing(-0.4).Children(func() {
								for wi, wd := range l.Words {
									lit := clamp01(float32((pos - wd.Start).Seconds() / max(0.05, (wd.End-wd.Start).Seconds())))
									ui.Text(c.Key(wi), wd.Text).TextColor(white.Alpha(0.38 + 0.62*lit))
								}
							})
							if pl.Playing() {
								// Words brighten smoothly enough at 25 frames a second.
								c.After(40 * time.Millisecond)
							}
						} else {
							ui.Text(c, l.Text).FontSize(st.size).FontWeight(st.weight).LineHeight(st.lineHeight).LetterSpacing(-0.4).TextColor(white)
						}
						if l.Translation != "" {
							ui.Text(c, l.Translation).FontSize(17).FontWeight(600).TextColor(white.Alpha(0.7))
						}
					})
				})
				if line.Clicked() {
					pl.Seek(l.At)
				}
				if on {
					active = line
				}
			}
		})
		if foot != nil {
			foot()
		}
	})
	// Bring the line sung to a third of the way down, once it changed.
	if cur != a.lyricAt {
		box := scroll.Bounds()
		target := float32(0)
		if active.Valid() {
			// Where the glide is going, without where it still is.
			if lb := active.LayoutBounds(); lb.H > 0 && box.H > 0 {
				target = lb.Y - box.Y + a.lyricScroll.Y - box.H*0.3
			} else {
				return // not laid out yet: the next frame scrolls
			}
		}
		a.lyricAt = cur
		a.lyricGlide.to(c, &a.lyricScroll, min(max(0, target), a.lyricScroll.MaxY))
	}
}

// lyricGlide scrolls the lyrics to the line sung, as a critically damped
// spring, as ScrollTo's would. ScrollTo moves the offset in every frame,
// and MyGo builds the view again in the frame after each that moved it,
// and its spring creeps over the last pixel for half a second: with a line
// following another every second or two, most of a song. So the offset
// goes to the line at once, in one frame, and the lines are held back by
// what the glide has still to go, its lag, which settles to nothing within
// half a pixel.
type lyricGlide struct {
	on bool
	// The lag t after start: (a + b·t)·e^(−ωt), in DIPs.
	a, b  float64
	start time.Time
	// The offset it goes to, set in the next frame (pending), with the lag
	// that holds the lines back from it: the layout of the frame that
	// chose it takes the offset too, the lines built without the lag.
	target  float32
	pending bool
	set     float32 // the offset it set
}

// lyricOmega is the spring's ω: a line or two away, it comes within half a
// pixel in about 800 ms.
const lyricOmega = 10

// at returns the lag at now, and how fast it changes, in DIPs a second.
func (g *lyricGlide) at(now time.Time) (lag, v float64) {
	if !g.on {
		return 0, 0
	}
	t := now.Sub(g.start).Seconds()
	e := math.Exp(-lyricOmega * t)
	return (g.a + g.b*t) * e, (g.b - lyricOmega*(g.a+g.b*t)) * e
}

// to scrolls s to target from the next frame, gliding from where it
// shows, at the speed it went if it was on its way.
func (g *lyricGlide) to(c *ui.Context, s *ui.ScrollState, target float32) {
	now := c.Now()
	lag, v := g.at(now)
	shown := float64(s.Y) + lag
	g.target, g.pending = target, true
	g.on = !c.Preferences().ReduceMotion
	g.start, g.a = now, shown-float64(target)
	g.b = v + lyricOmega*g.a
	c.AnimationFrame()
}

// step returns the lag of the lines in this frame, and asks for the next
// while there is one. The listener scrolling stops the glide where it
// shows.
func (g *lyricGlide) step(c *ui.Context, s *ui.ScrollState) float32 {
	if g.pending {
		s.Y, g.set, g.pending = g.target, g.target, false
	}
	if !g.on {
		return 0
	}
	lag, v := g.at(c.Now())
	if s.Y != g.set {
		// The listener took over: the offset takes what showed.
		s.Y, g.on = max(0, min(s.Y+float32(lag), s.MaxY)), false
		return 0
	}
	if math.Abs(lag) < 0.5 && math.Abs(v) < 20 {
		g.on = false
		return 0
	}
	c.AnimationFrame()
	return float32(lag)
}

// queuePane lists the whole queue in play order, the track playing marked
// among the others: a click plays one there, and the list stays as it is.
func (a *app) queuePane(c *ui.Context) {
	pl := a.pl
	order, at := pl.PlayOrder()
	// Opened, it shows the track playing at its top; after, it follows it
	// as little as keeps it in view.
	if at >= 0 && at != a.queueAt {
		if a.queueAt < 0 {
			a.queueList.ScrollTo(at, ui.Start)
		} else {
			a.queueList.ScrollIntoView(at)
		}
		a.queueAt = at
	}
	a.queueList.Key = func(row int) any { return order[row] }
	ui.List(c.Key("queue"), &a.queueList, len(order), func(r int) {
		i := order[r]
		tr := &pl.Queue()[i]
		playing := r == at
		row := ui.ButtonBase(c.Key(i)).Height(58).PaddingX(10).Gap(14).Radius(12).AlignItems(ui.Center).FocusRing(false).Label(tr.Name).
			ContextMenu(a.songItems(tr, ""))
		switch {
		case playing:
			row.Background(white.Alpha(0.12))
		case row.Hovered():
			row.Background(white.Alpha(0.08))
		}
		if tr.Unavailable {
			row.Opacity(0.4)
		}
		row.Children(func() {
			a.cover(c, tr.Album.ImageURL, 96, 42, 7)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, tr.Name).FontSize(14).FontWeight(600).SingleLine()
				a.artistLinks(c, tr, white.Alpha(0.55), white).FontSize(12.5).SingleLine()
			})
			ui.Text(c, clock(tr.Length())).FontSize(12).FontFeatures("tnum").TextColor(white.Alpha(0.4))
		})
		if row.Clicked() && !tr.Unavailable && !playing {
			pl.Jump(i)
		}
	}).Grow(1).Padding(8, 0, 40, 0).Gap(2)
}
