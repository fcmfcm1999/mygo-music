package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/i18n"
	"mygo-music/internal/music"
	"mygo-music/internal/player"
)

// featureCard leads a source's Home: the songs of its first section, in the
// colors of their first cover, the day's date set huge and faint behind them.
// The first cover is a sleeve with its record peeking out, the next two behind
// it; on wide pages the first picks are listed between. Under the pointer the
// covers fan and the record slides out, turning; while its songs play it turns
// on, and grooves ripple out from it across the card. A click opens all of them.
func (a *app) featureCard(c *ui.Context, sec *music.Section, pageID string, at int, now time.Time) {
	songs := sec.Tracks
	if len(songs) == 0 {
		return
	}
	w, _ := c.Size()
	inner := w - sidebarWidth - 2*pagePad
	still := c.Preferences().ReduceMotion
	art := a.art.get(sized(songs[0].Album.ImageURL, 600), true)
	card := ui.ButtonBase(c.Key("daily")).Height(dailyHeight).Radius(dailyRadius).FocusRing(false).Label(sec.Title).TextColor(white)
	hovered := card.Hovered()
	ours, playing := a.dailyPlaying(songs)

	// The cover's colors, eased in as it loads.
	deep, rich, glow := dailyColors(art)
	fade := ui.Animation{Duration: 600 * time.Millisecond}
	deep, rich, glow = card.AnimateColor("deep", deep, fade), card.AnimateColor("rich", rich, fade), card.AnimateColor("glow", glow, fade)
	card.Background(deep)
	lift := card.AnimateTo("lift", b2f(hovered), ui.Snappy)
	card.Shadow(0, 14+8*lift, 34+12*lift, -14, a.pal.cardShade)
	// A sheen sweeps across once as the pointer comes in.
	sweep := float32(0)
	if hovered && !still {
		sweep = card.AnimateTo("sweep", 1, ui.Animation{Duration: 1100 * time.Millisecond, Ease: ui.EaseInOut})
	} else {
		card.Jump("sweep", 0)
	}

	// The record: tucked in, out a little under the pointer, out while it
	// plays.
	out := float32(0)
	switch {
	case ours:
		out = 1
	case hovered:
		out = 0.6
	}
	out = card.AnimateTo("out", out, ui.Animation{Spring: ui.SpringOptions{Duration: 700 * time.Millisecond, Bounce: 0.25}})
	turn := 0.0
	switch {
	case still:
	case playing:
		turn = 1
	case hovered:
		turn = 0.12
	}
	showStack, showPicks, showDay := inner >= 620, inner >= 1040, inner >= 900
	backdrop := backdrop(art)

	card.Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(p *ui.Painter, r ui.Rect) {
			spin := &a.dailySpin
			if spin.step(p.Now(), turn) {
				p.AnimationFrameRate(dailyRate)
			}
			// Where the record turns: at the right, in its sleeve's stack.
			cx := r.X + r.W - dailyPadX - 120
			if showStack {
				cx = r.X + r.W - dailyPadX - stackWidth + sleeveLeft + sleeveSize/2 + recordOut(out)
			}
			drawDailyBackdrop(p, r, backdrop, deep, rich, glow, cx, float32(spin.ripple), sweep)
			// The day, set huge and faint after the words, cut off by the
			// foot, where the covers leave it room.
			if showDay {
				day := ui.Span{Text: fmt.Sprint(now.Day()), Size: 240, Weight: 800, LetterSpacing: -12, Features: "tnum", Color: white.Alpha(0.055)}
				p.Clip(r, dailyRadius, func() { p.RichText(r.X+dailyPadX+300, r.Y+r.H*0.22, 0, day) })
			}
		})
		ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Padding(0, dailyPadX).Gap(28).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, i18n.Today(now)).FontSize(12).FontWeight(600).LetterSpacing(0.3).TextColor(white.Alpha(0.66)).MaxLines(1)
				ui.Text(c, sec.Title).FontSize(32).FontWeight(800).LetterSpacing(-0.8).LineHeight(1.15)
				ui.Text(c, dailyArtists(songs)).FontSize(13).TextColor(white.Alpha(0.8)).MaxLines(1)
				ui.Row(c).Gap(10).Margin(12, 0, 0, 0).Children(func() {
					a.dailyPlayButton(c.Key("play"), songs, ours, playing)
					if a.library.signedOut() && pillButton(c.Key("login"), iconUser, T("Sign In"), white.Alpha(0.16), white).Clicked() {
						a.promptSignIn(a.library)
					}
				})
			})
			if showPicks {
				a.dailyPicks(c, songs, ours)
			}
			if showStack {
				a.dailyStack(c, songs, hovered && !still, out)
			}
		})
	})
	if card.Clicked() {
		if sec.MorePage != "" {
			a.open(route{page: pageBrowse, id: sec.MorePage})
		} else {
			a.open(route{page: pageSection, id: pageID, query: strconv.Itoa(at)})
		}
	}
}

