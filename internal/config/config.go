package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

var ErrInvalid = errors.New("invalid config")

var formats = []string{"opus", "flac", "mp3"}

// Spotify credentials are optional: with none, metadata comes from Spotify's
// public web pages, since the Web API needs a Premium account.
type Spotify struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
}

type Config struct {
	Spotify     Spotify `toml:"spotify"`
	Output      string  `toml:"output"`
	Format      string  `toml:"format"`
	Bitrate     string  `toml:"bitrate"`
	Jobs        int     `toml:"jobs"`
	ResolveJobs int     `toml:"resolve_jobs"`
}

func Default() Config {
	return Config{
		Output:      "~/Music",
		Format:      "opus",
		Jobs:        4,
		ResolveJobs: 8,
	}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config dir: %w", err)
	}
	return filepath.Join(dir, "spotify-dl", "config.toml"), nil
}

// Load reads path over the defaults. A missing file is not an error; every
// setting has a default.
func Load(path string) (Config, error) {
	cfg := Default()
	if _, err := toml.DecodeFile(path, &cfg); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	out, err := ExpandHome(cfg.Output)
	if err != nil {
		return Config{}, err
	}
	cfg.Output = out
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
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
	if !slices.Contains(formats, c.Format) {
		errs = append(errs, fmt.Errorf("format %q must be one of %s", c.Format, strings.Join(formats, ", ")))
	}
	if c.Jobs < 1 {
		errs = append(errs, fmt.Errorf("jobs must be at least 1, got %d", c.Jobs))
	}
	if c.ResolveJobs < 1 {
		errs = append(errs, fmt.Errorf("resolve_jobs must be at least 1, got %d", c.ResolveJobs))
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalid, errors.Join(errs...))
	}
	return nil
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
