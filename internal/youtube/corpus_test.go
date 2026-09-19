package youtube

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
)

// The corpus is the real first-query YouTube search (5 results) of every
// song of a real 201-song playlist, and golden.json every candidate's score
// and verdict as scored when the corpus was recorded. It guards backward
// compatibility: a scoring change may only turn a candidate rejected for not
// naming the artist into an accepted one (ranked below any candidate that
// names the artist fully); every other score, verdict and pick must stay.
//
// Refresh the golden file only for a deliberate scoring change:
//
//	GEET_UPDATE_GOLDEN=1 go test ./internal/youtube -run TestCorpusBackwardCompatible

type corpusSong struct {
	SpotifyID  string            `json:"spotify_id"`
	Title      string            `json:"title"`
	Artists    []string          `json:"artists"`
	DurationMS int64             `json:"duration_ms"`
	Explicit   bool              `json:"explicit"`
	Candidates []json.RawMessage `json:"candidates"`
}

type goldenRow struct {
	Song   string  `json:"song"`
	ID     string  `json:"id"`
	Score  float64 `json:"score"`
	Reject string  `json:"reject,omitempty"`
}

type goldenSong struct {
	Song string      `json:"song"`
	Pick string      `json:"pick,omitempty"` // "" when nothing matched
	Rows []goldenRow `json:"rows"`
}

type corpusCase struct {
	name  string
	track spotify.Track
	cands []Candidate
}

func corpusCases(t *testing.T) []corpusCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "corpus", "playlist4.json"))
	if err != nil {
		t.Fatal(err)
	}
	var songs []corpusSong
	if err := json.Unmarshal(raw, &songs); err != nil {
		t.Fatal(err)
	}
	var cases []corpusCase
	for _, s := range songs {
		var lines []string
		for _, c := range s.Candidates {
			var b bytes.Buffer
			if err := json.Compact(&b, c); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, b.String())
		}
		cands, err := parseCandidates(strings.NewReader(strings.Join(lines, "\n")))
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, corpusCase{
			name:  s.SpotifyID + " " + s.Title,
			track: spotify.Track{ID: s.SpotifyID, Title: s.Title, Artists: s.Artists, Duration: time.Duration(s.DurationMS) * time.Millisecond, Explicit: s.Explicit},
			cands: cands,
		})
	}
	// The hand-picked real searches of TestBestOnRealSearches and friends.
	for _, f := range []struct {
		fixture string
		track   spotify.Track
	}{
		{"ancestral.ndjson", spotify.Track{Title: "Ancestral", Artists: []string{"Steven Wilson"}, Duration: 810 * time.Second}},
		{"blinding_lights.ndjson", spotify.Track{Title: "Blinding Lights", Artists: []string{"The Weeknd"}, Duration: 200040 * time.Millisecond}},
		{"hataarindai.ndjson", spotify.Track{Title: "Hataarindai, Bataasindai (feat. Shyam Nepali)", Artists: []string{"Sajjan Raj Vaidya", "Shyam Nepali"}, Duration: 330 * time.Second}},
		{"niggas_in_paris.ndjson", spotify.Track{Title: "Ni**as In Paris", Artists: []string{"JAŸ-Z", "Kanye West"}, Duration: 219 * time.Second}},
		{"1train.ndjson", spotify.Track{Title: "1Train (feat. Kendrick Lamar, Joey Bada$$, Yelawolf, Danny Brown, Action Bronson & Big K.R.I.T.)",
			Artists: []string{"A$AP Rocky", "Kendrick Lamar", "Joey Bada$$", "Yelawolf", "Danny Brown", "Action Bronson", "Big K.R.I.T."}, Duration: 372173 * time.Millisecond}},
		{"tonight.ndjson", spotify.Track{Title: "Tonight (I'm Fuckin' You)", Artists: []string{"Enrique Iglesias", "Ludacris", "DJ Frank E"}, Duration: 232213 * time.Millisecond, Explicit: true}},
		{"kaalpanik.ndjson", spotify.Track{Title: "Kaalpanik / Maayajastai", Artists: []string{"Bartika Eam Rai"}, Duration: 273 * time.Second}},
		{"renegade.ndjson", spotify.Track{Title: "Renegade", Artists: []string{"Aaryan Shah"}, Duration: 222576 * time.Millisecond}},
	} {
		cases = append(cases, corpusCase{name: f.fixture, track: f.track, cands: loadFixture(t, f.fixture)})
	}
	return cases
}

func scoreCorpus(t *testing.T) []goldenSong {
	var out []goldenSong
	for _, c := range corpusCases(t) {
		best, all, err := Best(c.track, c.cands, maxDiff)
		g := goldenSong{Song: c.name}
		if err == nil {
			g.Pick = best.ID
		}
		for _, s := range all {
			g.Rows = append(g.Rows, goldenRow{Song: c.name, ID: s.ID, Score: math.Round(s.Score*1e6) / 1e6, Reject: s.Reject})
		}
		out = append(out, g)
	}
	return out
}

func TestCorpusBackwardCompatible(t *testing.T) {
	path := filepath.Join("testdata", "corpus", "golden.json")
	now := scoreCorpus(t)
	if os.Getenv("GEET_UPDATE_GOLDEN") == "1" {
		b, err := json.MarshalIndent(now, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Skip("golden file written")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var was []goldenSong
	if err := json.Unmarshal(raw, &was); err != nil {
		t.Fatal(err)
	}
	if len(was) != len(now) {
		t.Fatalf("%d songs scored, golden has %d", len(now), len(was))
	}
	var rescued []string
	for i := range was {
		w, n := was[i], now[i]
		if w.Song != n.Song || len(w.Rows) != len(n.Rows) {
			t.Fatalf("song %d: %s (%d candidates), golden %s (%d)", i, n.Song, len(n.Rows), w.Song, len(w.Rows))
		}
		for j := range w.Rows {
			a, b := w.Rows[j], n.Rows[j]
			if a == b {
				continue
			}
			if a.Reject == rejectNoArtist && b.Reject == "" {
				continue // the one change allowed
			}
			t.Errorf("%s: candidate %s changed: score %v %q -> %v %q", w.Song, a.ID, a.Score, a.Reject, b.Score, b.Reject)
		}
		switch {
		case w.Pick != "" && n.Pick != w.Pick:
			t.Errorf("%s: pick changed %s -> %s", w.Song, w.Pick, n.Pick)
		case w.Pick == "" && n.Pick != "":
			rescued = append(rescued, fmt.Sprintf("%s -> %s", w.Song, n.Pick))
		}
	}
	for _, r := range rescued {
		t.Logf("newly matched: %s", r)
	}
}
