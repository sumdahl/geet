package spotify

import (
	"errors"
	"strconv"
	"time"
)

var ErrNotFound = errors.New("spotify resource not found")

type Track struct {
	ID          string
	Title       string
	Artists     []string
	AlbumArtist string
	Album       string
	CoverURL    string
	TrackNumber int
	DiscNumber  int
	Year        int
	Duration    time.Duration
	ISRC        string
	// SourceURL is where the track came from when that isn't Spotify (an
	// Apple Music link for search results); URL returns it when set.
	SourceURL string
	// Clean marks a clean (censored) edit of an explicit song.
	Clean bool
	// Explicit marks a song with explicit lyrics. Its explicit upload is
	// preferred, and a clean edit is only a fallback.
	Explicit bool
}

// Collection is what a Spotify link resolves to.
type Collection struct {
	Ref    Ref
	Name   string // the track's title, the album's or the playlist's name
	Tracks []Track
	// Total is how many items the source says the collection has, when
	// that's more than it let us read: Spotify's public playlist page lists
	// at most 100. Zero when everything was read.
	Total int
}

func collect(ref Ref, name string, tracks []Track) Collection {
	if name == "" && len(tracks) > 0 {
		name = tracks[0].Album
		if ref.Kind == KindTrack {
			name = tracks[0].Title
		}
	}
	return Collection{Ref: ref, Name: name, Tracks: tracks}
}

func (t Track) URL() string {
	if t.SourceURL != "" {
		return t.SourceURL
	}
	return Ref{Kind: KindTrack, ID: t.ID}.URL()
}

// Dates come as "2011", "2011-03", "2011-03-14" or a full ISO timestamp.
func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}
