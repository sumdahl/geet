// Package config holds every engine setting. Each one can come from the TOML
// file, a SPOTIFY_DL_* environment variable or a command-line flag (in
// increasing precedence); see Settings for the single list all three derive
// from.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/sumdahl/spotify-dl/internal/index"
	"github.com/sumdahl/spotify-dl/internal/library"
)

var ErrInvalid = errors.New("invalid config")

var (
	formats         = []string{"opus", "flac", "mp3"}
	duplicateModes  = []string{"link", "copy", "skip", "download"}
	progressAnswers = []string{"auto", "always", "never"}
	bitrate         = regexp.MustCompile(`^[1-9][0-9]*k$`)
)

type Config struct {
	Output             string  `toml:"output" json:"output"`
	OutputTemplate     string  `toml:"output_template" json:"output_template"`
	PlaylistFolder     bool    `toml:"playlist_folder" json:"playlist_folder"`
	PlaylistFolderCase string  `toml:"playlist_folder_case" json:"playlist_folder_case"`
	Format             string  `toml:"format" json:"format"`
	Bitrate            string  `toml:"bitrate" json:"bitrate"`
	Overwrite          bool    `toml:"overwrite" json:"overwrite"`
	Duplicates         string  `toml:"duplicates" json:"duplicates"`
	IndexPath          string  `toml:"index_path" json:"index_path"`
	Progress           string  `toml:"progress" json:"progress"`
	DownloadRetries    int     `toml:"download_retries" json:"download_retries"`
	Jobs               int     `toml:"jobs" json:"jobs"`
	ResolveJobs        int     `toml:"resolve_jobs" json:"resolve_jobs"`
	Spotify            Spotify `toml:"spotify" json:"spotify"`
	YouTube            YouTube `toml:"youtube" json:"youtube"`
	Tools              Tools   `toml:"tools" json:"tools"`
}

// Spotify credentials are optional: with none, metadata comes from Spotify's
// public web pages, since the Web API needs a Premium account.
type Spotify struct {
	ClientID     string `toml:"client_id" json:"client_id"`
	ClientSecret string `toml:"client_secret" json:"client_secret"`
}

type YouTube struct {
	SearchQuery        string   `toml:"search_query" json:"search_query"`
	FallbackQuery      string   `toml:"fallback_query" json:"fallback_query"`
	SearchResults      int      `toml:"search_results" json:"search_results"`
	MaxDurationDiff    Duration `toml:"max_duration_diff" json:"max_duration_diff"`
	CookiesFile        string   `toml:"cookies_file" json:"cookies_file"`
	CookiesFromBrowser string   `toml:"cookies_from_browser" json:"cookies_from_browser"`
	ExtraArgs          []string `toml:"extra_args" json:"extra_args"`
}

type Tools struct {
	YtDlp   string `toml:"yt_dlp" json:"yt_dlp"`
	FFmpeg  string `toml:"ffmpeg" json:"ffmpeg"`
	FFprobe string `toml:"ffprobe" json:"ffprobe"`
}

// Duration reads and writes as a Go duration string ("10s") in both TOML and
// JSON, where time.Duration alone would be an integer of nanoseconds.
type Duration struct{ time.Duration }

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func Default() Config {
	return Config{
		Output:             "~/Music",
		OutputTemplate:     library.DefaultTemplate,
		PlaylistFolder:     true,
		PlaylistFolderCase: "lower",
		Format:             "opus",
		Progress:           "auto",
		Duplicates:         "link",
		DownloadRetries:    2,
		Jobs:               4,
		ResolveJobs:        8,
		YouTube: YouTube{
			SearchQuery:     "{artists} - {title}",
			FallbackQuery:   "{artists} - {title} audio",
			SearchResults:   5,
			MaxDurationDiff: Duration{10 * time.Second},
			ExtraArgs:       []string{},
		},
		Tools: Tools{YtDlp: "yt-dlp", FFmpeg: "ffmpeg", FFprobe: "ffprobe"},
	}
}

// DefaultPath is $SPOTIFY_DL_CONFIG if set, else the XDG config location.
func DefaultPath() (string, error) {
	if p := os.Getenv("SPOTIFY_DL_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config dir: %w", err)
	}
	return filepath.Join(dir, "spotify-dl", "config.toml"), nil
}

