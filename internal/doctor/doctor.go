// Package doctor checks everything geet depends on outside its own code
// (the external tools, the user's setup, and the web services it reads) and
// says what to do about anything broken, for `geet doctor`.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/deezer"
	"github.com/sumdahl/geet/internal/index"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/spotify"
	"github.com/sumdahl/geet/internal/youtube"
	"github.com/sumdahl/geet/internal/ytdlp"
)

type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn" // works, but needs attention
	Fail Status = "fail" // downloads will fail until fixed
	Skip Status = "skip" // optional and absent, or not checked
)

type Check struct {
	Group  string `json:"group"`
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	MS     int64  `json:"ms,omitempty"` // time taken, for network checks
}

// Env is what the checks look at.
type Env struct {
	Config     config.Config
	ConfigPath string
	ConfigErr  error // the config failed to load; Config holds defaults
	Offline    bool  // skip the network checks
	Now        time.Time
}

const (
	groupTools    = "Tools"
	groupSetup    = "Setup"
	groupServices = "Services"

	// yt-dlp is the part that breaks when YouTube changes; releases come
	// every few weeks, so an old one is the first suspect.
	ytdlpStale    = 60 * 24 * time.Hour
	ytdlpVeryOld  = 180 * 24 * time.Hour
	lowDiskSpace  = 1 << 30 // 1 GiB
	serviceBudget = 25 * time.Second

	// Known-good public items the service checks read: a Creative Commons
	// track (Kevin MacLeod, "Monkeys Spinning Monkeys") and a long-lived
	// catalog entry (The Weeknd, "Blinding Lights").
	probeSpotifyTrack = "3XtbtOMVVBooqbcGz8UErp"
	probeYouTubeQuery = "Kevin MacLeod - Monkeys Spinning Monkeys"
	probeITunesID     = "1499378607"
)

// Run performs every check. Service checks run in parallel; the order of the
// result is fixed (tools, setup, services).
func Run(ctx context.Context, env Env) []Check {
	cfg := env.Config
	checks := []Check{
		checkYtDlp(ctx, cfg.Tools.YtDlp, env.Now),
		checkFFmpeg(ctx, cfg.Tools.FFmpeg, cfg.Format),
		checkFFprobe(ctx, cfg.Tools.FFprobe),
		checkFzf(ctx),
		checkConfig(env),
		checkLibrary(cfg.Output),
		checkIndex(cfg.IndexPath),
	}
	if env.Offline {
		return append(checks, Check{Group: groupServices, Name: "network", Status: Skip, Detail: "not checked (--offline)"})
	}

	services := []func(context.Context, config.Config) Check{checkSpotify, checkYouTube, checkDeezer, checkITunes}
	results := make([]Check, len(services))
	var wg sync.WaitGroup
	for i, check := range services {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, serviceBudget)
			defer cancel()
			start := time.Now()
			c := check(ctx, cfg)
			c.Group = groupServices
			c.MS = time.Since(start).Milliseconds()
			results[i] = c
		})
	}
	wg.Wait()
	return append(checks, results...)
}

// Healthy reports whether nothing failed; warnings still count as healthy.
func Healthy(checks []Check) bool {
	for _, c := range checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// --- tools ---

func checkYtDlp(ctx context.Context, bin string, now time.Time) Check {
	c := Check{Group: groupTools, Name: "yt-dlp"}
	out, err := toolOutput(ctx, bin, "--version")
	if err != nil {
		c.Status, c.Detail = Fail, fmt.Sprintf("not found (%q)", bin)
		c.Fix = "install it: sudo pacman -S yt-dlp (or set tools.yt_dlp)"
		return c
	}
	version := strings.TrimSpace(out)
	c.Status, c.Detail = ytdlpVerdict(version, now)
	if c.Status == Warn {
		c.Fix = "YouTube breaks old versions; update: sudo pacman -Syu yt-dlp (or yt-dlp -U)"
	}
	return c
}

var ytdlpDate = regexp.MustCompile(`^(\d{4})\.(\d{2})\.(\d{2})`)

// ytdlpVerdict judges yt-dlp by its release date, which is its version
// number (2026.08.19).
func ytdlpVerdict(version string, now time.Time) (Status, string) {
	m := ytdlpDate.FindString(version)
	released, err := time.Parse("2006.01.02", m)
	if m == "" || err != nil {
		return OK, version // a nightly or custom build: nothing to judge
	}
	age := now.Sub(released)
	days := int(age.Hours() / 24)
	switch {
	case age > ytdlpVeryOld:
		return Warn, fmt.Sprintf("%s is %d days old: downloads are likely to fail", version, days)
	case age > ytdlpStale:
		return Warn, fmt.Sprintf("%s is %d days old", version, days)
	case days < 1:
		return OK, version + " (released today)"
	}
	return OK, fmt.Sprintf("%s (%d days old)", version, days)
}

// encoderFor is the ffmpeg encoder each output format needs.
var encoderFor = map[string]string{"opus": "libopus", "mp3": "libmp3lame", "flac": "flac"}

func checkFFmpeg(ctx context.Context, bin, format string) Check {
	c := Check{Group: groupTools, Name: "ffmpeg"}
	out, err := toolOutput(ctx, bin, "-hide_banner", "-version")
	if err != nil {
		c.Status, c.Detail = Fail, fmt.Sprintf("not found (%q)", bin)
		c.Fix = "install it: sudo pacman -S ffmpeg (or set tools.ffmpeg)"
		return c
	}
	version := ffmpegVersion(out)
	encoders, _ := toolOutput(ctx, bin, "-hide_banner", "-encoders")
	have := parseEncoders(encoders)

	var parts []string
	for _, f := range []string{"opus", "mp3", "flac"} {
		mark := "✓"
		if !have[encoderFor[f]] {
			mark = "✗"
		}
		parts = append(parts, f+" "+mark)
	}
	c.Status, c.Detail = OK, fmt.Sprintf("%s (%s)", version, strings.Join(parts, " "))
	if !have[encoderFor[format]] {
		c.Status = Fail
		c.Fix = fmt.Sprintf("this ffmpeg lacks %s, needed for format = %q: install a full ffmpeg build or pick another format", encoderFor[format], format)
	}
	return c
}

func ffmpegVersion(out string) string {
	f := strings.Fields(out)
	if len(f) >= 3 && (f[0] == "ffmpeg" || f[0] == "ffprobe") {
		return strings.TrimPrefix(f[2], "n")
	}
	return "unknown version"
}

// parseEncoders reads `ffmpeg -encoders`: " A....D libopus   libopus Opus".
func parseEncoders(out string) map[string]bool {
	have := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && len(f[0]) == 6 && f[0][0] == 'A' {
			have[f[1]] = true
		}
	}
	return have
}

