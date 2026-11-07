package plugin

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// RequestHeaders, in plugin.json, are headers the app sends with what it
// fetches itself of the plugin's hosts: its pictures, and its songs' audio.
// A service may keep those behind its session, which a plugin's own fetch
// carries, but a cover's or a stream's URL can't:
//
//	"requestHeaders": [{
//	  "hosts": ["{setting:server}"],
//	  "kinds": ["image", "audio"],
//	  "headers": {"cookie": "music-token={storage:token}"}
//	}]
//
// {setting:key} is what the listener filled in, and {storage:key} what the
// plugin keeps with host.storage, as each is when the request goes out, so a
// token it changes goes with the next one. Nothing of them goes in a URL.
type RequestHeaders struct {
	// Hosts are those of the network permission it's for: api.example.com,
	// *.example.com, or {setting:key}, the host of the server a setting
	// names, such as https://nas.local:5666. Each has to be one the network
	// permission reaches, and * isn't one: headers go to hosts named.
	Hosts []string `json:"hosts"`
	// Kinds are what it's for, image or audio, and both without it.
	Kinds   []string          `json:"kinds,omitempty"`
	Headers map[string]string `json:"headers"`
}

// Kinds of what the app fetches for a plugin.
const (
	KindImage = "image"
	KindAudio = "audio"
)

var placeholder = regexp.MustCompile(`\{(setting|storage):([^{}]+)\}`)

// checkRequestHeaders reports what's wrong with m's requestHeaders, if
// anything, as it loads.
func checkRequestHeaders(m *Manifest) error {
	for i, r := range m.RequestHeaders {
		if len(r.Hosts) == 0 || len(r.Headers) == 0 {
			return fmt.Errorf("requestHeaders[%d] needs hosts and headers", i)
		}
		for _, k := range r.Kinds {
			if k != KindImage && k != KindAudio {
				return fmt.Errorf("requestHeaders[%d]: kind %q isn't image or audio", i, k)
			}
		}
		for _, h := range r.Hosts {
			if key, ok := strings.CutPrefix(h, "{setting:"); ok {
				if key, ok = strings.CutSuffix(key, "}"); !ok || key == "" || strings.ContainsAny(key, "{}") {
					return fmt.Errorf("requestHeaders[%d]: host %q is neither a host nor {setting:key}", i, h)
				}
				continue
			}
			if h == "*" || strings.ContainsAny(h, "{}/:") {
				return fmt.Errorf("requestHeaders[%d]: host %q isn't a host or *.domain", i, h)
			}
			if !m.Permissions.coversPattern(h) {
				return fmt.Errorf("requestHeaders[%d]: host %q is outside the network permission", i, h)
			}
		}
		for name := range r.Headers {
			if strings.EqualFold(name, "host") || strings.EqualFold(name, "content-length") {
				return fmt.Errorf("requestHeaders[%d]: %s isn't a header it can set", i, name)
			}
		}
	}
	return nil
}

// reaches reports whether the network permission reaches host.
func (ps Permissions) reaches(host string) bool {
	host = strings.ToLower(host)
	for _, h := range ps.Network {
		h = strings.ToLower(h)
		if h == "*" || h == host || strings.HasPrefix(h, "*.") && strings.HasSuffix(host, h[1:]) {
			return true
		}
	}
	return false
}

// coversPattern reports whether the network permission reaches every host
// pattern does: pattern is a host or *.domain.
func (ps Permissions) coversPattern(pattern string) bool {
	pattern = strings.ToLower(pattern)
	for _, h := range ps.Network {
		h = strings.ToLower(h)
		switch {
		case h == "*", h == pattern:
			return true
		case strings.HasPrefix(h, "*."):
			// *.example.com covers a.example.com and *.a.example.com.
			if strings.HasSuffix(strings.TrimPrefix(pattern, "*"), h[1:]) {
				return true
			}
		}
	}
	return false
}

// HeadersFor answers the headers to fetch u with, of what the plugin's
// requestHeaders give for kind, its placeholders filled in as the settings
// and storage are now. A header with a placeholder of nothing is left out,
// as before the listener has signed in. It answers nil for a URL not of the
// plugin's hosts.
func (p *Plugin) HeadersFor(u *url.URL, kind string) map[string]string {
	if len(p.manifest.RequestHeaders) == 0 || u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	p.mu.Lock()
	defer p.mu.Unlock()
	var out map[string]string
	for _, r := range p.manifest.RequestHeaders {
		if len(r.Kinds) > 0 && !slices.Contains(r.Kinds, kind) || !p.ruleReaches(r, host) {
			continue
		}
		for name, value := range r.Headers {
			v, ok := p.fill(value)
			if !ok {
				continue
			}
			if out == nil {
				out = map[string]string{}
			}
			if _, set := out[name]; !set {
				out[name] = v
			}
		}
	}
	return out
}

// ruleReaches reports whether r is for host. A host of a setting counts
// only while the network permission reaches it too. The caller holds p.mu.
func (p *Plugin) ruleReaches(r RequestHeaders, host string) bool {
	for _, h := range r.Hosts {
		if key, ok := strings.CutPrefix(h, "{setting:"); ok {
			if serverHost(p.settings[strings.TrimSuffix(key, "}")]) == host && p.manifest.Permissions.reaches(host) {
				return true
			}
			continue
		}
		h = strings.ToLower(h)
		if h == host || strings.HasPrefix(h, "*.") && strings.HasSuffix(host, h[1:]) {
			return true
		}
	}
	return false
}

// serverHost is the host of a server as a setting names it: a URL, such as
// https://nas.local:5666, or a host, with its port or not.
func serverHost(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// fill fills in value's placeholders, and reports false if one is of
// nothing. A stored value that isn't a string goes as its JSON. The caller
// holds p.mu.
func (p *Plugin) fill(value string) (string, bool) {
	ok := true
	filled := placeholder.ReplaceAllStringFunc(value, func(m string) string {
		parts := placeholder.FindStringSubmatch(m)
		var v string
		switch parts[1] {
		case "setting":
			v = p.settings[parts[2]]
		case "storage":
			raw := p.storage[parts[2]]
			if err := json.Unmarshal(raw, &v); err != nil && len(raw) > 0 && string(raw) != "null" {
				v = string(raw)
			}
		}
		if v == "" {
			ok = false
		}
		return v
	})
	// A header can't hold a line break, which would end it.
	if strings.ContainsAny(filled, "\r\n") {
		return "", false
	}
	return filled, ok
}
