package itunes

import (
	"slices"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
)

const (
	// cleanEditDiff is how far a clean edit's length may be from the
	// explicit version's: they're the same recording with words muted or
	// swapped, a second or so apart.
	cleanEditDiff = 5 * time.Second
	// minCleanCoverage is the share of the explicit title's words the clean
	// edit must have: "Tonight (I'm Lovin' You)" has 4 of "Tonight (I'm
	// Fuckin' You)"'s 5, while another song by the same artist ("I Like
	// It") has 1.
	minCleanCoverage = 0.6
)

// CleanEditTerm is what to search the catalog for to find t's clean edit.
func CleanEditTerm(t spotify.Track) string {
	artist := ""
	if len(t.Artists) > 0 {
		artist = t.Artists[0]
	}
	return strings.TrimSpace(artist + " " + textnorm.StripVersion(t.Title))
}

// CleanEdit finds the clean edit of the explicit track t among a catalog
// search's results: by the same primary artist, not explicit, within 5s of
// t's length, and with most of t's title (not counting a "(feat. …)" part).
// A clean edit swaps words ("I'm Fuckin' You" → "I'm Lovin' You") or cuts
// them off ("fukumean" → "umean"), so a word also matches a clean edit's
// word that ends it. Edits the catalog marks "cleaned" win over ones that
// are simply not explicit.
func CleanEdit(t spotify.Track, results []spotify.Track) (spotify.Track, bool) {
	if len(t.Artists) == 0 {
		return spotify.Track{}, false
	}
	artist := textnorm.Norm(t.Artists[0])
	want := textnorm.Words(textnorm.StripFeat(t.Title))
	var best spotify.Track
	bestScore := -1.0
	for _, r := range results {
		if r.Explicit || len(r.Artists) == 0 || textnorm.Norm(r.Artists[0]) != artist ||
			(r.Duration-t.Duration).Abs() > cleanEditDiff ||
			len(textnorm.Variants(r.Title+" "+r.Album, t.Title)) > 0 {
			continue
		}
		have := textnorm.Words(textnorm.StripFeat(r.Title))
		cover := cleanCoverage(want, have)
		if cover < minCleanCoverage {
			continue
		}
		score := cover
		if r.Clean {
			score += 1
		}
		if score > bestScore {
			best, bestScore = r, score
		}
	}
	if bestScore < 0 {
		return spotify.Track{}, false
	}
	best.Clean = true
	return best, true
}

func cleanCoverage(want, have []string) float64 {
	if len(want) == 0 {
		return 0
	}
	hit := 0
	for _, w := range want {
		if slices.ContainsFunc(have, func(h string) bool {
			return textnorm.WordMatch(w, h) || (len(h) >= 3 && strings.HasSuffix(w, h))
		}) {
			hit++
		}
	}
	return float64(hit) / float64(len(want))
}