// dayCover heads the page of the feature card's songs as the card is: the
// first cover blurred through its colors, the day's number set large over
// it.
func (a *app) dayCover(c *ui.Context, songs []music.Track, now time.Time) {
	var art *artwork
	if len(songs) > 0 {
		art = a.art.get(sized(songs[0].Album.ImageURL, 600), true)
	}
	box := ui.Box(c).Size(212, 212).Radius(14).Shadow(0, 18, 40, -10, a.pal.cardShade)
	deep, rich, _ := dailyColors(art)
	fade := ui.Animation{Duration: 600 * time.Millisecond}
	deep, rich = box.AnimateColor("deep", deep, fade), box.AnimateColor("rich", rich, fade)
	blurred := backdrop(art)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Clip(r, 14, func() {
			p.FillGradient(r, ui.LinearGradient{Angle: 135, Oklab: true, Stops: []ui.GradientStop{
				{Color: deep, At: 0}, {Color: rich, At: 1},
			}}, 0)
			if blurred != nil {
				p.ImageRegion(blurred, ui.Rect{W: 48, H: 48}, coverRect(r, 48, 48), ui.ImageOptions{Tint: white.Alpha(0.34)})
			}
		})
		p.Stroke(r, white.Alpha(0.1), 14, 1)
	})
	box.Center().Children(func() {
		ui.Text(c, fmt.Sprint(now.Day())).FontSize(112).FontWeight(800).LetterSpacing(-5).TextColor(white).LineHeight(1)
	})
}

// The daily card's measures; the stack of covers is laid out by hand.
const (
	dailyHeight = 212
	dailyRadius = 24
	dailyPadX   = 32
	stackWidth  = 300
	sleeveLeft  = 64 // where the first cover's sleeve is in the stack
	sleeveSize  = 156
	recordSize  = 146
)

// dailyRate is how many frames a second the daily card draws what moves
// on it, its record turning and the bars on its play button: every frame
// of a 120 Hz display, it redrew the whole card 120 times a second, for
// motion that looks as smooth at half of that; at a quarter, the record's
// label steps.
const dailyRate = 60

// recordOut is how far right of its sleeve's middle the record's is, as
// far out as out.
func recordOut(out float32) float32 { return 30 + 46*out }

// dailyPlaying reports whether songs are what plays, and whether they play now
// rather than paused.
func (a *app) dailyPlaying(songs []music.Track) (ours, playing bool) {
	q, cur := a.pl.Queue(), a.pl.Current()
	if cur == nil || len(q) != len(songs) || len(q) == 0 || q[0].ID != songs[0].ID || q[len(q)-1].ID != songs[len(q)-1].ID {
		return false, false
	}
	return true, a.pl.Playing()
}

// dailyColors returns the card's colors for the first cover: a deep
// and a rich shade of its hue, a little turned, and a bright one to glow,
// at depths that white reads over whatever the cover. A gray cover, or one
// still loading, takes the app's red.
func dailyColors(art *artwork) (deep, rich, glow ui.Color) {
	h, s := 356.0, 0.74
	if art != nil && !art.failed {
		th, ts, _ := hsv(float64(art.tint.R)/255, float64(art.tint.G)/255, float64(art.tint.B)/255)
		if ts > 0.15 {
			h, s = th, min(max(ts*1.2, 0.5), 0.78)
		}
	}
	return hsvColor(h, s, 0.24), hsvColor(math.Mod(h+16, 360), s*0.9, 0.5), hsvColor(math.Mod(h+32, 360), s*0.55, 1)
}

