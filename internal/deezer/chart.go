package deezer

import (
	"context"
	"net/url"
	"strconv"

	"github.com/sumdahl/geet/internal/spotify"
)

// Chart returns the songs Deezer's listeners are playing now, most played
// first. The endpoint needs no key and is localised by where the request
// comes from: from Nepal it mixes Nepali songs in with the global hits,
// which is the whole point of showing a chart.
func (c *Client) Chart(ctx context.Context, limit int) ([]spotify.Track, error) {
	if limit < 1 {
		limit = 25
	}
	if limit > 100 {
		limit = 100 // the endpoint's own cap
	}
	var page struct {
		Data []catalogTrack `json:"data"`
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	// Chart 0 is "all genres".
	if err := c.get(ctx, "/chart/0/tracks?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	tracks := make([]spotify.Track, 0, len(page.Data))
	for _, d := range page.Data {
		// A chart entry names only the main artist and has no year or
		// ISRC; Lookup fills those in when a song is actually picked.
		tracks = append(tracks, d.toTrack())
	}
	return tracks, nil
}
