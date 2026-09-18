package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/BurntSushi/toml"

	"github.com/sumdahl/spotify-dl/internal/config"
	"github.com/sumdahl/spotify-dl/internal/deezer"
	"github.com/sumdahl/spotify-dl/internal/spotify"
	"github.com/sumdahl/spotify-dl/internal/youtube"
)

const (
	exitOK      = 0
	exitPartial = 1
	exitFatal   = 2
)

// Set at build time: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

const usage = `usage: spotify-dl <command> [flags]

commands:
  download <spotify-url>   download a track, album or playlist
  watch                    watch the clipboard for Spotify URLs
  config                   show the effective configuration
  config path              print the config file location
  config settings          list every setting with its flag and env variable
  version                  print the version

Every setting can be given as a flag, a SPOTIFY_DL_* environment variable or
a key in the config file, in that order of precedence.
Run "spotify-dl <command> -h" for flags.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitFatal
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "download":
		return download(ctx, args[1:], stderr)
	case "config":
		return configCmd(args[1:], stdout, stderr)
	case "watch":
		fmt.Fprintln(stderr, "spotify-dl: watch is not implemented yet")
		return exitFatal
	case "version", "--version":
		fmt.Fprintln(stdout, version)
		return exitOK
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "spotify-dl: unknown command %q\n\n%s", args[0], usage)
		return exitFatal
	}
}

type cli struct {
	fs         *flag.FlagSet
	configPath string
	json       bool
	verbose    bool
	overrides  map[string]string // setting key -> raw flag value
}

// newCLI registers the common flags plus one flag per config setting.
func newCLI(name, synopsis string, stderr io.Writer) *cli {
	c := &cli{fs: flag.NewFlagSet(name, flag.ContinueOnError), overrides: map[string]string{}}
	c.fs.SetOutput(stderr)
	c.fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: spotify-dl %s\n\nflags:\n", synopsis)
		c.fs.PrintDefaults()
	}
	c.fs.StringVar(&c.configPath, "config", "", "config file (default $SPOTIFY_DL_CONFIG or $XDG_CONFIG_HOME/spotify-dl/config.toml)")
	c.fs.BoolVar(&c.json, "json", false, "machine-readable output on stdout")
	c.fs.BoolVar(&c.verbose, "v", false, "verbose (debug) logging")

	def := config.Default()
	for _, s := range def.Settings() {
		usage := s.Usage
		if d := s.String(); d != "" {
			usage += fmt.Sprintf(" (default %q)", d)
		}
		c.fs.Func(s.Flag(), usage, func(v string) error {
			c.overrides[s.Key] = v
			return nil
		})
	}
	return c
}

// parse accepts flags before and after positional arguments, so both
// `download --json <url>` and `download <url> --json` work.
func (c *cli) parse(args []string) ([]string, error) {
	var positional []string
	for {
		if err := c.fs.Parse(args); err != nil {
			return nil, err
		}
		rest := c.fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func (c *cli) load() (config.Config, string, error) {
	path := c.configPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return config.Config{}, "", err
		}
		path = p
	}
	cfg, err := config.Load(path, c.overrides)
	return cfg, path, err
}

func setupLogging(w io.Writer, verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})))
}

func download(ctx context.Context, args []string, stderr io.Writer) int {
	c := newCLI("download", "download [flags] <spotify-url>", stderr)
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil {
		return exitFatal
	}
	if len(positional) != 1 {
		c.fs.Usage()
		return exitFatal
	}
	setupLogging(stderr, c.verbose)

	cfg, _, err := c.load()
	if err != nil {
		fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
		return exitFatal
	}
	ref, err := spotify.ParseURL(positional[0])
	if err != nil {
		fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
		return exitFatal
	}

	tracks, err := resolveMetadata(ctx, cfg, ref)
	if err != nil {
		fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
		return exitFatal
	}
	fmt.Fprintf(stderr, "%s %s: %d track(s)\n", ref.Kind, ref.ID, len(tracks))

	yt := youtube.New(youtube.Options{
		Binary:             cfg.Tools.YtDlp,
		SearchQuery:        cfg.YouTube.SearchQuery,
		SearchResults:      cfg.YouTube.SearchResults,
		MaxDurationDiff:    cfg.YouTube.MaxDurationDiff.Duration,
		CookiesFile:        cfg.YouTube.CookiesFile,
		CookiesFromBrowser: cfg.YouTube.CookiesFromBrowser,
		ExtraArgs:          cfg.YouTube.ExtraArgs,
	})
	failed := 0
	for i, t := range tracks {
		fmt.Fprintf(stderr, "%3d. %s – %s (%s #%d-%d, %d, %d:%02d, %s)\n",
			i+1, strings.Join(t.Artists, ", "), t.Title, t.Album, t.DiscNumber, t.TrackNumber, t.Year,
			int(t.Duration.Minutes()), int(t.Duration.Seconds())%60, orDash(t.ISRC))

		best, _, err := yt.Resolve(ctx, t)
		switch {
		case ctx.Err() != nil:
			return exitFatal
		case errors.Is(err, youtube.ErrToolMissing):
			fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
			return exitFatal
		case err != nil:
			failed++
			fmt.Fprintf(stderr, "     ✗ %v\n", err)
		default:
			fmt.Fprintf(stderr, "     → %s  %q by %s (score %.0f, %d:%02d)\n",
				best.URL, best.Title, best.Channel, best.Score,
				int(best.Duration.Minutes()), int(best.Duration.Seconds())%60)
		}
	}
	fmt.Fprintln(stderr, "(matching only: downloading is not implemented yet)")
	if failed > 0 {
		return exitPartial
	}
	return exitOK
}

// resolveMetadata uses the official API when credentials are configured.
// Otherwise it reads Spotify's public pages and fills in ISRC and disc numbers
// from Deezer, which is best effort: a failed lookup only costs those tags.
func resolveMetadata(ctx context.Context, cfg config.Config, ref spotify.Ref) ([]spotify.Track, error) {
	if cfg.HasAPICredentials() {
		slog.DebugContext(ctx, "metadata source: Spotify Web API")
		return spotify.NewAPI(cfg.Spotify.ClientID, cfg.Spotify.ClientSecret).Resolve(ctx, ref)
	}

	slog.DebugContext(ctx, "metadata source: Spotify public pages + Deezer")
	tracks, err := spotify.NewWeb("").Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}

	dz := deezer.New("")
	if ref.Kind == spotify.KindAlbum {
		err = dz.EnrichAlbum(ctx, tracks)
	} else {
		for i := range tracks {
			if err = dz.EnrichTrack(ctx, &tracks[i]); err != nil {
				break
			}
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.WarnContext(ctx, "Deezer lookup failed; ISRC and disc numbers may be missing", "err", err)
	}
	return tracks, nil
}

func configCmd(args []string, stdout, stderr io.Writer) int {
	c := newCLI("config", "config [path|settings] [flags]", stderr)
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil || len(positional) > 1 {
		return exitFatal
	}
	cfg, path, err := c.load()
	if err != nil {
		fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
		return exitFatal
	}

	sub := ""
	if len(positional) == 1 {
		sub = positional[0]
	}
	switch sub {
	case "":
		if c.json {
			err = writeJSON(stdout, cfg.Redacted())
		} else {
			err = toml.NewEncoder(stdout).Encode(cfg.Redacted())
		}
	case "path":
		if c.json {
			_, statErr := os.Stat(path)
			err = writeJSON(stdout, map[string]any{"path": path, "exists": statErr == nil})
		} else {
			_, err = fmt.Fprintln(stdout, path)
		}
	case "settings":
		err = printSettings(stdout, cfg, c.json)
	default:
		fmt.Fprintf(stderr, "spotify-dl: unknown config subcommand %q\n", sub)
		return exitFatal
	}
	if err != nil {
		fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
		return exitFatal
	}
	return exitOK
}

func printSettings(w io.Writer, cfg config.Config, asJSON bool) error {
	def := config.Default()
	defs := def.Settings()
	cur := cfg.Settings()
	value := func(s config.Setting) string {
		if v := s.String(); !s.Secret || v == "" {
			return v
		}
		return "<redacted>"
	}

	if asJSON {
		type entry struct {
			Key     string `json:"key"`
			Flag    string `json:"flag"`
			Env     string `json:"env"`
			Type    string `json:"type"`
			Default string `json:"default"`
			Value   string `json:"value"`
			Secret  bool   `json:"secret"`
			Usage   string `json:"usage"`
		}
		out := make([]entry, len(cur))
		for i, s := range cur {
			out[i] = entry{s.Key, "--" + s.Flag(), s.Env(), s.Type(), defs[i].String(), value(s), s.Secret, s.Usage}
		}
		return writeJSON(w, out)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tFLAG\tENV\tVALUE")
	for _, s := range cur {
		fmt.Fprintf(tw, "%s\t--%s\t%s\t%s\n", s.Key, s.Flag(), s.Env(), value(s))
	}
	return tw.Flush()
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
