package youtube

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/textnorm"
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
		{
			// The official channel, ASAPROCKYUPTOWN, names the artist only as
			// one run-together word with "$" spelled "S", and Spotify writes
			// "1Train" where fan uploads write "1 Train".
			name:    "run-together channel name and title",
			fixture: "1train.ndjson",
			track: spotify.Track{
				Title:    "1Train (feat. Kendrick Lamar, Joey Bada$$, Yelawolf, Danny Brown, Action Bronson & Big K.R.I.T.)",
				Artists:  []string{"A$AP Rocky", "Kendrick Lamar", "Joey Bada$$", "Yelawolf", "Danny Brown", "Action Bronson", "Big K.R.I.T."},
				Duration: 372173 * time.Millisecond,
			},
			want: "TvEhl8IBWVo",
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
						fmt.Fprintf(&b, "%.1f", s.Score)
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
		{
			name:      "clean upload of an explicit song",
			track:     func(t *spotify.Track) { t.Explicit = true },
			cand:      func(c *Candidate) { c.Title = "Band - Song (Clean)" },
			wantLower: true,
		},
		{
			name:      "radio edit of an explicit song",
			track:     func(t *spotify.Track) { t.Explicit = true },
			cand:      func(c *Candidate) { c.Title = "Band - Song (Radio Edit)" },
			wantLower: true,
		},
		{
			name:  "explicit upload of an explicit song",
			track: func(t *spotify.Track) { t.Explicit = true },
			cand:  func(c *Candidate) { c.Title = "Band - Song (Explicit)" },
		},
		{
			name:      "explicit upload when the clean edit was picked",
			track:     func(t *spotify.Track) { t.Clean = true },
			cand:      func(c *Candidate) { c.Title = "Band - Song (Explicit)" },
			wantLower: true,
		},
		{
			name: "clean upload of a song that isn't explicit",
			cand: func(c *Candidate) { c.Title = "Band - Song (Clean)" },
		},
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

func TestTokenCoverageJoinsWords(t *testing.T) {
	tests := []struct {
		want, have string
		cover      float64
	}{
		{"1Train", "A$AP Rocky - 1 Train ft Kendrick Lamar", 1},
		{"1 Train", "A$AP Rocky - 1Train", 1},
		{"Superstar", "Super Star (Official Audio)", 1},
		{"Super Star", "Superstar", 1},
		{"Up", "Upside Down", 0},             // a word never matches part of a word
		{"Blinding Lights", "Blinding", 0.5}, // joins add matches, they don't loosen them
	}
	for _, tt := range tests {
		got := tokenCoverage(textnorm.Words(tt.want), textnorm.Words(tt.have))
		if got != tt.cover {
			t.Errorf("tokenCoverage(%q in %q) = %v, want %v", tt.want, tt.have, got, tt.cover)
		}
	}
}

func TestArtistMatchChannelHandle(t *testing.T) {
	tests := []struct {
		artists []string
		title   string
		channel string
		want    int
	}{
		{[]string{"A$AP Rocky"}, "1Train", "ASAPROCKYUPTOWN", 1},
		{[]string{"A$AP Rocky"}, "1Train", "ASAP Rocky", 1},
		{[]string{"A$AP Rocky"}, "ASAP Rocky - 1Train", "Some Fan", 1},
		{[]string{"The Weeknd", "Daft Punk"}, "Starboy", "DaftPunkVEVO", 2},
		{[]string{"SZA"}, "Kill Bill", "szafanpage", 0}, // too short to trust inside a handle
		{[]string{"A$AP Rocky"}, "1Train", "Rocky Uploads", 0},
		{[]string{"A$AP Rocky"}, "1Train", "TheASAPROCKYchannel", 0}, // only at the start
	}
	for _, tt := range tests {
		tokens := append(textnorm.Tokens(tt.title), textnorm.Tokens(tt.channel)...)
		if got := artistMatch(tt.artists, tokens, tt.channel); got != tt.want {
			t.Errorf("artistMatch(%q, %q | %q) = %d, want %d", tt.artists, tt.title, tt.channel, got, tt.want)
		}
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

// kaalpanik is Bartika Eam Rai's "Kaalpanik / Maayajastai": regular search
// finds only the official video (21s of intro too long), a live session and
// uploads spelling the title "MaayaaJastai". YouTube Music lists the studio
// audio on "Bartika Eam Rai - Topic" at Spotify's exact 273s.
var kaalpanik = spotify.Track{Title: "Kaalpanik / Maayajastai", Artists: []string{"Bartika Eam Rai"}, Duration: 273 * time.Second}

func fakeYtDlp(t *testing.T, fixtures ...string) (bin, argsFile string) {
	t.Helper()
	bin, _ = filepath.Abs("testdata/fake-yt-dlp")
	var outs []string
	for _, f := range fixtures {
		p, _ := filepath.Abs(filepath.Join("testdata", f))
		outs = append(outs, p)
	}
	argsFile = filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_YTDLP_ARGS", argsFile)
	t.Setenv("FAKE_YTDLP_OUTPUT", "")
	t.Setenv("FAKE_YTDLP_OUTPUTS", strings.Join(outs, " "))
	return bin, argsFile
}

func newResolver(bin string, music bool) *Resolver {
	return New(Options{
		YtDlp:           ytdlp.Runner{Binary: bin},
		SearchQuery:     "{artists} - {title}",
		FallbackQuery:   "{artists} - {title} audio",
		SearchResults:   5,
		MaxDurationDiff: maxDiff,
		MusicFallback:   music,
	})
}

func TestResolveFallsBackToYouTubeMusic(t *testing.T) {
	bin, argsFile := fakeYtDlp(t, "kaalpanik.ndjson", "kaalpanik_audio.ndjson", "kaalpanik_music.ndjson", "kaalpanik_music_details.ndjson")
	best, all, err := newResolver(bin, true).Resolve(context.Background(), kaalpanik)
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "6gvt_1t-nGY" || best.Channel != "Bartika Eam Rai - Topic" {
		t.Errorf("picked %s %q on %q, want the Topic channel's 6gvt_1t-nGY", best.ID, best.Title, best.Channel)
	}
	if best.URL != "https://www.youtube.com/watch?v=6gvt_1t-nGY" {
		t.Errorf("URL %q, want the watch page", best.URL)
	}
	if len(all) != 11 {
		t.Errorf("%d candidates reported, want 5 + 5 from the searches and 1 from YouTube Music", len(all))
	}
	raw, _ := os.ReadFile(argsFile)
	calls := strings.Split(strings.TrimSuffix(string(raw), "--\n"), "--\n")
	if len(calls) != 4 {
		t.Fatalf("%d yt-dlp runs, want 4:\n%s", len(calls), raw)
	}
	if !strings.Contains(calls[2], "https://music.youtube.com/search?q=Bartika+Eam+Rai+Kaalpanik+%2F+Maayajastai#songs") {
		t.Errorf("YouTube Music not searched for the songs section:\n%s", calls[2])
	}
	// Only the listed title that fits is opened, not the artist's other songs.
	if !strings.Contains(calls[3], "--skip-download") || strings.Count(calls[3], "watch?v=") != 1 || !strings.Contains(calls[3], "6gvt_1t-nGY") {
		t.Errorf("details run should open just 6gvt_1t-nGY:\n%s", calls[3])
	}
}

func TestResolveWithoutYouTubeMusic(t *testing.T) {
	bin, argsFile := fakeYtDlp(t, "kaalpanik.ndjson", "kaalpanik_audio.ndjson")
	if _, _, err := newResolver(bin, false).Resolve(context.Background(), kaalpanik); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	if raw, _ := os.ReadFile(argsFile); strings.Contains(string(raw), "music.youtube.com") {
		t.Errorf("searched YouTube Music with the fallback off:\n%s", raw)
	}
}

// When no listed title fits, nothing is opened: that would cost ~2s a
// result for candidates bound to be rejected.
func TestYouTubeMusicOpensOnlyFittingTitles(t *testing.T) {
	bin, argsFile := fakeYtDlp(t, "kaalpanik.ndjson", "kaalpanik_audio.ndjson", "kaalpanik_music.ndjson")
	other := spotify.Track{Title: "Some Other Song", Artists: []string{"Bartika Eam Rai"}, Duration: 273 * time.Second}
	if _, _, err := newResolver(bin, true).Resolve(context.Background(), other); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
	raw, _ := os.ReadFile(argsFile)
	if strings.Contains(string(raw), "--skip-download") {
		t.Errorf("opened results although no title fit:\n%s", raw)
	}
}

func TestParseCandidatesIgnoresStreamURL(t *testing.T) {
	in := `{"id":"abc","title":"Song","channel":"Band","duration":200,"url":"https://rr1---sn.googlevideo.com/videoplayback?expire=1"}
{"id":"def","title":"Song","channel":"Band","duration":200,"url":"https://music.youtube.com/watch?v=def"}
{"id":"ghi","title":"Song","channel":"Band","duration":200,"webpage_url":"https://www.youtube.com/watch?v=ghi","url":"https://rr1---sn.googlevideo.com/x"}
`
	cands, err := parseCandidates(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://www.youtube.com/watch?v=abc", "https://music.youtube.com/watch?v=def", "https://www.youtube.com/watch?v=ghi"}
	for i, c := range cands {
		if c.URL != want[i] {
			t.Errorf("%s: URL %q, want %q", c.ID, c.URL, want[i])
		}
	}
}

// The explicit "Tonight (I'm Fuckin' You)" is age-restricted on YouTube.
// Of the real search, the only thing worth trying next is a fan channel's
// re-upload of the same audio: full title, 233s against Spotify's 232.2s.
// The lyric video (12s long), the clean "Lovin'" video and a 213s edit
// stay out.
func TestAlternativesForAgeRestricted(t *testing.T) {
	track := spotify.Track{
		Title:    "Tonight (I'm Fuckin' You)",
		Artists:  []string{"Enrique Iglesias", "Ludacris", "DJ Frank E"},
		Duration: 232213 * time.Millisecond,
		Explicit: true,
	}
	best, all, err := Best(track, loadFixture(t, "tonight.ndjson"), maxDiff)
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "-yFj3FvoOWY" {
		t.Fatalf("best %s, want the official -yFj3FvoOWY", best.ID)
	}
	var ids []string
	for _, a := range Alternatives(track, all, best) {
		ids = append(ids, a.ID)
	}
	if want := []string{"xA0V8jCVMmE"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("alternatives %v, want %v", ids, want)
	}
}

func TestAlternativesOrderAndLimits(t *testing.T) {
	track := spotify.Track{Title: "Song", Artists: []string{"Band"}, Duration: 200 * time.Second, Explicit: true}
	c := func(id, title, channel string, secs int) Candidate {
		return Candidate{ID: id, Title: title, Channel: channel, Duration: time.Duration(secs) * time.Second}
	}
	cands := []Candidate{
		c("best", "Band - Song", "Band", 200),
		c("low", "Band - Song (Lyrics)", "Fan", 205),
		c("high", "Band - Song (Audio)", "Band - Topic", 200),
		c("remix", "Band - Song (Official Remix) [Official Audio]", "Band", 206), // accepted by matching, but another recording
		c("reup", "Song", "Somebody", 201),
		c("reup-far", "Song", "Somebody", 204),           // not within 2s
		c("reup-clean", "Song (Clean)", "Somebody", 200), // the other edit
		c("best", "Band - Song", "Band", 200),            // the same video from the second search
	}
	var all []Scored
	for i, x := range cands {
		all = append(all, score(track, x, i, len(cands), maxDiff))
	}
	var ids []string
	for _, a := range Alternatives(track, all, all[0]) {
		ids = append(ids, a.ID)
	}
	if want := []string{"high", "low", "reup"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("alternatives %v, want %v", ids, want)
	}
}

// A clean edit found as a fallback, "Tonight (I'm Lovin' You)", fits the
// base title "Tonight" as well as the age-restricted explicit song does.
// YouTube Music lists both (tonight_clean_music.ndjson is the real listing),
// and only the clean ones may be opened; an upload with the explicit words
// scores lower; and the clean upload can't stand in for the explicit song.
var tonightClean = spotify.Track{
	Title:    "Tonight (I'm Lovin' You) [feat. Ludacris & DJ Frank E]",
	Artists:  []string{"Enrique Iglesias"},
	Duration: 231 * time.Second,
	Clean:    true,
}

func TestCleanEditOpensOnlyCleanMusicResults(t *testing.T) {
	// The two regular searches return another song's results (any that don't
	// match will do), so the YouTube Music fallback runs.
	bin, argsFile := fakeYtDlp(t, "kaalpanik.ndjson", "kaalpanik_audio.ndjson", "tonight_clean_music.ndjson", "tonight_clean_details.ndjson")
	best, _, err := newResolver(bin, true).Resolve(context.Background(), tonightClean)
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "K_l00nL1o8s" {
		t.Errorf("picked %s, want the clean K_l00nL1o8s", best.ID)
	}
	raw, _ := os.ReadFile(argsFile)
	calls := strings.Split(strings.TrimSuffix(string(raw), "--\n"), "--\n")
	details := calls[len(calls)-1]
	for _, id := range []string{"-yFj3FvoOWY", "CSK_GVD_I3M"} {
		if strings.Contains(details, id) {
			t.Errorf("opened the explicit %s for a clean edit:\n%s", id, details)
		}
	}
	for _, id := range []string{"K_l00nL1o8s", "IgkslT5Tv3o"} {
		if !strings.Contains(details, id) {
			t.Errorf("didn't open the clean %s:\n%s", id, details)
		}
	}
}

func TestEditWordsInsideBrackets(t *testing.T) {
	explicit := spotify.Track{Title: "Tonight (I'm Fuckin' You)", Artists: []string{"Enrique Iglesias"}, Duration: 232 * time.Second, Explicit: true}
	mk := func(id, title string) Candidate {
		return Candidate{ID: id, Title: title, Channel: "Enrique Iglesias", Duration: 232 * time.Second, Verified: true}
	}
	explicitUp := mk("e", "Enrique Iglesias - Tonight (I'm Fuckin' You)")
	cleanUp := mk("c", "Enrique Iglesias - Tonight (I'm Lovin' You)")

	// Picking the clean edit, the explicit upload scores lower.
	if s, e := score(tonightClean, explicitUp, 0, 2, maxDiff), score(tonightClean, cleanUp, 0, 2, maxDiff); s.Score >= e.Score {
		t.Errorf("clean edit: explicit upload %.1f, clean upload %.1f; want the explicit one lower", s.Score, e.Score)
	}
	// The clean upload passes matching for the explicit song (same base
	// title), but must never stand in for it.
	all := []Scored{score(explicit, explicitUp, 0, 2, maxDiff), score(explicit, cleanUp, 1, 2, maxDiff)}
	if all[1].Reject != "" {
		t.Fatalf("setup: clean upload rejected (%s); the case needs it accepted", all[1].Reject)
	}
	if alts := Alternatives(explicit, all, all[0]); len(alts) != 0 {
		t.Errorf("alternatives %v, want none: the clean upload isn't the explicit recording", alts[0].Title)
	}
}

// The first search for "Tonight (I'm Fuckin' You)" came back without any
// stand-in for the age-restricted official upload, so the explicit version
// needs a wider search before any clean edit. In the real 10-result searches
// (both phrasings), the exact re-upload xA0V8jCVMmE is there.
func TestMoreAlternatives(t *testing.T) {
	bin, argsFile := fakeYtDlp(t, "tonight_wide.ndjson", "tonight_wide_artist.ndjson")
	track := spotify.Track{
		Title:    "Tonight (I'm Fuckin' You)",
		Artists:  []string{"Enrique Iglesias", "Ludacris", "DJ Frank E"},
		Duration: 232213 * time.Millisecond,
		Explicit: true,
	}
	more, err := newResolver(bin, true).MoreAlternatives(context.Background(), track, map[string]bool{"-yFj3FvoOWY": true})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range more {
		ids = append(ids, a.ID)
		if a.ID == "-yFj3FvoOWY" {
			t.Error("offered the upload that already failed")
		}
		if !fullTitle(track, a.Title) {
			t.Errorf("offered %q, which isn't the explicit recording", a.Title)
		}
	}
	if !slices.Contains(ids, "xA0V8jCVMmE") {
		t.Errorf("alternatives %v lack the exact re-upload xA0V8jCVMmE", ids)
	}
	raw, _ := os.ReadFile(argsFile)
	for _, want := range []string{"ytsearch10:Enrique Iglesias, Ludacris, DJ Frank E - Tonight (I'm Fuckin' You)", "ytsearch10:Enrique Iglesias Tonight (I'm Fuckin' You)"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("didn't search %q:\n%s", want, raw)
		}
	}
}

func TestCoreName(t *testing.T) {
	tests := []struct {
		artist string
		want   []string
	}{
		{"Kush Band Nepal", []string{"kush"}},
		{"The Goo Goo Dolls", []string{"goo", "goo", "dolls"}},
		{"Band of Horses", []string{"of", "horses"}},
		{"Enrique Iglesias", nil}, // nothing generic to leave out: the full name is the only match
		{"The Band", nil},         // nothing distinctive left
		{"DJ Nepal", nil},         // too short to identify an artist
	}
	for _, tt := range tests {
		if got := coreName(tt.artist); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("coreName(%q) = %q, want %q", tt.artist, got, tt.want)
		}
	}
}

// "Kush Band Nepal" is "KUSH" on YouTube. The distinctive part matches only
// when the full name doesn't, and ranks below it, so an upload naming the
// artist in full always wins.
func TestArtistCoreMatch(t *testing.T) {
	track := spotify.Track{Title: "Harayeko Graha", Artists: []string{"Kush Band Nepal"}, Duration: 214 * time.Second}
	bare := Candidate{ID: "bare", Title: "KUSH - Harayeko Graha (Official Video)", Channel: "Shaurav Bhattarai", Duration: 214 * time.Second}
	full := Candidate{ID: "full", Title: "Kush Band Nepal - Harayeko Graha", Channel: "Somebody", Duration: 214 * time.Second}
	if got := artistMatch(track.Artists, textnorm.Tokens(bare.Title+" "+bare.Channel), bare.Channel); got != artistCore {
		t.Fatalf("bare name: artistMatch = %d, want artistCore", got)
	}
	b, f := score(track, bare, 0, 2, maxDiff), score(track, full, 1, 2, maxDiff)
	if b.Reject != "" || f.Reject != "" {
		t.Fatalf("rejected: bare %q, full %q", b.Reject, f.Reject)
	}
	if b.Score >= f.Score {
		t.Errorf("bare name %.1f ranks with or above full name %.1f", b.Score, f.Score)
	}
	// An upload naming neither is still rejected.
	other := Candidate{ID: "x", Title: "Harayeko Graha", Channel: "Random Uploader", Duration: 214 * time.Second}
	if s := score(track, other, 0, 1, maxDiff); s.Reject != rejectNoArtist {
		t.Errorf("upload naming no artist: reject %q, want %q", s.Reject, rejectNoArtist)
	}
}

// The last resort, by title alone, on the real search for "Harayeko Graha"
// (as if YouTube knew the band by some other name): only an exact
// re-upload is accepted, and a TV-show cover 1 s off, karaoke and a guitar
// playthrough are not.
func TestTitleOnlyFallback(t *testing.T) {
	track := spotify.Track{Title: "Harayeko Graha", Artists: []string{"A Name YouTube Doesnt Use"}, Duration: 214 * time.Second}
	bin, argsFile := fakeYtDlp(t, "kaalpanik.ndjson", "kaalpanik_audio.ndjson", "harayeko_title.ndjson")
	r := newResolver(bin, false)
	r.opts.TitleFallback = true
	best, all, err := r.Resolve(context.Background(), track)
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "lvoVXF7mtjo" || !best.TitleOnly {
		t.Errorf("picked %s (title only %v), want the official lvoVXF7mtjo marked TitleOnly", best.ID, best.TitleOnly)
	}
	for _, s := range all {
		if s.Reject == "" && (s.ID == "BXB8qxWzY9A" || s.ID == "AfpccFzZh9Q" || s.ID == "HC1xBOP-FcM") {
			t.Errorf("accepted %s %q", s.ID, s.Title)
		}
	}
	raw, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(raw), "ytsearch10:Harayeko Graha") {
		t.Errorf("didn't search the title alone:\n%s", raw)
	}
}

func TestTitleOnlyFallbackLimits(t *testing.T) {
	// A one-word title names too many songs to be searched alone.
	one := spotify.Track{Title: "Graha", Artists: []string{"Nobody Known"}, Duration: 214 * time.Second}
	bin, argsFile := fakeYtDlp(t, "kaalpanik.ndjson", "kaalpanik_audio.ndjson", "harayeko_title.ndjson")
	r := newResolver(bin, false)
	r.opts.TitleFallback = true
	if _, _, err := r.Resolve(context.Background(), one); !errors.Is(err, ErrNoMatch) {
		t.Errorf("one-word title: err %v, want ErrNoMatch", err)
	}
	if raw, _ := os.ReadFile(argsFile); strings.Count(string(raw), "--\n") != 2 {
		t.Errorf("searched a one-word title alone:\n%s", raw)
	}

	// Turned off, it never searches.
	track := spotify.Track{Title: "Harayeko Graha", Artists: []string{"Nobody Known"}, Duration: 214 * time.Second}
	bin, argsFile = fakeYtDlp(t, "kaalpanik.ndjson", "kaalpanik_audio.ndjson", "harayeko_title.ndjson")
	if _, _, err := newResolver(bin, false).Resolve(context.Background(), track); !errors.Is(err, ErrNoMatch) {
		t.Errorf("off: err %v, want ErrNoMatch", err)
	}
	if raw, _ := os.ReadFile(argsFile); strings.Contains(string(raw), "ytsearch10:") {
		t.Errorf("searched by title with the fallback off:\n%s", raw)
	}
}
