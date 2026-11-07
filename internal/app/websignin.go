package app

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"mygo-music/internal/plugin"
)

// Plugins of services that have no way in but their website sign in on a page
// the app shows: see plugin.WebSignIn.

const (
	// signInPagePoll is how often the page's cookies are looked at for those
	// that say the listener signed in.
	signInPagePoll = 400 * time.Millisecond
	// signInPageFor is how long the page waits for them, as long as the
	// plugin waits for a sign-in.
	signInPageFor = 5 * time.Minute
)

// webSignIn shows a plugin's sign-in page in a window of its own, over the
// one its dialog is in, in a session of its own: it sees none of the
// listener's other sign-ins, and is forgotten once the window closes. It answers the Cookie
// header of the address the plugin asked for once the cookies it named are
// all there, and closes the window. Closing it first is giving up.
func (a *app) webSignIn(o plugin.WebSignIn) (string, error) {
	// Asked on the main thread: on Linux the first asking loads WebKitGTK and
	// makes its default web context, and WebKit then takes the thread that did
	// for its main one. Asked from here, the window made on the real main
	// thread next aborted the app.
	var available bool
	mygo.RunOnMain(func() { available = mygo.WebViewAvailable() })
	if !available {
		return "", errors.New(T("This computer can't show the page to sign in on"))
	}
	session := mygo.NewEphemeralSession()
	over := a.win
	if a.signInOver != nil {
		if w := a.signInOver.Load(); w != nil {
			over = w
		}
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:     Tf("Sign in to %s", o.Name),
		URL:       o.URL,
		Width:     480,
		Height:    680,
		MinWidth:  360,
		MinHeight: 480,
		Parent:    over,
		Page:      mygo.PageOptions{Session: session, UserAgent: o.UserAgent},
	})
	closed := make(chan struct{})
	win.OnClosed(func() { close(closed) })
	a.post(func() {
		if a.signInPages == nil {
			a.signInPages = map[string]*mygo.Window{}
		}
		a.signInPages[o.Plugin] = win
	})
	defer a.post(func() {
		if a.signInPages[o.Plugin] == win {
			delete(a.signInPages, o.Plugin)
		}
	})
	tick := time.NewTicker(signInPagePoll)
	defer tick.Stop()
	timeout := time.NewTimer(signInPageFor)
	defer timeout.Stop()
	for {
		select {
		case <-closed:
			return "", errors.New(T("The sign-in window was closed before you finished signing in"))
		case <-timeout.C:
			win.Close()
			return "", errors.New(T("Signing in took too long. Try again."))
		case <-tick.C:
			cookies, err := session.Cookies.List(mygo.CookieFilter{URL: o.CookiesOf})
			if err != nil {
				log.Printf("sign-in page of %s: %v", o.Plugin, err)
				continue
			}
			if header, ok := cookieHeader(cookies, o.Until); ok {
				win.Close()
				return header, nil
			}
		}
	}
}

// cookieHeader is the Cookie header that sends cookies, once those named in
// until are all among them.
func cookieHeader(cookies []mygo.Cookie, until []string) (string, bool) {
	have := map[string]bool{}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c.Value == "" {
			continue
		}
		have[c.Name] = true
		parts = append(parts, c.Name+"="+c.Value)
	}
	for _, name := range until {
		if !have[name] {
			return "", false
		}
	}
	return strings.Join(parts, "; "), true
}

// closeSignInPage gives up on a plugin's sign-in page, if it shows.
func (a *app) closeSignInPage(id string) {
	if w := a.signInPages[id]; w != nil {
		w.Close()
	}
}
