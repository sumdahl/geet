// Package textnorm normalizes titles and names for fuzzy matching across
// Spotify, Deezer and YouTube.
package textnorm

import (
	"strings"
	"unicode"
)

// Norm lowercases s and turns punctuation into single spaces. Marks are kept
// as word characters: Devanagari vowel signs are marks, and dropping them
// would split every Nepali or Hindi word apart.
func Norm(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// Base drops a version suffix such as " - Remastered 2009", "(feat. X)" or
// "[Live]" and normalizes what's left.
func Base(s string) string {
	if i := strings.IndexAny(s, "(["); i > 0 {
		s = s[:i]
	}
	if i := strings.Index(s, " - "); i > 0 {
		s = s[:i]
	}
	return Norm(s)
}

func Tokens(s string) []string {
	return strings.Fields(Norm(s))
}