func checkFFprobe(ctx context.Context, bin string) Check {
	c := Check{Group: groupTools, Name: "ffprobe"}
	out, err := toolOutput(ctx, bin, "-hide_banner", "-version")
	if err != nil {
		c.Status, c.Detail = Warn, fmt.Sprintf("not found (%q)", bin)
		c.Fix = "only needed to rebuild the download index; it ships with ffmpeg"
		return c
	}
	c.Status, c.Detail = OK, ffmpegVersion(out)
	return c
}

func checkFzf(ctx context.Context) Check {
	c := Check{Group: groupTools, Name: "fzf"}
	out, err := toolOutput(ctx, "fzf", "--version")
	if err != nil {
		c.Status, c.Detail = Skip, "not installed (optional: geet search falls back to a numbered list)"
		return c
	}
	c.Status, c.Detail = OK, strings.Fields(out + " ?")[0]+" (search menu)"
	return c
}

func toolOutput(ctx context.Context, bin string, args ...string) (string, error) {
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).Output()
	return string(out), err
}

// --- setup ---

func checkConfig(env Env) Check {
	c := Check{Group: groupSetup, Name: "config"}
	if env.ConfigErr != nil {
		c.Status, c.Detail = Fail, env.ConfigErr.Error()
		c.Fix = "fix or remove " + env.ConfigPath + " (geet config settings lists the valid keys)"
		return c
	}
	cfg := env.Config
	where := env.ConfigPath + " (defaults only: file not created)"
	if _, err := os.Stat(env.ConfigPath); err == nil {
		where = env.ConfigPath
	}
	source := "keyless Spotify pages + Deezer"
	if cfg.HasAPICredentials() {
		source = "Spotify Web API"
	}
	c.Status = OK
	c.Detail = fmt.Sprintf("%s · %s · jobs %d, resolve_jobs %d · metadata: %s", where, cfg.Format, cfg.Jobs, cfg.ResolveJobs, source)
	return c
}

func checkLibrary(output string) Check {
	c := Check{Group: groupSetup, Name: "library"}
	dir := existingParent(output)
	if dir == "" {
		c.Status, c.Detail = Fail, output+": no existing parent folder"
		c.Fix = "set output to a folder you can write to"
		return c
	}
	f, err := os.CreateTemp(dir, ".geet-doctor-*")
	if err != nil {
		c.Status, c.Detail = Fail, fmt.Sprintf("%s is not writable: %v", dir, err)
		c.Fix = "fix the folder's permissions, or set output elsewhere"
		return c
	}
	f.Close()
	os.Remove(f.Name())

	where := output
	if dir != output {
		where += " (will be created)"
	}
	free, ok := freeSpace(dir)
	switch {
	case !ok:
		c.Status, c.Detail = OK, where+" writable"
	case free < lowDiskSpace:
		c.Status, c.Detail = Warn, fmt.Sprintf("%s writable, only %s free", where, humanBytes(free))
		c.Fix = "a song takes 3-10 MB; free some space"
	default:
		c.Status, c.Detail = OK, fmt.Sprintf("%s writable, %s free", where, humanBytes(free))
	}
	return c
}

func existingParent(p string) string {
	for {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ""
		}
		p = parent
	}
}

