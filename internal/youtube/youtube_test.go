package youtube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/ytdlp"
)

const maxDiff = 10 * time.Second

func loadFixture(t *testing.T, name string) []Candidate {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cands, err := parseCandidates(f)
	if err != nil {
		t.Fatal(err)
	}
	return cands
}

// The fixtures are real `yt-dlp ytsearch5:... --flat-playlist --dump-json`
// output, so these cases pin the scorer against what YouTube actually ranks.
func TestBestOnRealSearches(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		track   spotify.Track
		want    string
	}{
		{
			// #1 is the official channel's live cut, 3s off: "concert" must sink it.
			name:    "studio track over official live version",
			fixture: "ancestral.ndjson",
			track:   spotify.Track{Title: "Ancestral", Artists: []string{"Steven Wilson"}, Duration: 810 * time.Second},
			want:    "fD_5z8E_cIc",
		},
		{
			// The official video is 63s too long; the lyrics upload and the
			// VMA live cut are the right length but lose to official audio.
			name:    "official audio over video, lyrics and live",
			fixture: "blinding_lights.ndjson",
			track:   spotify.Track{Title: "Blinding Lights", Artists: []string{"The Weeknd"}, Duration: 200040 * time.Millisecond},
			want:    "fHI8X4OXluQ",
		},
		{
			// The official upload is 12s longer than Spotify's, so only the
			// fan lyrics upload is within range; its title lacks the feat.
			name:    "nepali track ignores feat suffix",
			fixture: "hataarindai.ndjson",
			track:   spotify.Track{Title: "Hataarindai, Bataasindai (feat. Shyam Nepali)", Artists: []string{"Sajjan Raj Vaidya", "Shyam Nepali"}, Duration: 330 * time.Second},
			want:    "alb0nHpuPfE",
		},
		{
			// Spotify censors the title and spells the artist "JAŸ-Z"; the
			// official upload is 33s too long and #5 is a remix, leaving the
			// spelled-out fan upload of the right length.
			name:    "censored title and accented artist",
			fixture: "niggas_in_paris.ndjson",
			track:   spotify.Track{Title: "Ni**as In Paris", Artists: []string{"JAŸ-Z", "Kanye West"}, Duration: 219 * time.Second},
			want:    "fbFnF-86eYs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			best, all, err := Best(tt.track, loadFixture(t, tt.fixture), maxDiff)
			if err != nil {
				t.Fatal(err)
			}
			if best.ID != tt.want {
				var b strings.Builder
				for _, s := range all {
					b.WriteString("\n  " + s.ID + " " + s.Title + " | " + s.Channel + " | ")
					if s.Reject != "" {
						b.WriteString("rejected: " + s.Reject)
					} else {
						b.WriteString(fmt.Sprintf("%.1f", s.Score))
					}
				}
				t.Errorf("picked %s, want %s; candidates:%s", best.ID, tt.want, b.String())
			}
		})
	}
}

func TestBestNoMatch(t *testing.T) {
	track := spotify.Track{Title: "Some Other Song", Artists: []string{"Nobody"}, Duration: 3 * time.Minute}
	_, all, err := Best(track, loadFixture(t, "blinding_lights.ndjson"), maxDiff)
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	for _, s := range all {
		if s.Reject == "" {
			t.Errorf("%s not rejected", s.ID)
		}
	}
}

func TestScoreRules(t *testing.T) {
	track := spotify.Track{Title: "Song", Artists: []string{"Band"}, Duration: 200 * time.Second}
	base := Candidate{ID: "x", Title: "Band - Song", Channel: "Band", Duration: 200 * time.Second, Verified: true}
	baseScore := score(track, base, 0, 1, maxDiff).Score

	tests := []struct {
		name       string
		track      func(*spotify.Track)
		cand       func(*Candidate)
		wantReject bool
		wantLower  bool // scores below the plain official upload
	}{
		{name: "live stream", cand: func(c *Candidate) { c.Live = true }, wantReject: true},
		{name: "no duration", cand: func(c *Candidate) { c.Duration = 0 }, wantReject: true},
		{name: "11s too long", cand: func(c *Candidate) { c.Duration = 211 * time.Second }, wantReject: true},
		{name: "different song", cand: func(c *Candidate) { c.Title = "Band - Other" }, wantReject: true},
		{name: "artist missing", cand: func(c *Candidate) { c.Title = "Song"; c.Channel = "Uploader" }, wantReject: true},
		{name: "cover", cand: func(c *Candidate) { c.Title = "Band - Song (Cover)" }, wantLower: true},
		{name: "sped up", cand: func(c *Candidate) { c.Title = "Band - Song (Sped Up)" }, wantLower: true},
		{name: "fan channel", cand: func(c *Candidate) { c.Channel = "Fan"; c.Verified = false }, wantLower: true},
		{
			name:  "live is fine when spotify title is live",
			track: func(t *spotify.Track) { t.Title = "Song (Live)" },
			cand:  func(c *Candidate) { c.Title = "Band - Song (Live)" },
		},
		{
			name:  "clean edit's cut title matches the full word",
			track: func(t *spotify.Track) { t.Title = "ong"; t.Clean = true },
			cand:  func(*Candidate) {},
		},
		{
			name:       "cut title without the clean flag is a different song",
			track:      func(t *spotify.Track) { t.Title = "ong" },
			cand:       func(*Candidate) {},
			wantReject: true,
		},
		{name: "topic channel", cand: func(c *Candidate) { c.Title = "Song"; c.Channel = "Band - Topic" }},
		{name: "vevo channel", cand: func(c *Candidate) { c.Channel = "BandVEVO" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, c := track, base
			if tt.track != nil {
				tt.track(&tr)
			}
			tt.cand(&c)
			s := score(tr, c, 0, 1, maxDiff)
			if got := s.Reject != ""; got != tt.wantReject {
				t.Fatalf("rejected = %v (%q), want %v", got, s.Reject, tt.wantReject)
			}
			if !tt.wantReject && (s.Score < baseScore) != tt.wantLower {
				t.Errorf("score %.1f vs plain %.1f, want lower = %v", s.Score, baseScore, tt.wantLower)
			}
		})
	}
}

