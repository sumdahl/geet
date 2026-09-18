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
	weightArtist   = 3.0  // the result's own artist is named in the query
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
		g := &groups[i]
		g.Editions++
		switch {
		case t.Explicit && !g.Explicit:
			// One catalog marks the song explicit and the other doesn't:
			// keep the explicit one, whose download gets the explicit
			// upload.
			year := g.Year
			g.Track = t
			if g.Year == 0 {
				g.Year = year
			}
		case t.Year > 0 && (g.Year == 0 || t.Year < g.Year) && t.Explicit == g.Explicit:
			g.Track = t // the original release rather than a later reissue
		}
	}

	words := textnorm.Words(query)
	for i := range groups {
		g := &groups[i]
		have := textnorm.Words(g.Title + " " + strings.Join(g.Artists, " "))
		g.Score = weightQuery * coverage(words, have, g.Clean)
		// "gunna fukumean" should put Gunna's songs above other artists'
		// uploads titled "Fukumean Gunna": both have every query word,
		// but only one is by Gunna.
		if artistNamed(words, g.Artists) {
			g.Score += weightArtist
		}
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
	return explicitFirst(groups)
}

// explicitFirst moves each explicit song up to just above its own clean edit
// when the edit ranked higher (it can have more album editions): the
// explicit version is the default, and the clean one stays listed right
// below it for whoever wants it.
func explicitFirst(results []Result) []Result {
	for j := 0; j < len(results); j++ {
		if !results[j].Explicit {
			continue
		}
		for i := 0; i < j; i++ {
			if _, edit := CleanEdit(results[j].Track, []spotify.Track{results[i].Track}); edit {
				e := results[j]
				copy(results[i+1:j+1], results[i:j])
				results[i] = e
				break
			}
		}
	}
	return results
}

// sameRecording merges one song's album editions, including the same song
// from both catalogs (Apple writes "Song [feat. X]", Deezer "Song"). A clean
// edit is never merged with its explicit original.
func sameRecording(a, b spotify.Track) bool {
	return a.Clean == b.Clean &&
		textnorm.Norm(textnorm.StripFeat(a.Title)) == textnorm.Norm(textnorm.StripFeat(b.Title)) &&
		len(a.Artists) > 0 && len(b.Artists) > 0 &&
		textnorm.Norm(a.Artists[0]) == textnorm.Norm(b.Artists[0]) &&
		(a.Duration-b.Duration).Abs() <= sameLength
}

// coverage is the share of want found in have. A clean edit's title may
// have the explicit part cut off ("umean" for "fukumean"), so for one, a
// word also matches a word that ends it.
func coverage(want, have []string, clean bool) float64 {
	if len(want) == 0 {
		return 0
	}
	hit := 0
	for _, w := range want {
		if slices.ContainsFunc(have, func(h string) bool {
			return textnorm.WordMatch(w, h) || (clean && len(h) >= 3 && strings.HasSuffix(w, h))
		}) {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}

// artistNamed reports whether every word of the primary artist's name is
// in the query.
func artistNamed(query []string, artists []string) bool {
	if len(artists) == 0 {
		return false
	}
	name := textnorm.Words(artists[0])
	return len(name) > 0 && coverage(name, query, false) == 1
}