// Load layers defaults, the file at path (missing is fine), SPOTIFY_DL_*
// environment variables and then flags, given as raw values keyed by
// setting key.
func Load(path string, flags map[string]string) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	default:
		if undec := md.Undecoded(); len(undec) > 0 {
			return Config{}, fmt.Errorf("%s: %w: unknown key %q", path, ErrInvalid, undec[0].String())
		}
	}

	for _, s := range cfg.Settings() {
		if v, ok := os.LookupEnv(s.Env()); ok {
			if err := s.Set(v); err != nil {
				return Config{}, fmt.Errorf("%s: %w: %w", s.Env(), ErrInvalid, err)
			}
		}
		if v, ok := flags[s.Key]; ok {
			if err := s.Set(v); err != nil {
				return Config{}, fmt.Errorf("--%s: %w: %w", s.Flag(), ErrInvalid, err)
			}
		}
	}

	if cfg.IndexPath == "" {
		if cfg.IndexPath, err = index.DefaultPath(); err != nil {
			return Config{}, err
		}
	}
	for _, p := range []*string{&cfg.Output, &cfg.IndexPath, &cfg.YouTube.CookiesFile, &cfg.Tools.YtDlp, &cfg.Tools.FFmpeg, &cfg.Tools.FFprobe} {
		if *p, err = ExpandHome(*p); err != nil {
			return Config{}, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) HasAPICredentials() bool {
	return c.Spotify.ClientID != "" && c.Spotify.ClientSecret != ""
}

func (c Config) Validate() error {
	var errs []error
	if (c.Spotify.ClientID == "") != (c.Spotify.ClientSecret == "") {
		errs = append(errs, errors.New("set both spotify.client_id and spotify.client_secret, or neither"))
	}
	if c.Output == "" {
		errs = append(errs, errors.New("output must not be empty"))
	}
	if err := library.ValidateTemplate(c.OutputTemplate); err != nil {
		errs = append(errs, fmt.Errorf("output_template: %w", err))
	}
	if !slices.Contains(library.FolderCases, c.PlaylistFolderCase) {
		errs = append(errs, fmt.Errorf("playlist_folder_case %q must be one of %s", c.PlaylistFolderCase, strings.Join(library.FolderCases, ", ")))
	}
	if !slices.Contains(formats, c.Format) {
		errs = append(errs, fmt.Errorf("format %q must be one of %s", c.Format, strings.Join(formats, ", ")))
	}
	if !slices.Contains(duplicateModes, c.Duplicates) {
		errs = append(errs, fmt.Errorf("duplicates %q must be one of %s", c.Duplicates, strings.Join(duplicateModes, ", ")))
	}
	if !slices.Contains(progressAnswers, c.Progress) {
		errs = append(errs, fmt.Errorf("progress %q must be one of %s", c.Progress, strings.Join(progressAnswers, ", ")))
	}
	if c.Bitrate != "" && !bitrate.MatchString(c.Bitrate) {
		errs = append(errs, fmt.Errorf("bitrate %q must look like 320k, or be empty for best quality", c.Bitrate))
	}
	if c.DownloadRetries < 0 || c.DownloadRetries > 10 {
		errs = append(errs, fmt.Errorf("download_retries must be 0-10, got %d", c.DownloadRetries))
	}
	if c.Jobs < 1 {
		errs = append(errs, fmt.Errorf("jobs must be at least 1, got %d", c.Jobs))
	}
	if c.ResolveJobs < 1 {
		errs = append(errs, fmt.Errorf("resolve_jobs must be at least 1, got %d", c.ResolveJobs))
	}
	if !strings.Contains(c.YouTube.SearchQuery, "{title}") {
		errs = append(errs, errors.New("youtube.search_query must contain {title}"))
	}
	if c.YouTube.FallbackQuery != "" && !strings.Contains(c.YouTube.FallbackQuery, "{title}") {
		errs = append(errs, errors.New("youtube.fallback_query must contain {title}, or be empty to disable it"))
	}
	if c.YouTube.SearchResults < 1 || c.YouTube.SearchResults > 50 {
		errs = append(errs, fmt.Errorf("youtube.search_results must be 1-50, got %d", c.YouTube.SearchResults))
	}
	if c.YouTube.MaxDurationDiff.Duration <= 0 {
		errs = append(errs, errors.New("youtube.max_duration_diff must be positive"))
	}
	if c.YouTube.CookiesFile != "" && c.YouTube.CookiesFromBrowser != "" {
		errs = append(errs, errors.New("set youtube.cookies_file or youtube.cookies_from_browser, not both"))
	}
	if c.Tools.YtDlp == "" || c.Tools.FFmpeg == "" || c.Tools.FFprobe == "" {
		errs = append(errs, errors.New("tools.yt_dlp, tools.ffmpeg and tools.ffprobe must not be empty"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
	}
	return nil
}

// Redacted returns a copy safe to print or hand to another process.
func (c Config) Redacted() Config {
	if c.Spotify.ClientSecret != "" {
		c.Spotify.ClientSecret = "<redacted>"
	}
	return c
}

func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expanding %s: %w", p, err)
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}
