package app

import (
	"net/url"
	"sync/atomic"

	"mygo-music/internal/plugin"
)

// headerPlugins are the plugins that are on, for the pictures loading off
// the main thread to ask for their requestHeaders.
type headerPlugins struct{ on atomic.Pointer[[]*plugin.Plugin] }

// set takes the plugins that are on.
func (h *headerPlugins) set(on []*plugin.Plugin) { h.on.Store(&on) }

// image answers the headers to fetch the picture at u with: those of the
// first plugin on whose requestHeaders are for its host.
func (h *headerPlugins) image(u *url.URL) map[string]string {
	on := h.on.Load()
	if on == nil {
		return nil
	}
	for _, p := range *on {
		if headers := p.HeadersFor(u, plugin.KindImage); headers != nil {
			return headers
		}
	}
	return nil
}