// drawDailyBackdrop paints the daily card in r: a gradient of the cover's
// colors, the cover itself blurred faintly through it, a glow where the
// record turns at cx, and grooves rippling out from it, ripple points
// past where they start; and, as sweep goes from 0 to 1, a sheen crossing
// it.
func drawDailyBackdrop(p *ui.Painter, r ui.Rect, blurred *ui.Bitmap, deep, rich, glow ui.Color, cx, ripple, sweep float32) {
	cy := r.Y + r.H/2
	p.Clip(r, dailyRadius, func() {
		p.FillGradient(r, ui.LinearGradient{Angle: 100, Oklab: true, Stops: []ui.GradientStop{
			{Color: deep, At: 0.1}, {Color: rich, At: 0.85}, {Color: rich.Mix(deep, 0.3), At: 1},
		}}, 0)
		if blurred != nil {
			p.ImageRegion(blurred, ui.Rect{W: 48, H: 48}, coverRect(r, 48, 48), ui.ImageOptions{Tint: white.Alpha(0.28)})
		}
		// Deeper behind the words, at the left.
		p.FillGradient(r, ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
			{Color: deep.Alpha(0.85), At: 0}, {Color: deep.Alpha(0), At: 0.6},
		}}, 0)
		// The glow behind the record, and light from above.
		p.Shadow(ui.Rect{X: cx - 60, Y: cy - 60, W: 120, H: 120}, 60, 0, 0, 140, 30, glow.Alpha(0.32))
		p.Shadow(ui.Rect{X: r.X + r.W*0.3, Y: r.Y - 80, W: r.W * 0.4, H: 40}, 20, 0, 0, 120, 0, white.Alpha(0.08))
		// Grooves rippling out, fainter as they go, by whole points: the
		// card around the record is drawn again only as they move one, not
		// for each frame the record turns.
		off := float32(math.Floor(float64(ripple)))
		const gap = rippleGap
		for rr := float32(recordSize/2+6) + off; rr < r.W; rr += gap {
			a := 0.05 * (1 - (rr-recordSize/2)/(r.H*1.6))
			if a <= 0.004 {
				break
			}
			// Faded in as each leaves the record.
			a *= min(1, (rr-recordSize/2-6)/gap)
			p.Stroke(ui.Rect{X: cx - rr, Y: cy - rr, W: 2 * rr, H: 2 * rr}, white.Alpha(a), rr, 1)
		}
		if sweep > 0 && sweep < 1 {
			at := -0.25 + 1.5*sweep
			p.FillGradient(r, ui.LinearGradient{Angle: 110, Stops: []ui.GradientStop{
				{Color: white.Alpha(0), At: clamp01(at - 0.12)},
				{Color: white.Alpha(0.13 * (1 - sweep*0.5)), At: clamp01(at)},
				{Color: white.Alpha(0), At: clamp01(at + 0.12)},
			}}, 0)
		}
	})
	// A hairline of light around its edge, as on glass.
	p.Stroke(r, white.Alpha(0.1), dailyRadius, 1)
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// spinState turns the daily card's record: its angle, in degrees, how far
// the grooves around it have rippled out, up to a groove, and how fast it
// turns, as a part of a turntable's speed, which eases up and glides to a
// stop.
type spinState struct {
	angle, ripple, speed float64
	last                 time.Time
}

// The grooves around the record are rippleGap apart, and ripple out a
// groove every ripplePeriod at a turntable's speed. Each step they take
// redraws most of the card, in each of the window's buffers in turn: at a
// groove a turn, 1.8 seconds, by pixels of a Retina display, they took 20
// steps a second while the card's songs played; by points, a groove in 6
// seconds, they take 3.
const (
	rippleGap    = 18
	ripplePeriod = 6.0 // seconds
)

// step turns the record on to now, its speed easing to to, and reports
// whether it still turns.
func (s *spinState) step(now time.Time, to float64) bool {
	dt := now.Sub(s.last).Seconds()
	if s.last.IsZero() || dt > 0.2 {
		dt = 1.0 / 60
	}
	s.last = now
	s.speed += (to - s.speed) * min(1, 2.2*dt)
	if math.Abs(to-s.speed) < 0.002 {
		s.speed = to
	}
	// 33⅓ turns a minute.
	s.angle = math.Mod(s.angle+s.speed*dt*200, 360)
	s.ripple = math.Mod(s.ripple+s.speed*dt*rippleGap/ripplePeriod, rippleGap)
	return s.speed > 0
}

// dailyArtists names the first few singers of songs, and how many songs.
func dailyArtists(songs []music.Track) string {
	var names []string
	seen := map[string]bool{}
	for i := range songs {
		if len(songs[i].Artists) == 0 {
			continue
		}
		n := songs[i].Artists[0].Name
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
		if len(names) == 3 {
			break
		}
	}
	if len(names) == 0 {
		return i18n.Songs(len(songs))
	}
	return Tf("%s and more · %s", strings.Join(names, ", "), i18n.Songs(len(songs)))
}

