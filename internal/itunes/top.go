package itunes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/sumdahl/geet/internal/spotify"
)

// feedBaseURL is Apple's marketing feed: the most played songs in a
// country's store, keyless and refreshed daily.
//
// The older itunes.apple.com/<cc>/rss/topsongs feed still answers, but it
// returns years-old entries — do not use it.
const feedBaseURL = "https://rss.marketingtools.apple.com/api/v2"

// FeedBaseURL lets a test point the feed at its own server.
var FeedBaseURL = feedBaseURL

// TopSongs returns the most played songs in the client's store country,
// most played first. The feed gives names and ids only, so each song is
// looked up to get the details the rest of geet needs (length, ISRC via
// Deezer later, artwork size, explicit flag).
func (c *Client) TopSongs(ctx context.Context, limit int) ([]spotify.Track, error) {
	if limit < 1 {
		limit = 25
	}
	country := strings.ToLower(c.country)
	if country == "" {
		country = "us"
	}
	path := fmt.Sprintf("%s/%s/music/most-played/%d/songs.json", FeedBaseURL, url.PathEscape(country), limit)
	body, err := c.fetch(ctx, path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Feed struct {
			Results []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				ArtistName string `json:"artistName"`
			} `json:"results"`
		} `json:"feed"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("apple top songs: %w", err)
	}

	tracks := make([]spotify.Track, 0, len(doc.Feed.Results))
	for _, r := range doc.Feed.Results {
		if _, err := strconv.ParseInt(r.ID, 10, 64); err != nil {
			continue
		}
		full, err := c.Lookup(ctx, r.ID)
		if err != nil {
			// A song the store lists but won't look up is not worth
			// failing the whole chart for.
			continue
		}
		tracks = append(tracks, full)
	}
	return tracks, nil
}