func freeSpace(dir string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return st.Bavail * uint64(st.Bsize), true
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, suffix := float64(n), "KMGTP"
	i := -1
	for v >= unit && i < len(suffix)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %sB", v, string(suffix[i]))
}

func checkIndex(path string) Check {
	c := Check{Group: groupSetup, Name: "index"}
	idx, fresh, err := index.Open(path)
	if err != nil {
		c.Status, c.Detail = Fail, err.Error()
		c.Fix = "delete " + path + "; geet rebuilds it from your files' tags on the next download"
		return c
	}
	if fresh {
		c.Status, c.Detail = OK, "not created yet (built on the first download)"
		return c
	}
	total, missing := idx.Stats()
	c.Status = OK
	c.Detail = fmt.Sprintf("%d songs known", total)
	if missing > 0 {
		c.Detail += fmt.Sprintf(", %d point to deleted files (dropped automatically when looked up)", missing)
	}
	return c
}

// --- services ---

func checkSpotify(ctx context.Context, cfg config.Config) Check {
	c := Check{Name: "Spotify"}
	var t spotify.Track
	var err error
	if cfg.HasAPICredentials() {
		c.Name = "Spotify API"
		t, err = spotify.NewAPI(cfg.Spotify.ClientID, cfg.Spotify.ClientSecret).Track(ctx, probeSpotifyTrack)
	} else {
		t, err = spotify.NewWeb("").Track(ctx, probeSpotifyTrack)
	}
	switch {
	case err != nil:
		c.Status, c.Detail = Fail, err.Error()
		c.Fix = spotifyFix(err)
	case t.Title == "" || t.Album == "" || t.Duration <= 0 || len(t.Artists) == 0:
		c.Status, c.Detail = Fail, fmt.Sprintf("page read but incomplete: %+v", t)
		c.Fix = "Spotify changed its pages; update geet (or report it)"
	default:
		c.Status, c.Detail = OK, fmt.Sprintf("read %q by %s", t.Title, t.Artists[0])
	}
	return c
}

func spotifyFix(err error) string {
	switch {
	case errors.Is(err, spotify.ErrPageFormat):
		return "Spotify changed its public pages; update geet (or report it)"
	case errors.Is(err, spotify.ErrAuth):
		return "check spotify.client_id and spotify.client_secret, or remove them to use the keyless pages"
	}
	return networkFix(err)
}

func checkYouTube(ctx context.Context, cfg config.Config) Check {
	c := Check{Name: "YouTube"}
	yt := youtube.New(youtube.Options{
		YtDlp: ytdlp.Runner{
			Binary:             cfg.Tools.YtDlp,
			CookiesFile:        cfg.YouTube.CookiesFile,
			CookiesFromBrowser: cfg.YouTube.CookiesFromBrowser,
			ExtraArgs:          cfg.YouTube.ExtraArgs,
		},
		SearchResults: 1,
	})
	cands, err := yt.Search(ctx, probeYouTubeQuery)
	switch {
	case err != nil:
		c.Status, c.Detail = Fail, err.Error()
		c.Fix = youtubeFix(err)
	case len(cands) == 0:
		c.Status, c.Detail = Fail, "search returned nothing"
		c.Fix = "update yt-dlp: sudo pacman -Syu yt-dlp"
	default:
		c.Status, c.Detail = OK, "search works"
	}
	return c
}

func youtubeFix(err error) string {
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, ytdlp.ErrToolMissing):
		return "install yt-dlp: sudo pacman -S yt-dlp"
	case strings.Contains(msg, "not a bot") || strings.Contains(msg, "sign in"):
		return "YouTube is limiting this IP: wait a while, lower jobs, or set youtube.cookies_from_browser = \"firefox\""
	case strings.Contains(msg, "403") || strings.Contains(msg, "unable to extract") || strings.Contains(msg, "no video formats"):
		return "YouTube changed something yt-dlp doesn't handle yet: update it (sudo pacman -Syu yt-dlp)"
	}
	return networkFix(err)
}

func checkDeezer(ctx context.Context, _ config.Config) Check {
	c := Check{Name: "Deezer"}
	country, err := deezer.New("").Ping(ctx)
	if err != nil {
		c.Status, c.Detail = Warn, err.Error()
		c.Fix = "only ISRC and disc numbers come from Deezer; downloads still work without it"
		return c
	}
	c.Status, c.Detail = OK, fmt.Sprintf("reachable (searches as %s: some label catalogs are regional)", country)
	return c
}

func checkITunes(ctx context.Context, cfg config.Config) Check {
	c := Check{Name: "iTunes"}
	t, err := itunes.New("", cfg.Search.Country).Lookup(ctx, probeITunesID)
	if err != nil {
		c.Status, c.Detail = Warn, err.Error()
		c.Fix = "only geet search uses it; " + networkFix(err)
		return c
	}
	c.Status, c.Detail = OK, fmt.Sprintf("reachable (%s store, found %q)", strings.ToUpper(cfg.Search.Country), t.Title)
	return c
}

func networkFix(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out: check your internet connection"
	}
	return "check your internet connection (or a proxy or firewall)"
}