// dailyPlayButton plays the songs; while they play it says so, its
// bars dancing, and pauses them; paused, it goes on.
func (a *app) dailyPlayButton(c *ui.Context, songs []music.Track, ours, playing bool) {
	ink := ui.Hex("#1d1d1f")
	// As wide hovered as not: narrower, it would leave the pointer, and flicker
	// between the two.
	b := ui.ButtonBase(c).Height(38).MinWidth(112).PaddingX(20).Gap(8).Radius(999).AlignItems(ui.Center).TextColor(ink).
		Shadow(0, 6, 16, -6, ui.RGBA(0, 0, 0, 0.35))
	fill := white
	if b.Hovered() {
		fill = ui.Hex("#f2f2f4")
	}
	b.Background(b.AnimateColor("bg", fill, ui.Animation{Duration: 120 * time.Millisecond}))
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Scale = 0.95
	}
	b.AnimatePose(pose, ui.Snappy)
	label := T("Play")
	switch {
	case playing && b.Hovered():
		label = T("Pause")
	case playing:
		label = T("Playing")
	case ours:
		label = T("Resume")
	}
	b.Label(label)
	b.Children(func() {
		if playing && !b.Hovered() {
			accent := a.pal.accent
			ui.Box(c).Size(14, 13).Draw(func(p *ui.Painter, r ui.Rect) {
				lv, live := a.pl.Levels()
				a.dailyBars.step(p.Now(), lv, live)
				drawBars(p, r, &a.dailyBars, 4, accent, 2.5)
				if live || a.dailyBars.group(0, player.Bands) > 0.01 {
					p.AnimationFrameRate(dailyRate)
				}
			})
		} else {
			icon := iconPlay
			if playing {
				icon = iconPause
			}
			ui.Icon(c, icon).Size(14, 14)
		}
		ui.Text(c, label).FontSize(14).FontWeight(650).SingleLine()
	})
	if b.Clicked() {
		if ours {
			a.pl.TogglePlay()
		} else {
			a.pl.PlayQueue(songs, firstPlayable(songs))
		}
	}
}

// dailyPicks lists the first songs, numbered, the one playing with
// its bars; each plays the songs from it.
func (a *app) dailyPicks(c *ui.Context, songs []music.Track, ours bool) {
	cur := a.pl.Current()
	ui.Column(c.Key("picks")).Width(236).Shrink(0).Gap(2).Children(func() {
		for i := range min(3, len(songs)) {
			tr := &songs[i]
			row := ui.ButtonBase(c.Key(tr.ID)).Row().Gap(12).Padding(7, 12).Radius(12).AlignItems(ui.Center).FocusRing(false).Label(tr.Name).
				ContextMenu(a.songItems(tr, ""))
			bg := white.Alpha(0)
			if row.Hovered() {
				bg = white.Alpha(0.1)
			}
			row.Background(row.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
			current := ours && sameTrack(cur, tr)
			row.Children(func() {
				ui.Box(c).Width(18).Center().Children(func() {
					if current {
						ui.Icon(c, iconPlay).Size(11, 11).TextColor(white)
					} else {
						ui.Textf(c, "%02d", i+1).FontSize(12).FontWeight(700).FontFeatures("tnum").TextColor(white.Alpha(0.45))
					}
				})
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, tr.Name).FontSize(13).FontWeight(600).MaxLines(1)
					a.artistLinks(c, tr, white.Alpha(0.6), white).FontSize(11.5).MaxLines(1)
				})
			})
			if row.Clicked() {
				a.pl.PlayTrack(songs, i)
			}
		}
	})
}

