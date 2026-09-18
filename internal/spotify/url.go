package spotify

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var ErrInvalidURL = errors.New("not a Spotify track, album or playlist URL")

type Kind string

const (
	KindTrack    Kind = "track"
	KindAlbum    Kind = "album"
	KindPlaylist Kind = "playlist"
)

type Ref struct {
	Kind Kind
	ID   string
}

func (r Ref) URL() string {
	return "https://open.spotify.com/" + string(r.Kind) + "/" + r.ID
}

// ParseURL accepts open.spotify.com links (with or without a locale prefix
// such as /intl-de/ and share query strings) and spotify:{kind}:{id} URIs,
// which is what MPRIS reports as the track id.
func ParseURL(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	var kind, id string
	if rest, ok := strings.CutPrefix(s, "spotify:"); ok {
		parts := strings.Split(rest, ":")
		if len(parts) != 2 {
			return Ref{}, fmt.Errorf("%w: %q", ErrInvalidURL, s)
		}
		kind, id = parts[0], parts[1]
	} else {
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host != "open.spotify.com" {
			return Ref{}, fmt.Errorf("%w: %q", ErrInvalidURL, s)
		}
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) > 0 && strings.HasPrefix(segs[0], "intl-") {
			segs = segs[1:]
		}
		if len(segs) != 2 {
			return Ref{}, fmt.Errorf("%w: %q", ErrInvalidURL, s)
		}
		kind, id = segs[0], segs[1]
	}

	k := Kind(kind)
	if k != KindTrack && k != KindAlbum && k != KindPlaylist {
		return Ref{}, fmt.Errorf("%w: unsupported type %q", ErrInvalidURL, kind)
	}
	if !validID(id) {
		return Ref{}, fmt.Errorf("%w: bad id %q", ErrInvalidURL, id)
	}
	return Ref{Kind: k, ID: id}, nil
}

func validID(id string) bool {
	if len(id) != 22 {
		return false
	}
	for _, c := range id {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z') {
			return false
		}
	}
	return true
}
