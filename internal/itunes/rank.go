package itunes

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
)

// Result is a ranked search hit: one recording, however many album
// editions list it.
type Result struct {
	spotify.Track
	Editions int
	Score    float64
}

const (
	sameLength     = 2 * time.Second
	weightQuery    = 10.0 // share of the query's words found in title + artists
	penaltyVariant = 5.0  // per remix/cover/instrumental/... word the query didn't ask for
	penaltyClean   = 0.5  // an explicit original, when listed, beats its clean edit
	weightEdition  = 0.8  // per extra edition, capped: a popularity proxy
	maxEditions    = 5
)

// Rank merges album editions of the same recording and orders the results
// best first. The catalog's own order puts covers and remixes above
// originals ("Blinding Lights" by The Weeknd came 7th), so it only breaks
// ties. How many editions list a recording is the popularity signal: the
// original sits on the album, its deluxe edition and compilations, while a
// cover is usually a lone single.
func Rank(query string, tracks []spotify.Track) []Result {
	var groups []Result
	for _, t := range tracks {
		i := slices.IndexFunc(groups, func(g Result) bool { return sameRecording(g.Track, t) })
		if i < 0 {
			groups = append(groups, Result{Track: t, Editions: 1})
			continue
		}
		groups[i].Editions++
		if t.Year > 0 && t.Year < groups[i].Year {
			groups[i].Track = t // the original release rather than a later reissue
		}
	}

	words := textnorm.Words(query)
	for i := range groups {
		g := &groups[i]
		have := textnorm.Words(g.Title + " " + strings.Join(g.Artists, " "))
		g.Score = weightQuery * coverage(words, have)
		g.Score -= penaltyVariant * float64(len(textnorm.Variants(g.Title, query)))
		// Tribute and lullaby acts give themselves away in the album or
		// artist name while keeping the plain title ("Rockabye Baby! ·
		// Lullaby Renditions of the Weeknd"), so those count half.
		g.Score -= penaltyVariant / 2 * float64(len(textnorm.Variants(g.Album+" "+strings.Join(g.Artists, " "), query)))
		g.Score += weightEdition * float64(min(g.Editions-1, maxEditions))
		if g.Clean {
			g.Score -= penaltyClean
		}
	}
	slices.SortStableFunc(groups, func(a, b Result) int { return cmp.Compare(b.Score, a.Score) })
	return groups
}

func sameRecording(a, b spotify.Track) bool {
	return textnorm.Norm(a.Title) == textnorm.Norm(b.Title) &&
		textnorm.Norm(strings.Join(a.Artists, " ")) == textnorm.Norm(strings.Join(b.Artists, " ")) &&
		(a.Duration-b.Duration).Abs() <= sameLength
}

func coverage(want, have []string) float64 {
	if len(want) == 0 {
		return 0
	}
	hit := 0
	for _, w := range want {
		if slices.ContainsFunc(have, func(h string) bool { return textnorm.WordMatch(w, h) }) {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}
