package tui

import (
	"strings"
	"testing"
)

// A real lyric line from a Nepali song, which is what exposed the problem:
// 24 runes, 15 grapheme clusters, 19 columns by Unicode's count.
const devanagari = "सम्झनामा म सधैँ हाँसीरहू"

func TestIsComplexScript(t *testing.T) {
	for _, tc := range []struct {
		s    string
		want bool
	}{
		{devanagari, true},
		{"तैँले बुनिदिएको सपना", true},
		{"In the end, I hope it's you and me", false},
		{"Café", false}, // precomposed, no combining mark
		{"", false},
	} {
		if got := IsComplexScript(tc.s); got != tc.want {
			t.Errorf("IsComplexScript(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// Complex script must be measured at its worst case, or a terminal that
// draws one cell per rune overflows the box.
func TestWidthIsPessimisticForComplexScript(t *testing.T) {
	if got, runes := Width(devanagari), len([]rune(devanagari)); got != runes {
		t.Errorf("Width(%q) = %d, want the rune count %d", devanagari, got, runes)
	}
	const latin = "hello there"
	if got := Width(latin); got != len(latin) {
		t.Errorf("Width(%q) = %d, want %d", latin, got, len(latin))
	}
}

// Truncation must never cut a letter from the vowel sign attached to it.
func TestTruncateKeepsClustersWhole(t *testing.T) {
	for _, max := range []int{4, 8, 12, 20} {
		got := Truncate(devanagari, max, "…")
		if Width(got) > max {
			t.Errorf("Truncate(max=%d) = %q, width %d", max, got, Width(got))
		}
		trimmed := strings.TrimSuffix(got, "…")
		if trimmed == "" {
			continue
		}
		// A dangling combining mark means a cluster was split.
		first := []rune(trimmed)[0]
		if first >= 0x093E && first <= 0x094D {
			t.Errorf("Truncate(max=%d) starts with a combining mark: %q", max, got)
		}
		if !strings.HasPrefix(devanagari, trimmed) {
			t.Errorf("Truncate(max=%d) = %q, which is not a prefix of the line", max, got)
		}
	}
}

func TestTruncateLeavesShortTextAlone(t *testing.T) {
	if got := Truncate("short", 20, "…"); got != "short" {
		t.Errorf("got %q", got)
	}
	if got := Truncate("anything", 0, "…"); got != "" {
		t.Errorf("a zero width must give nothing, got %q", got)
	}
}

func TestWrap(t *testing.T) {
	lines := Wrap("one two three four five", 10)
	for _, l := range lines {
		if Width(l) > 10 {
			t.Errorf("line %q is %d wide", l, Width(l))
		}
	}
	if strings.Join(lines, " ") != "one two three four five" {
		t.Errorf("words were lost or reordered: %q", lines)
	}

	// Devanagari wraps on cluster boundaries and stays inside the width.
	for _, l := range Wrap(devanagari+" "+devanagari, 12) {
		if Width(l) > 12 {
			t.Errorf("complex line %q is %d wide", l, Width(l))
		}
	}
}
