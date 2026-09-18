package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sumdahl/spotify-dl/internal/config"
	"github.com/sumdahl/spotify-dl/internal/spotify"
)

const (
	exitOK      = 0
	exitPartial = 1
	exitFatal   = 2
)

const usage = `usage: spotify-dl <command> [flags]

commands:
  download <spotify-url>   download a track, album or playlist
  watch                    watch the clipboard for Spotify URLs

run "spotify-dl <command> -h" for flags
`

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitFatal
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "download":
		return download(ctx, args[1:], stderr)
	case "watch":
		fmt.Fprintln(stderr, "spotify-dl: watch is not implemented yet")
		return exitFatal
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stderr, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "spotify-dl: unknown command %q\n\n%s", args[0], usage)
		return exitFatal
	}
}

type flags struct {
	configPath  string
	output      string
	format      string
	bitrate     string
	jobs        int
	resolveJobs int
	json        bool
	verbose     bool
}

func newFlagSet(name string, stderr io.Writer, f *flags) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.configPath, "config", "", "config file (default $XDG_CONFIG_HOME/spotify-dl/config.toml)")
	fs.StringVar(&f.output, "output", "", "music library directory")
	fs.StringVar(&f.format, "format", "", "audio format: opus, flac or mp3")
	fs.StringVar(&f.bitrate, "bitrate", "", "audio bitrate, e.g. 320k")
	fs.IntVar(&f.jobs, "jobs", 0, "concurrent downloads")
	fs.IntVar(&f.resolveJobs, "resolve-jobs", 0, "concurrent YouTube lookups")
	fs.BoolVar(&f.json, "json", false, "emit NDJSON progress on stdout")
	fs.BoolVar(&f.verbose, "v", false, "verbose logging")
	return fs
}

// loadConfig applies explicitly set flags over the config file.
func loadConfig(f flags) (config.Config, error) {
	path := f.configPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			return config.Config{}, err
		}
		path = p
	}
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, err
	}
	if f.output != "" {
		out, err := config.ExpandHome(f.output)
		if err != nil {
			return config.Config{}, err
		}
		cfg.Output = out
	}
	if f.format != "" {
		cfg.Format = f.format
	}
	if f.bitrate != "" {
		cfg.Bitrate = f.bitrate
	}
	if f.jobs != 0 {
		cfg.Jobs = f.jobs
	}
	if f.resolveJobs != 0 {
		cfg.ResolveJobs = f.resolveJobs
	}
	return cfg, cfg.Validate()
}

func download(ctx context.Context, args []string, stderr io.Writer) int {
	var f flags
	fs := newFlagSet("download", stderr, &f)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: spotify-dl download [flags] <spotify-url>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitFatal
	}
	// Let flags follow the URL too: `download <url> --json`.
	rest := fs.Args()
	var positional []string
	for len(rest) > 0 {
		positional = append(positional, rest[0])
		if err := fs.Parse(rest[1:]); err != nil {
			return exitFatal
		}
		rest = fs.Args()
	}
	if len(positional) != 1 {
		fs.Usage()
		return exitFatal
	}
	setupLogging(stderr, f.verbose)

	cfg, err := loadConfig(f)
	if err != nil {
		fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
		return exitFatal
	}
	ref, err := spotify.ParseURL(positional[0])
	if err != nil {
		fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
		return exitFatal
	}

	client := spotify.New(cfg.Spotify.ClientID, cfg.Spotify.ClientSecret)
	tracks, err := client.Resolve(ctx, ref)
	if err != nil {
		fmt.Fprintf(stderr, "spotify-dl: %v\n", err)
		return exitFatal
	}

	fmt.Fprintf(stderr, "%s %s: %d track(s)\n", ref.Kind, ref.ID, len(tracks))
	for i, t := range tracks {
		fmt.Fprintf(stderr, "%3d. %s – %s (%s, %d, %d:%02d, %s)\n",
			i+1, strings.Join(t.Artists, ", "), t.Title, t.Album, t.Year,
			int(t.Duration.Minutes()), int(t.Duration.Seconds())%60, orDash(t.ISRC))
	}
	fmt.Fprintln(stderr, "(metadata only: downloading is not implemented yet)")
	return exitOK
}

func setupLogging(w io.Writer, verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
