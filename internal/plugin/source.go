package plugin

import (
	"errors"
	"log"
	"net/url"
	"strings"

	"mygo-music/internal/music"
)

// The answers of a plugin name their tracks without their source, which is
// the plugin: it can't get that wrong, and needn't know its own ID.

func (p *Plugin) stamp(tracks []music.Track) {
	for i := range tracks {
		tracks[i].Source = p.manifest.ID
	}
}

func (p *Plugin) stampBrowse(b *music.Browse) {
	for i := range b.Sections {
		s := &b.Sections[i]
		p.stamp(s.Tracks)
		for k := range s.Playlists {
			p.stamp(s.Playlists[k].Tracks)
		}
	}
}

func (p *Plugin) SignIn() (*music.Account, error) {
	var a music.Account
	if err := p.callWithin(signInTimeout, "signIn", nil, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (p *Plugin) SignOut() error { return p.call("signOut", nil, nil) }

// Account is who is signed in, or nil if nobody is.
func (p *Plugin) Account() (*music.Account, error) {
	var a *music.Account
	if err := p.call("account", nil, &a); err != nil {
		return nil, err
	}
	return a, nil
}

func (p *Plugin) Search(query string, kind music.SearchKind, offset int) (*music.SearchResult, error) {
	var r music.SearchResult
	err := p.call("search", map[string]any{"query": query, "kind": kind, "offset": offset}, &r)
	if err != nil {
		return nil, err
	}
	p.stamp(r.Tracks)
	for i := range r.Playlists {
		p.stamp(r.Playlists[i].Tracks)
	}
	return &r, nil
}

func (p *Plugin) Suggest(query string) ([]string, error) {
	var out []string
	if err := p.call("suggest", map[string]string{"query": query}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Plugin) Home() (*music.Browse, error) {
	var b music.Browse
	if err := p.call("home", nil, &b); err != nil {
		return nil, err
	}
	p.stampBrowse(&b)
	return &b, nil
}

func (p *Plugin) Browse(page string) (*music.Browse, error) {
	var b music.Browse
	if err := p.call("browse", map[string]string{"page": page}, &b); err != nil {
		return nil, err
	}
	p.stampBrowse(&b)
	return &b, nil
}

func (p *Plugin) Playlists() ([]music.Playlist, error) {
	var ps []music.Playlist
	if err := p.call("playlists", nil, &ps); err != nil {
		return nil, err
	}
	for i := range ps {
		p.stamp(ps[i].Tracks)
	}
	return ps, nil
}

func (p *Plugin) Playlist(id string) (*music.Playlist, error) {
	var pl music.Playlist
	if err := p.call("playlist", map[string]string{"id": id}, &pl); err != nil {
		return nil, err
	}
	p.stamp(pl.Tracks)
	return &pl, nil
}

func (p *Plugin) Album(id string) (*music.AlbumPage, error) {
	var a music.AlbumPage
	if err := p.call("album", map[string]string{"id": id}, &a); err != nil {
		return nil, err
	}
	p.stamp(a.Tracks)
	return &a, nil
}

func (p *Plugin) Artist(id string) (*music.ArtistPage, error) {
	var a music.ArtistPage
	if err := p.call("artist", map[string]string{"id": id}, &a); err != nil {
		return nil, err
	}
	p.stamp(a.Tracks)
	return &a, nil
}

func (p *Plugin) Liked(offset int) (*music.Page, error) {
	var page music.Page
	if err := p.call("liked", map[string]int{"offset": offset}, &page); err != nil {
		return nil, err
	}
	p.stamp(page.Tracks)
	return &page, nil
}

func (p *Plugin) Recent() ([]music.Track, error) {
	var tracks []music.Track
	if err := p.call("recent", nil, &tracks); err != nil {
		return nil, err
	}
	p.stamp(tracks)
	return tracks, nil
}

func (p *Plugin) IsLiked(ids []string) (map[string]bool, error) {
	var liked map[string]bool
	if err := p.call("isLiked", map[string][]string{"ids": ids}, &liked); err != nil {
		return nil, err
	}
	return liked, nil
}

func (p *Plugin) Like(id string, on bool) error {
	return p.call("like", map[string]any{"id": id, "on": on}, nil)
}

func (p *Plugin) LikeEpisode(id string, on bool) error {
	return p.call("likeEpisode", map[string]any{"id": id, "on": on}, nil)
}

func (p *Plugin) CreatePlaylist(name string) (*music.Playlist, error) {
	var pl music.Playlist
	if err := p.call("createPlaylist", map[string]string{"name": name}, &pl); err != nil {
		return nil, err
	}
	return &pl, nil
}

func (p *Plugin) RenamePlaylist(id, name string) error {
	return p.call("renamePlaylist", map[string]string{"id": id, "name": name}, nil)
}

func (p *Plugin) DeletePlaylist(id string) error {
	return p.call("deletePlaylist", map[string]string{"id": id}, nil)
}

func (p *Plugin) AddToPlaylist(id string, trackIDs []string) error {
	return p.call("addToPlaylist", map[string]any{"id": id, "trackIds": trackIDs}, nil)
}

func (p *Plugin) RemoveFromPlaylist(id string, trackIDs []string) error {
	return p.call("removeFromPlaylist", map[string]any{"id": id, "trackIds": trackIDs}, nil)
}

func (p *Plugin) MovePlaylistTrack(id string, from, to int) error {
	return p.call("movePlaylistTrack", map[string]any{"id": id, "from": from, "to": to}, nil)
}

func (p *Plugin) SavePlaylist(id string, on bool) error {
	return p.call("savePlaylist", map[string]any{"id": id, "on": on}, nil)
}

func (p *Plugin) FollowArtist(id string, on bool) error {
	return p.call("followArtist", map[string]any{"id": id, "on": on}, nil)
}

func (p *Plugin) SaveAlbum(id string, on bool) error {
	return p.call("saveAlbum", map[string]any{"id": id, "on": on}, nil)
}

func (p *Plugin) SavedAlbums(offset int) (*music.AlbumList, error) {
	var l music.AlbumList
	if err := p.call("savedAlbums", map[string]int{"offset": offset}, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

func (p *Plugin) Shows() ([]music.Show, error) {
	var out []music.Show
	if err := p.call("shows", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Plugin) Show(id string, offset int) (*music.ShowPage, error) {
	var page music.ShowPage
	if err := p.call("show", map[string]any{"id": id, "offset": offset}, &page); err != nil {
		return nil, err
	}
	p.stamp(page.Episodes)
	return &page, nil
}

func (p *Plugin) SubscribeShow(id string, on bool) error {
	return p.call("subscribeShow", map[string]any{"id": id, "on": on}, nil)
}

func (p *Plugin) Stations() ([]music.Station, error) {
	var out []music.Station
	if err := p.call("stations", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Plugin) StationNext(id string) ([]music.Track, error) {
	var tracks []music.Track
	if err := p.call("stationNext", map[string]string{"id": id}, &tracks); err != nil {
		return nil, err
	}
	p.stamp(tracks)
	return tracks, nil
}

func (p *Plugin) Comments(trackID string, offset int) (*music.CommentPage, error) {
	var page music.CommentPage
	if err := p.call("comments", map[string]any{"trackId": trackID, "offset": offset}, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

func (p *Plugin) LikeComment(trackID, commentID string, on bool) error {
	return p.call("likeComment", map[string]any{"trackId": trackID, "commentId": commentID, "on": on}, nil)
}

func (p *Plugin) PostComment(trackID, text string) (*music.Comment, error) {
	var cm music.Comment
	if err := p.call("postComment", map[string]string{"trackId": trackID, "text": text}, &cm); err != nil {
		return nil, err
	}
	return &cm, nil
}

// Played tells the plugin a track plays, if it has "played": for its recent
// list, or whatever else it wants to know that for.
func (p *Plugin) Played(t music.Track) {
	if err := p.call("played", map[string]any{"id": t.ID}, nil); err != nil && !errors.Is(err, music.ErrUnsupported) {
		log.Printf("plugin %s: played: %v", p.manifest.ID, err)
	}
}

func (p *Plugin) Lyrics(id string) (*music.Lyrics, error) {
	var l music.Lyrics
	if err := p.call("lyrics", map[string]string{"id": id}, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

func (p *Plugin) Stream(id, quality string) (*music.Stream, error) {
	var s music.Stream
	if err := p.call("stream", map[string]string{"id": id, "quality": quality}, &s); err != nil {
		return nil, err
	}
	// The plugin's requestHeaders for audio go with it, under what the
	// stream says itself.
	if u, err := url.Parse(s.URL); err == nil {
		for k, v := range p.HeadersFor(u, KindAudio) {
			if s.Headers == nil {
				s.Headers = map[string]string{}
			}
			if !hasHeader(s.Headers, k) {
				s.Headers[k] = v
			}
		}
	}
	return &s, nil
}

func (p *Plugin) RemotePlay(ids []string, start int, position int64) error {
	return p.call("remotePlay", map[string]any{"ids": ids, "start": start, "position": position}, nil)
}

func (p *Plugin) RemotePause() error  { return p.call("remotePause", nil, nil) }
func (p *Plugin) RemoteResume() error { return p.call("remoteResume", nil, nil) }

func (p *Plugin) RemoteSeek(position int64) error {
	return p.call("remoteSeek", map[string]int64{"position": position}, nil)
}

func (p *Plugin) RemoteVolume(percent int) error {
	return p.call("remoteVolume", map[string]int{"percent": percent}, nil)
}

// RemoteState is what the plugin is playing, or nil if nothing is.
func (p *Plugin) RemoteState() (*music.RemoteState, error) {
	var s *music.RemoteState
	if err := p.call("remoteState", nil, &s); err != nil {
		return nil, err
	}
	return s, nil
}

func (p *Plugin) RemoteDevices() ([]music.Device, error) {
	var ds []music.Device
	if err := p.call("remoteDevices", nil, &ds); err != nil {
		return nil, err
	}
	return ds, nil
}

func (p *Plugin) RemoteUse(deviceID string) error {
	return p.call("remoteUse", map[string]string{"deviceId": deviceID}, nil)
}

var (
	_ music.Source   = (*Plugin)(nil)
	_ music.Recorder = (*Plugin)(nil)
)

// hasHeader reports whether headers has name, in any case.
func hasHeader(headers map[string]string, name string) bool {
	for k := range headers {
		if strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}