func TestSearchRunsYtDlp(t *testing.T) {
	bin, err := filepath.Abs("testdata/fake-yt-dlp")
	if err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args")
	fixture, _ := filepath.Abs("testdata/ancestral.ndjson")
	t.Setenv("FAKE_YTDLP_ARGS", argsFile)
	t.Setenv("FAKE_YTDLP_OUTPUT", fixture)

	r := New(Options{
		YtDlp:           ytdlp.Runner{Binary: bin, CookiesFromBrowser: "firefox", ExtraArgs: []string{"--proxy", "socks5://127.0.0.1:9050"}},
		SearchQuery:     "{artists} - {title} {album}",
		SearchResults:   5,
		MaxDurationDiff: maxDiff,
	})
	track := spotify.Track{Title: "Ancestral", Artists: []string{"Steven Wilson"}, Album: "Hand Cannot Erase", Duration: 810 * time.Second}
	best, all, err := r.Resolve(context.Background(), track)
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "fD_5z8E_cIc" || len(all) != 5 {
		t.Errorf("best = %s of %d candidates", best.ID, len(all))
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	gotArgs := strings.Split(strings.TrimSuffix(strings.TrimSpace(string(raw)), "\n--"), "\n")
	wantArgs := []string{
		"--cookies-from-browser", "firefox",
		"--proxy", "socks5://127.0.0.1:9050",
		"--flat-playlist", "--dump-json", "--no-warnings", "--no-progress",
		"ytsearch5:Steven Wilson - Ancestral Hand Cannot Erase",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Errorf("args\n got  %q\n want %q", gotArgs, wantArgs)
	}
}

func TestSearchErrors(t *testing.T) {
	t.Run("missing binary", func(t *testing.T) {
		r := New(Options{YtDlp: ytdlp.Runner{Binary: "definitely-not-yt-dlp"}, SearchQuery: "{title}", SearchResults: 1})
		_, err := r.Search(context.Background(), "x")
		if !errors.Is(err, ytdlp.ErrToolMissing) {
			t.Fatalf("err = %v, want ErrToolMissing", err)
		}
	})
	t.Run("yt-dlp failure keeps its message", func(t *testing.T) {
		bin, _ := filepath.Abs("testdata/fake-yt-dlp")
		t.Setenv("FAKE_YTDLP_ARGS", filepath.Join(t.TempDir(), "args"))
		t.Setenv("FAKE_YTDLP_FAIL", "1")
		r := New(Options{YtDlp: ytdlp.Runner{Binary: bin}, SearchQuery: "{title}", SearchResults: 1})
		_, err := r.Search(context.Background(), "x")
		if err == nil || !strings.Contains(err.Error(), "not a bot") {
			t.Fatalf("err = %v, want yt-dlp's stderr in it", err)
		}
	})
}

// The first page has only the official video (intro: 12s too long) and a
// trimmed lyrics upload (12s too short); the fallback query finds the
// artist's "(Audio)" upload within range.
func TestResolveFallsBackToAudioQuery(t *testing.T) {
	bin, _ := filepath.Abs("testdata/fake-yt-dlp")
	first, _ := filepath.Abs("testdata/renegade.ndjson")
	second, _ := filepath.Abs("testdata/renegade_audio.ndjson")
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_YTDLP_ARGS", argsFile)
	t.Setenv("FAKE_YTDLP_OUTPUTS", first+" "+second)

	r := New(Options{
		YtDlp:           ytdlp.Runner{Binary: bin},
		SearchQuery:     "{artists} - {title}",
		FallbackQuery:   "{artists} - {title} audio",
		SearchResults:   5,
		MaxDurationDiff: maxDiff,
	})
	track := spotify.Track{Title: "Renegade", Artists: []string{"Aaryan Shah"}, Duration: 222576 * time.Millisecond}
	best, all, err := r.Resolve(context.Background(), track)
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "vTDzejo_8Ng" {
		t.Errorf("picked %s %q, want the (Audio) upload vTDzejo_8Ng", best.ID, best.Title)
	}
	if len(all) != 10 {
		t.Errorf("%d candidates reported, want both searches' 10", len(all))
	}
	raw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(raw), "ytsearch5:Aaryan Shah - Renegade audio") {
		t.Errorf("fallback query not searched:\n%s", raw)
	}

	r.opts.FallbackQuery = ""
	os.Remove(argsFile)
	if _, _, err := r.Resolve(context.Background(), track); !errors.Is(err, ErrNoMatch) {
		t.Errorf("without a fallback: err = %v, want ErrNoMatch", err)
	}
}
