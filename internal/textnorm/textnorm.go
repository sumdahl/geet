// Package textnorm normalizes titles and names for fuzzy matching across
// Spotify, Deezer and YouTube.
package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// VariantWords mark a different or altered recording. Matching penalizes one
// found in a candidate's title unless the wanted title has it too ("Song
// (Live)" should still match a live upload). Multi-word entries are matched
// as whole-word phrases against Norm output.
var VariantWords = []string{
	"live", "concert", "cover", "remix", "karaoke", "instrumental", "acoustic",
	"demo", "sped up", "slowed", "reverb", "nightcore", "8d", "extended",
	"mashup", "parody", "reaction", "tutorial", "lesson", "isolated", "solo",
	"1 hour", "loop", "bass boosted", "tribute", "lullaby", "rendition",
	"8 bit", "lofi", "lo fi", "jersey club", "chopped", "screwed",
}

// Variants returns the VariantWords in title that aren't also in reference.
func Variants(title, reference string) []string {
	t := " " + Norm(title) + " "
	r := " " + Norm(reference) + " "
	var out []string
	for _, w := range VariantWords {
		if strings.Contains(t, " "+w+" ") && !strings.Contains(r, " "+w+" ") {
			out = append(out, w)
		}
	}
	return out
}

// Norm lowercases s, folds accents off Latin letters ("JAŸ-Z" → "jay z",
// "Beyoncé" → "beyonce"), reads a "$" in a name as "s" ("A$AP" → "asap")
// and turns punctuation into single spaces.
func Norm(s string) string {
	return strings.Join(words(s, false), " ")
}

func Tokens(s string) []string {
	return words(s, false)
}

// Words is Tokens, except that an asterisk inside a word is kept as a
// wildcard: Spotify censors titles ("Ni**as In Paris") while uploads spell
// them out or censor differently. Compare the results with WordMatch.
func Words(s string) []string {
	return words(s, true)
}

// Base drops a version suffix such as " - Remastered 2009", "(feat. X)" or
// "[Live]" and normalizes what's left.
func Base(s string) string {
	return Norm(StripVersion(s))
}

// StripVersion cuts s before a "(", "[" or " - " suffix, leaving the title
// itself; s is returned unchanged if the cut would leave nothing.
func StripVersion(s string) string {
	if i := strings.IndexAny(s, "(["); i > 0 {
		s = s[:i]
	}
	if i := strings.Index(s, " - "); i > 0 {
		s = s[:i]
	}
	return s
}

// WordMatch reports whether two words from Words are equal, treating "*" in
// either as any run of letters: "ni**as" matches "niggas" and "n****s".
func WordMatch(a, b string) bool {
	if a == b {
		return true
	}
	if strings.Contains(a, "*") && glob(a, b) {
		return true
	}
	return strings.Contains(b, "*") && glob(b, a)
}

// glob matches s against pattern, where each run of "*" matches any
// (possibly empty) run of characters. A pattern of only asterisks matches
// nothing: it carries no information.
func glob(pattern, s string) bool {
	parts := strings.FieldsFunc(pattern, func(r rune) bool { return r == '*' })
	if len(parts) == 0 {
		return false
	}
	if !strings.HasPrefix(pattern, "*") {
		if !strings.HasPrefix(s, parts[0]) {
			return false
		}
		s = s[len(parts[0]):]
		parts = parts[1:]
	}
	for i, p := range parts {
		if i == len(parts)-1 && !strings.HasSuffix(pattern, "*") {
			return strings.HasSuffix(s, p)
		}
		j := strings.Index(s, p)
		if j < 0 {
			return false
		}
		s = s[j+len(p):]
	}
	return true
}

// dollarAsS spells a "$" that touches a letter as "s": artists write their
// names with one ("A$AP Rocky", "Joey Bada$$", "Ty Dolla $ign") and uploads
// often spell it out ("ASAP Rocky", "Joey Badass"). A "$" by digits, as in
// "$100", stays punctuation.
func dollarAsS(s string) string {
	if !strings.Contains(s, "$") {
		return s
	}
	r := []rune(s)
	for i := 0; i < len(r); {
		if r[i] != '$' {
			i++
			continue
		}
		j := i
		for j < len(r) && r[j] == '$' {
			j++
		}
		if (i > 0 && unicode.IsLetter(r[i-1])) || (j < len(r) && unicode.IsLetter(r[j])) {
			for k := i; k < j; k++ {
				r[k] = 's'
			}
		}
		i = j
	}
	return string(r)
}

// Compact is s's words run together, "ASAP Rocky" → "asaprocky", for
// matching names written without spaces, such as channel handles.
func Compact(s string) string {
	return strings.Join(Tokens(s), "")
}

func words(s string, keepStars bool) []string {
	s = dollarAsS(s)
	var out []string
	var w strings.Builder
	flush := func() {
		word := w.String()
		w.Reset()
		if word != "" {
			out = append(out, word)
		}
	}
	// NFD splits "ÿ" into "y" plus a combining mark. Marks after a Latin
	// letter are accents and get dropped; every other mark is kept, because
	// Devanagari vowel signs are marks too and dropping those would split
	// every Nepali or Hindi word apart.
	prevLatin := false
	for _, r := range norm.NFD.String(s) {
		switch {
		case unicode.IsMark(r):
			if !prevLatin {
				w.WriteRune(r)
			}
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			w.WriteRune(unicode.ToLower(r))
			prevLatin = unicode.Is(unicode.Latin, r)
		case keepStars && r == '*' && w.Len() > 0:
			w.WriteRune(r)
			prevLatin = false
		default:
			flush()
			prevLatin = false
		}
	}
	flush()
	return out
}
