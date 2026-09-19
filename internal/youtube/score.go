package youtube

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
)

// rejectNoArtist is the one rejection an exact re-upload may overcome (see
// Alternatives).
const rejectNoArtist = "no artist named"

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

// Scored is a candidate with its verdict, kept for all candidates so a
// consumer can explain why a match won.
type Scored struct {
	Candidate
	Score  float64
	Reject string // non-empty if the candidate was ruled out
	// TitleOnly marks a pick matched by title and length alone, the upload
	// not naming the artist (see Resolver.resolveTitleOnly).
	TitleOnly bool
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
	coverage := tokenCoverage(titleWords(t), textnorm.Words(c.Title))
	if coverage < minTitleCoverage {
		return reject("title matches %.0f%%", coverage*100)
	}
	s.Score += weightTitle * coverage

	channel := textnorm.Tokens(c.Channel)
	switch artistMatch(t.Artists, append(slices.Clone(ytTitle), channel...), c.Channel) {
	case artistNone:
		return reject(rejectNoArtist)
	case artistPrimary:
		s.Score += weightArtist
	case artistFeatured:
		s.Score += weightArtist / 2
	case artistCore:
		s.Score += weightArtist / 4
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

	for range textnorm.Variants(c.Title, t.Title) {
		s.Score -= penaltyVariant
	}
	for range otherEdit(t, c.Title) {
		s.Score -= penaltyVariant
	}
	// A clean edit differs from the explicit song only inside its title's
	// brackets ("Tonight (I'm Lovin' You)" vs "(I'm Fuckin' You)"), so an
	// upload missing those words is likely the explicit one.
	if t.Clean && !fullTitle(t, c.Title) {
		s.Score -= penaltyVariant
	}
	yt := " " + strings.Join(ytTitle, " ") + " "
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

var (
	// cleanWords mark a clean (censored) edit; explicitWords an uncensored
	// one. Matched as whole-word phrases against textnorm.Norm output.
	cleanWords    = []string{"clean", "radio edit", "radio version", "censored", "edited"}
	explicitWords = []string{"explicit", "dirty", "uncensored"}
)

// otherEdit returns the words in title that mark the other edit of the
// song: a clean edit when t is explicit, and an explicit one when t is the
// clean edit. The explicit version is the default, so it's what an
// explicit track must get; a clean edit is only a fallback.
func otherEdit(t spotify.Track, title string) []string {
	var marks []string
	switch {
	case t.Explicit:
		marks = cleanWords
	case t.Clean:
		marks = explicitWords
	default:
		return nil
	}
	have := " " + textnorm.Norm(title) + " "
	own := " " + textnorm.Norm(t.Title) + " "
	var out []string
	for _, w := range marks {
		if strings.Contains(have, " "+w+" ") && !strings.Contains(own, " "+w+" ") {
			out = append(out, w)
		}
	}
	return out
}

// titleWords are the words that identify the song: the base title without a
// "(feat. X)" or " - Remastered" suffix, which uploads often leave out.
// Censored words keep their asterisks as wildcards. A clean edit's title
// has the explicit part cut off ("umean" for "fukumean"), so its words may
// match the end of an upload's word.
func titleWords(t spotify.Track) []string {
	words := textnorm.Words(textnorm.StripVersion(t.Title))
	if len(words) == 0 {
		words = textnorm.Words(t.Title)
	}
	return cutOff(t, words)
}

// cutOff lets a clean edit's words match the end of an upload's word, as
// its title may have the explicit part cut off ("umean" for "fukumean").
func cutOff(t spotify.Track, words []string) []string {
	if t.Clean {
		for i, w := range words {
			if len(w) >= 3 && !strings.Contains(w, "*") {
				words[i] = "*" + w
			}
		}
	}
	return words
}

// tokenCoverage is the share of want found in have, comparing with
// textnorm.WordMatch so "ni**as" finds "niggas". Words split differently
// still match either way: "1Train" finds "1 Train", and "1 Train" finds
// "1Train".
func tokenCoverage(want, have []string) float64 {
	if len(want) == 0 {
		return 0
	}
	have = withJoins(have)
	found := func(w string) bool {
		return slices.ContainsFunc(have, func(h string) bool { return textnorm.WordMatch(w, h) })
	}
	hit := 0
	for i := 0; i < len(want); i++ {
		switch {
		case found(want[i]):
			hit++
		case i+1 < len(want) && found(want[i]+want[i+1]):
			hit += 2
			i++
		}
	}
	return float64(hit) / float64(len(want))
}

// withJoins returns words plus each pair and triple of neighbouring words
// run together.
func withJoins(words []string) []string {
	out := slices.Clone(words)
	for i := range words {
		if i+1 < len(words) {
			out = append(out, words[i]+words[i+1])
		}
		if i+2 < len(words) {
			out = append(out, words[i]+words[i+1]+words[i+2])
		}
	}
	return out
}

// How an upload names the song's artists, strongest first.
const (
	artistNone     = iota
	artistPrimary  // the primary artist, in full
	artistFeatured // only a featured artist, in full
	// artistCore: only the distinctive part of an artist's name ("Kush" of
	// "Kush Band Nepal"). Tried only when no name is found in full, and
	// scored below a full name, so an upload that names the artist in full
	// always wins, and what matched before still matches the same way.
	artistCore
)

// genericNameWords are words artists add to a name to tell it apart on a
// streaming service, while uploads often use the bare name: "Kush Band
// Nepal" is "KUSH" on YouTube. Only whole words, and only when the name has
// something else left.
var genericNameWords = map[string]bool{
	"band": true, "the": true, "official": true, "music": true, "group": true,
	"nepal": true, "nepali": true, "np": true, "india": true, "indian": true,
}

// minCoreName is the shortest distinctive part matched on its own: shorter
// ones ("dj", "mc") are too common to identify an artist.
const minCoreName = 3

// coreName is the distinctive words of an artist's name, or nil when there
// are no generic words to leave out, or nothing distinctive is left.
func coreName(artist string) []string {
	var core []string
	generic := false
	for _, w := range textnorm.Tokens(artist) {
		if genericNameWords[w] {
			generic = true
			continue
		}
		core = append(core, w)
	}
	if !generic || len(strings.Join(core, "")) < minCoreName {
		return nil
	}
	return core
}

// minHandleName is the shortest artist name found run together at the start
// of a channel name; shorter ones ("Ye", "SZA") would match unrelated
// channels.
const minHandleName = 5

// artistMatch returns 1 if the primary artist is fully named in tokens or
// opens the channel name as one word ("ASAPROCKYUPTOWN" for A$AP Rocky), 2
// if only a featured artist is, 0 if none is.
func artistMatch(artists []string, tokens []string, channel string) int {
	handle := textnorm.Compact(channel)
	for i, a := range artists {
		at := textnorm.Tokens(a)
		name := strings.Join(at, "")
		named := len(at) > 0 && tokenCoverage(at, tokens) == 1
		if !named && len(name) >= minHandleName && strings.HasPrefix(handle, name) {
			named = true
		}
		if named {
			if i == 0 {
				return artistPrimary
			}
			return artistFeatured
		}
	}
	for _, a := range artists {
		if core := coreName(a); core != nil && tokenCoverage(core, tokens) == 1 {
			return artistCore
		}
	}
	return artistNone
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

// reuploadDiff is how close a re-upload's length must be: a copy of the
// same audio keeps it to the second, while a different recording or edit
// rarely lands within 2s.
const reuploadDiff = 2 * time.Second

// Alternatives lists what to try when best can't be downloaded, because
// YouTube age-restricts it: the other accepted candidates, best first, then
// exact re-uploads. A re-upload was rejected only for not naming the artist
// (a fan channel re-posting the official audio), but has the song's full
// title and its length within 2s. Neither may carry variant or other-edit
// words: matching accepts a remix at a penalty when nothing better exists,
// but a stand-in for the recording must be the same recording.
func Alternatives(t spotify.Track, all []Scored, best Scored) []Scored {
	seen := map[string]bool{best.ID: true}
	var ok, reuploads []Scored
	for _, s := range all {
		if seen[s.ID] {
			continue
		}
		switch {
		case s.Reject == "" && sameRecording(t, s.Candidate):
			ok = append(ok, s)
		case s.Reject == rejectNoArtist && exactReupload(t, s.Candidate):
			reuploads = append(reuploads, s)
		default:
			continue
		}
		seen[s.ID] = true
	}
	slices.SortStableFunc(ok, func(a, b Scored) int { return cmp.Compare(b.Score, a.Score) })
	return append(ok, reuploads...)
}

// sameRecording is how sure a stand-in must be: no variant or other-edit
// words, and all of the title but a "(feat. …)" part, since what tells an
// explicit song from its clean edit is often only inside its brackets.
func sameRecording(t spotify.Track, c Candidate) bool {
	return len(textnorm.Variants(c.Title, t.Title)) == 0 && len(otherEdit(t, c.Title)) == 0 && fullTitle(t, c.Title) &&
		!hasWord(c.Title, t.Title, performanceWords) && !hasStem(c.Title, t.Title, versionStems)
}

// versionStems catch another version's words however they're spelled or
// inflected ("karoke", "instrumentally", "covered by"), which the
// whole-word variant list misses.
var versionStems = []string{"instrumental", "karaok", "karok", "cover", "playthrough"}

// hasStem reports whether a word of title, but none of reference, starts
// with one of stems.
func hasStem(title, reference string, stems []string) bool {
	has := func(s, stem string) bool {
		for _, w := range textnorm.Tokens(s) {
			if strings.HasPrefix(w, stem) {
				return true
			}
		}
		return false
	}
	for _, stem := range stems {
		if has(title, stem) && !has(reference, stem) {
			return true
		}
	}
	return false
}

// performanceWords mark someone else performing the song on a show or at
// an event ("ryhaan giri | harayeko graha | the voice of Nepal season 4",
// 1 s from the original's length). Matching tolerates them at a score, but
// a stand-in for the recording (an age-restricted upload's, or one matched
// by title alone) must not be one.
var performanceWords = []string{
	"the voice", "season", "episode", "audition", "idol", "x factor", "got talent",
	"contest", "competition", "session", "sessions", "tribute", "karaoke",
}

// hasWord reports whether title has one of words (whole words, after
// textnorm.Norm) that reference, the song's own title, doesn't.
func hasWord(title, reference string, words []string) bool {
	have := " " + textnorm.Norm(title) + " "
	own := " " + textnorm.Norm(reference) + " "
	for _, w := range words {
		if strings.Contains(have, " "+w+" ") && !strings.Contains(own, " "+w+" ") {
			return true
		}
	}
	return false
}

// fullTitle reports whether title has every word of t's title, not counting
// a "(feat. …)" part.
func fullTitle(t spotify.Track, title string) bool {
	return tokenCoverage(cutOff(t, textnorm.Words(textnorm.StripFeat(t.Title))), textnorm.Words(title)) == 1
}

func exactReupload(t spotify.Track, c Candidate) bool {
	return !c.Live && (c.Duration-t.Duration).Abs() <= reuploadDiff && sameRecording(t, c)
}
