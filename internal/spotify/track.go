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
}

func (t Track) URL() string { return Ref{Kind: KindTrack, ID: t.ID}.URL() }

// Dates come as "2011", "2011-03", "2011-03-14" or a full ISO timestamp.
func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}
