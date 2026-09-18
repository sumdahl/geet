package youtube

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
)

const (
	minTitleCoverage = 0.6

	weightTitle    = 40.0
	weightArtist   = 20.0
	weightOfficial = 20.0
	weightTopic    = 5.0
	weightVerified = 5.0
	weightAudio    = 5.0
	weightDuration = 15.0
	weightRank     = 5.0
	penaltyVariant = 35.0
	penaltyVideo   = 3.0
)

// Words that mark a different recording or an altered one. Each costs
// penaltyVariant unless the Spotify title has it too ("Song (Live)" should
// still match a live upload).
var variantWords = []string{
	"live", "concert", "cover", "remix", "karaoke", "instrumental", "acoustic",
	"demo", "sped up", "slowed", "reverb", "nightcore", "8d", "extended",
	"mashup", "parody", "reaction", "tutorial", "lesson", "isolated", "solo",
	"1 hour", "loop", "bass boosted",
}

// Scored is a candidate with its verdict, kept for all candidates so a
// consumer can explain why a match won.
type Scored struct {
	Candidate
	Score  float64
	Reject string // non-empty if the candidate was ruled out
}

func score(t spotify.Track, c Candidate, rank, n int, maxDiff time.Duration) Scored {
	s := Scored{Candidate: c}
	reject := func(format string, args ...any) Scored {
		s.Reject = fmt.Sprintf(format, args...)
		return s
	}

	if c.Live {
		return reject("live stream")
	}
	if c.Duration <= 0 {
		return reject("unknown length")
	}
	diff := (c.Duration - t.Duration).Abs()
	if diff > maxDiff {
		return reject("length off by %s", diff.Round(time.Second))
	}

	ytTitle := textnorm.Tokens(c.Title)
	coverage := tokenCoverage(titleWords(t.Title), textnorm.Words(c.Title))
	if coverage < minTitleCoverage {
		return reject("title matches %.0f%%", coverage*100)
	}
	s.Score += weightTitle * coverage

	channel := textnorm.Tokens(c.Channel)
	switch artistMatch(t.Artists, append(slices.Clone(ytTitle), channel...)) {
	case 0:
		return reject("no artist named")
	case 1:
		s.Score += weightArtist
	default:
		s.Score += weightArtist / 2
	}

	official, topic := officialChannel(t.Artists, c.Channel, c.Verified)
	if official {
		s.Score += weightOfficial
	}
	if topic {
		s.Score += weightTopic
	}
	if c.Verified {
		s.Score += weightVerified
	}

	spTitle := " " + textnorm.Norm(t.Title) + " "
	yt := " " + strings.Join(ytTitle, " ") + " "
	for _, w := range variantWords {
		if strings.Contains(yt, " "+w+" ") && !strings.Contains(spTitle, " "+w+" ") {
			s.Score -= penaltyVariant
		}
	}
	switch {
	case strings.Contains(yt, " audio "):
		s.Score += weightAudio
	case strings.Contains(yt, " official video ") || strings.Contains(yt, " music video "):
		// Videos often carry intros or skits; duration already bounds
		// this, the nudge only breaks ties toward a clean audio upload.
		s.Score -= penaltyVideo
	}

	s.Score += weightDuration * (1 - float64(diff)/float64(maxDiff))
	s.Score += weightRank * float64(n-rank) / float64(n)
	return s
}

// titleWords are the words that identify the song: the base title without a
// "(feat. X)" or " - Remastered" suffix, which uploads often leave out.
// Censored words keep their asterisks as wildcards.
func titleWords(title string) []string {
	if b := textnorm.Words(textnorm.StripVersion(title)); len(b) > 0 {
		return b
	}
	return textnorm.Words(title)
}

// tokenCoverage is the share of want found in have, comparing with
// textnorm.WordMatch so "ni**as" finds "niggas".
func tokenCoverage(want, have []string) float64 {
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

// artistMatch returns 1 if the primary artist is fully named in tokens, 2 if
// only a featured artist is, 0 if none is.
func artistMatch(artists []string, tokens []string) int {
	for i, a := range artists {
		at := textnorm.Tokens(a)
		if len(at) > 0 && tokenCoverage(at, tokens) == 1 {
			if i == 0 {
				return 1
			}
			return 2
		}
	}
	return 0
}

// officialChannel recognizes the artist's own channel ("The Weeknd"), its
// auto-generated "The Weeknd - Topic" and "TheWeekndVEVO", and a verified
// channel using part of the name ("Sajjan" for Sajjan Raj Vaidya).
func officialChannel(artists []string, channel string, verified bool) (official, topic bool) {
	ch := textnorm.Norm(channel)
	if ch == "" {
		return false, false
	}
	compact := strings.ReplaceAll(ch, " ", "")
	for _, a := range artists {
		an := textnorm.Norm(a)
		if an == "" {
			continue
		}
		ac := strings.ReplaceAll(an, " ", "")
		switch {
		case ch == an+" topic":
			return true, true
		case ch == an, compact == ac, compact == ac+"vevo", compact == ac+"official":
			return true, false
		case verified && strings.HasPrefix(an+" ", ch+" "):
			return true, false
		}
	}
	return false, false
}