// dailyStack is the first cover, as a sleeve with its record out as
// far as out, the two covers after it behind; fanned, they spread.
func (a *app) dailyStack(c *ui.Context, songs []music.Track, fanned bool, out float32) {
	spring := ui.Animation{Spring: ui.SpringOptions{Duration: 600 * time.Millisecond, Bounce: 0.3}}
	ui.Box(c.Key("stack")).Size(stackWidth, sleeveSize).Shrink(0).Children(func() {
		// The covers behind, the farthest first.
		for i := min(2, len(songs)-1); i >= 1; i-- {
			size := float32(sleeveSize - 18*i)
			pose := ui.Pose{Rotate: -5 * float32(i), Y: 2}
			if fanned {
				pose = ui.Pose{Rotate: -9 * float32(i), X: -10 * float32(i), Y: -2}
			}
			cov := ui.Box(c.Key(i)).Absolute().Left(sleeveLeft-30*float32(i)).Top((sleeveSize-size)/2).Size(size, size).Radius(10).
				Opacity(1-0.12*float32(i)).Shadow(0, 10, 22, -6, ui.RGBA(0, 0, 0, 0.45))
			cov.AnimatePose(pose, spring)
			cov.Children(func() { a.coverFill(c, songs[i].Album.ImageURL, 240, 10) })
		}
		// The record, behind its sleeve.
		mid := float32(sleeveLeft + sleeveSize/2)
		ui.Box(c.Key("record")).Absolute().Left(mid+recordOut(out)-recordSize/2).Top((sleeveSize-recordSize)/2).
			Size(recordSize, recordSize).Radius(recordSize/2).Shadow(0, 8, 20, -4, ui.RGBA(0, 0, 0, 0.5)).
			Draw(func(p *ui.Painter, r ui.Rect) {
				drawRecord(p, r, bitmap(a.art.get(sized(songs[0].Album.ImageURL, 240), false)), float32(a.dailySpin.angle))
			})
		// The sleeve.
		sleeve := ui.Box(c.Key("sleeve")).Absolute().Left(sleeveLeft).Top(0).Size(sleeveSize, sleeveSize).Radius(12).
			Shadow(0, 16, 30, -8, ui.RGBA(0, 0, 0, 0.5))
		sleeve.AnimatePose(ui.Pose{Rotate: -2 * b2f(fanned), Y: -3 * b2f(fanned)}, spring)
		sleeve.Children(func() {
			a.coverFill(c, songs[0].Album.ImageURL, 400, 12)
			// Light on its gloss, and its edge.
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(12).PassThrough().Border(1, white.Alpha(0.14)).
				LinearGradient(ui.LinearGradient{Angle: 145, Stops: []ui.GradientStop{
					{Color: white.Alpha(0.22), At: 0}, {Color: white.Alpha(0), At: 0.42},
					{Color: ui.RGBA(0, 0, 0, 0), At: 0.7}, {Color: ui.RGBA(0, 0, 0, 0.18), At: 1},
				}})
		})
	})
}

// drawRecord paints a vinyl record in r, its label the cover bmp, turned
// to angle: grooves catching the light, which stays where it is as the
// record turns, round a label that turns with it.
func drawRecord(p *ui.Painter, r ui.Rect, bmp *ui.Bitmap, angle float32) {
	d := r.W
	cx, cy := r.X+d/2, r.Y+d/2
	p.Fill(r, ui.Hex("#0e0e10"), d/2)
	for i, rr := 0, d*0.21; rr < d*0.48; i, rr = i+1, rr+2.2 {
		a := float32(0.035)
		if i%3 == 0 {
			a = 0.07
		}
		p.Stroke(ui.Rect{X: cx - rr, Y: cy - rr, W: 2 * rr, H: 2 * rr}, white.Alpha(a), rr, 0.7)
	}
	// The light on it, in two opposite fans.
	p.FillConicGradient(r, ui.ConicGradient{Angle: 20, Stops: []ui.GradientStop{
		{Color: white.Alpha(0), At: 0}, {Color: white.Alpha(0.13), At: 0.08}, {Color: white.Alpha(0), At: 0.2},
		{Color: white.Alpha(0), At: 0.5}, {Color: white.Alpha(0.13), At: 0.58}, {Color: white.Alpha(0), At: 0.7},
		{Color: white.Alpha(0), At: 1},
	}}, d/2)
	p.Stroke(r, white.Alpha(0.1), d/2, 1)
	// The label, turning: clipped still, its cover turning inside.
	lr := d * 0.18
	label := ui.Rect{X: cx - lr, Y: cy - lr, W: 2 * lr, H: 2 * lr}
	p.Fill(label, ui.Hex("#d8262d"), lr)
	if bmp != nil {
		p.Clip(label, lr, func() {
			p.Transform(ui.Rotation(angle), cx, cy, func() { p.Image(bmp, label, ui.Cover) })
		})
	}
	p.Stroke(label, ui.RGBA(0, 0, 0, 0.35), lr, 1.5)
	// The spindle hole.
	p.Fill(ui.Rect{X: cx - 2.5, Y: cy - 2.5, W: 5, H: 5}, ui.Hex("#0e0e10"), 2.5)
}
