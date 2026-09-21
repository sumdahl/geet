package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/sumdahl/geet/internal/config"
	"github.com/sumdahl/geet/internal/deezer"
	"github.com/sumdahl/geet/internal/itunes"
	"github.com/sumdahl/geet/internal/spotify"
)

const (
	exitOK      = 0
	exitPartial = 1
	exitFatal   = 2
)

// version is stamped at build time from the git tag:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" ./cmd/geet
//
// Unstamped builds fall back to the module version (set by
// `go install github.com/sumdahl/geet/cmd/geet@v0.1.0`) or "dev".
var version = ""

const usage = `usage: geet <command> [flags]

commands:
  download <link>          download a Spotify track, album or playlist
                           (also Apple Music and Deezer song links, and the
                           itunes:<id> / deezer:<id> refs geet search prints)
  search <words…>          find a song by name, pick it from a menu, download it
  play [words… | link]     play your library, or a song you name, with a
                           spectrum and synced lyrics (no argument: everything)
  watch                    download each Spotify link you copy, until stopped
  doctor                   check tools, setup and services, and how to fix problems
  config                   show the effective configuration
  config init              write the config file, every setting commented, to edit
  config path              print the config file location
  config settings          list every setting with its flag and env variable
  version                  print the version

Every setting can be given as a flag, a GEET_* environment variable or
a key in the config file, in that order of precedence.
Run "geet <command> -h" for flags.
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
		return downloadCmd(ctx, args[1:], stdout, stderr)
	case "search":
		return searchCmd(ctx, args[1:], stdout, stderr)
	case "play":
		return playCmd(ctx, args[1:], stdout, stderr)
	case "doctor":
		return doctorCmd(ctx, args[1:], stdout, stderr)
	case "config":
		return configCmd(args[1:], stdout, stderr)
	case "watch":
		return watchCmd(ctx, args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, versionString())
		return exitOK
	case "-h", "-help", "--help", "help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "geet: unknown command %q\n\n%s", args[0], usage)
		return exitFatal
	}
}

type cli struct {
	fs         *flag.FlagSet
	help       func() // the full flag list, for -h
	configPath string
	json       bool
	verbose    bool
	overrides  map[string]string // setting key -> raw flag value
}

// newCLI registers the common flags plus one flag per config setting.
func newCLI(name, synopsis string, stderr io.Writer) *cli {
	c := &cli{fs: flag.NewFlagSet(name, flag.ContinueOnError), overrides: map[string]string{}}
	c.fs.SetOutput(stderr)
	// The flag package calls Usage on every parse error; a typo should get
	// the error and a pointer, not all the flags. -h gets the full list
	// (see parse).
	c.fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: geet %s\nRun \"geet %s -h\" to see all flags.\n", synopsis, name)
	}
	c.help = func() {
		fmt.Fprintf(stderr, "usage: geet %s\n\nflags:\n", synopsis)
		c.fs.PrintDefaults()
	}
	c.fs.StringVar(&c.configPath, "config", "", "config file (default $GEET_CONFIG or $XDG_CONFIG_HOME/geet/config.toml)")
	c.fs.BoolVar(&c.json, "json", false, "machine-readable output on stdout")
	c.fs.BoolVar(&c.verbose, "v", false, "verbose (debug) logging")

	def := config.Default()
	for _, s := range def.Settings() {
		usage := s.Usage
		if d := s.String(); d != "" {
			usage += fmt.Sprintf(" (default %q)", d)
		}
		set := func(v string) error {
			c.overrides[s.Key] = v
			return nil
		}
		if s.Type() == "bool" {
			c.fs.BoolFunc(s.Flag(), usage, set) // --overwrite, --overwrite=false
		} else {
			c.fs.Func(s.Flag(), usage, set)
		}
	}
	return c
}

// parse accepts flags before and after positional arguments, so both
// `download --json <url>` and `download <url> --json` work.
func (c *cli) parse(args []string) ([]string, error) {
	// Answer -h here: the flag package would print the short usage first.
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "-h" || a == "-help" || a == "--help" {
			c.help()
			return nil, flag.ErrHelp
		}
	}
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

var pseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}`)

// versionString is "geet v0.1.0 (80f7f43, 2026-09-18)": the release, plus
// the exact commit and its date from the build's embedded VCS info, so a bug
// report pins down the code even between releases.
func versionString() string {
	v := version
	var rev, date string
	dirty := false
	if bi, ok := debug.ReadBuildInfo(); ok {
		// Go stamps local builds with a pseudo-version such as
		// v0.0.0-20260918105524-80f7f43ed8ab+dirty; only a real release
		// (go install …@v0.1.0) is worth showing.
		if mv := bi.Main.Version; v == "" && mv != "(devel)" && !pseudoVersion.MatchString(mv) {
			v = mv
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value[:min(7, len(s.Value))]
			case "vcs.time":
				date = s.Value[:min(10, len(s.Value))]
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
	}
	if v == "" {
		v = "dev"
	}
	var detail []string
	if rev != "" {
		if dirty {
			rev += "-dirty"
		}
		detail = append(detail, rev)
	}
	if date != "" {
		detail = append(detail, date)
	}
	if len(detail) == 0 {
		return "geet " + v
	}
	return fmt.Sprintf("geet %s (%s)", v, strings.Join(detail, ", "))
}

// setupLogging sends log records to w: every detail with -v, otherwise only
// warnings and errors, as plain "warning: …" lines rather than slog's
// timestamped key=value format.
func setupLogging(w io.Writer, verbose bool) {
	if verbose {
		slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})))
		return
	}
	slog.SetDefault(slog.New(&briefHandler{w: w}))
}

// briefHandler prints "warning: skipping a track Spotify no longer has (id=…)".
type briefHandler struct {
	mu    sync.Mutex
	w     io.Writer
	attrs []slog.Attr
}

func (h *briefHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }

func (h *briefHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if r.Level >= slog.LevelError {
		b.WriteString("error: ")
	} else {
		b.WriteString("warning: ")
	}
	b.WriteString(r.Message)
	var attrs []string
	add := func(a slog.Attr) bool {
		attrs = append(attrs, a.Key+"="+a.Value.String())
		return true
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(add)
	if len(attrs) > 0 {
		b.WriteString(" (" + strings.Join(attrs, ", ") + ")")
	}
	b.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *briefHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &briefHandler{w: h.w, attrs: append(slices.Clone(h.attrs), attrs...)}
}

func (h *briefHandler) WithGroup(string) slog.Handler { return h }

// resolveMetadata reads the link's tracks: from the official API when
// credentials are configured, otherwise from Spotify's public pages. Those
// lack ISRC and disc numbers, which come from Deezer (best effort: a failed
// lookup only costs those tags). An album's are looked up here in one go;
// for single tracks and playlists lateTags is true, and they are looked up
// per track in the pipeline, skipping tracks already downloaded.
//
// report, if set, is told how far each slow step has got: "spotify" while
// reading a playlist's tracks, "tags" during an album's Deezer lookup.
func resolveMetadata(ctx context.Context, cfg config.Config, ref spotify.Ref, report func(step string, done, total int)) (col spotify.Collection, lateTags bool, err error) {
	if report == nil {
		report = func(string, int, int) {}
	}
	if cfg.HasAPICredentials() {
		slog.DebugContext(ctx, "metadata source: Spotify Web API")
		col, err := spotify.NewAPI(cfg.Spotify.ClientID, cfg.Spotify.ClientSecret).Resolve(ctx, ref)
		return col, false, err
	}

	slog.DebugContext(ctx, "metadata source: Spotify public pages + Deezer")
	web := newWeb(ctx, cfg, report)
	defer saveCache(ctx, web)
	col, err = web.Resolve(ctx, ref)
	if err != nil {
		return spotify.Collection{}, false, err
	}
	if ref.Kind != spotify.KindAlbum {
		return col, true, nil
	}

	report("tags", 0, len(col.Tracks))
	if err := deezer.New("").EnrichAlbum(ctx, col.Tracks); err != nil {
		if ctx.Err() != nil {
			return spotify.Collection{}, false, ctx.Err()
		}
		slog.WarnContext(ctx, "Deezer lookup failed; ISRC and disc numbers may be missing", "err", err)
	}
	report("tags", len(col.Tracks), len(col.Tracks))
	return col, false, nil
}

// newWeb is the keyless Spotify reader, with the metadata cache
// (spotify.cache_days) unless it's turned off.
func newWeb(ctx context.Context, cfg config.Config, report func(step string, done, total int)) *spotify.Web {
	web := spotify.NewWeb("")
	web.Workers = cfg.ResolveJobs
	web.OnProgress = func(done, total int) { report("spotify", done, total) }
	if cfg.Spotify.CacheDays > 0 {
		if dir, err := os.UserCacheDir(); err == nil {
			web.Cache = spotify.LoadCache(filepath.Join(dir, "geet", "spotify.json"), time.Duration(cfg.Spotify.CacheDays)*24*time.Hour)
			slog.DebugContext(ctx, "spotify cache", "songs", web.Cache.Len())
		}
	}
	return web
}

// saveCache keeps what this run read, even when it failed partway: the songs
// read so far needn't be read again.
func saveCache(ctx context.Context, web *spotify.Web) {
	if web.Cache == nil {
		return
	}
	if err := web.Cache.Save(); err != nil {
		slog.WarnContext(ctx, "couldn't save the Spotify metadata cache", "err", err)
	}
}

// readLinks reads song links from r: one per line or separated by spaces,
// as the Spotify app copies them after selecting songs and pressing Ctrl+C.
// Blank lines and lines starting with # are ignored.
func readLinks(r io.Reader) ([]string, error) {
	var links []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		links = append(links, strings.Fields(line)...)
	}
	return links, sc.Err()
}

// resolveList reads the tracks behind a list of song links (Spotify links
// or URIs, Apple Music song links, itunes:<id>), in the given order and
// without repeats. It's how playlists over Spotify's 100-track public limit
// are downloaded in full.
// hintFrom, if set, is the playlist or album the links came from: its own
// page lists the details of up to 100 of them, so those need only their
// song pages (spotify.Web.Hint).
func resolveList(ctx context.Context, cfg config.Config, links []string, hintFrom spotify.Ref, report func(step string, done, total int)) ([]spotify.Track, error) {
	type slot struct{ spotifyID, itunesID, deezerID string }
	var slots []slot
	var spotifyIDs []string
	seen := map[string]bool{}
	for i, link := range links {
		if itunes.IsRef(link) {
			id, err := itunes.ParseRef(link)
			if err != nil {
				return nil, fmt.Errorf("link %d: %w", i+1, err)
			}
			if !seen["itunes:"+id] {
				seen["itunes:"+id] = true
				slots = append(slots, slot{itunesID: id})
			}
			continue
		}
		if deezer.IsRef(link) {
			id, err := deezer.ParseRef(link)
			if err != nil {
				return nil, fmt.Errorf("link %d: %w", i+1, err)
			}
			if !seen[deezer.RefPrefix+id] {
				seen[deezer.RefPrefix+id] = true
				slots = append(slots, slot{deezerID: id})
			}
			continue
		}
		ref, err := spotify.ParseURL(link)
		if errors.Is(err, spotify.ErrInvalidURL) && strings.HasPrefix(link, "spotify:episode:") {
			continue // a podcast episode copied along with the songs
		}
		if err != nil {
			return nil, fmt.Errorf("link %d: %w", i+1, err)
		}
		if ref.Kind != spotify.KindTrack {
			return nil, fmt.Errorf("link %d is %s link, but --tracks takes song links: select the songs in Spotify and press Ctrl+C", i+1, articled(string(ref.Kind)))
		}
		if !seen[ref.ID] {
			seen[ref.ID] = true
			slots = append(slots, slot{spotifyID: ref.ID})
			spotifyIDs = append(spotifyIDs, ref.ID)
		}
	}

	web := newWeb(ctx, cfg, report)
	defer saveCache(ctx, web)
	if hintFrom.ID != "" && (hintFrom.Kind == spotify.KindPlaylist || hintFrom.Kind == spotify.KindAlbum) {
		if _, err := web.Hint(ctx, hintFrom); err != nil {
			slog.DebugContext(ctx, "reading the listing for hints", "ref", hintFrom, "err", err)
		}
	}
	found, skipped, err := web.Tracks(ctx, spotifyIDs)
	if err != nil {
		return nil, err
	}
	if skipped > 0 {
		slog.WarnContext(ctx, fmt.Sprintf("%d of %d songs couldn't be read from Spotify and are skipped; run the same command again to get them", skipped, len(spotifyIDs)))
	}
	byID := make(map[string]spotify.Track, len(found))
	for _, t := range found {
		byID[t.ID] = t
	}

	var tracks []spotify.Track
	it := itunes.New("", cfg.Search.Country)
	dz := deezer.New("")
	for _, s := range slots {
		if s.deezerID != "" {
			t, err := dz.Lookup(ctx, s.deezerID)
			if err != nil {
				return nil, err
			}
			tracks = append(tracks, t)
			continue
		}
		if s.itunesID != "" {
			t, err := it.Lookup(ctx, s.itunesID)
			if err != nil {
				return nil, err
			}
			tracks = append(tracks, t)
			continue
		}
		if t, ok := byID[s.spotifyID]; ok { // missing: gone from Spotify, already warned
			tracks = append(tracks, t)
		}
	}
	return tracks, nil
}

func articled(word string) string {
	if strings.ContainsRune("aeiou", rune(word[0])) {
		return "an " + word
	}
	return "a " + word
}

func configCmd(args []string, stdout, stderr io.Writer) int {
	c := newCLI("config", "config [init|path|settings] [flags]", stderr)
	force := c.fs.Bool("force", false, "config init: replace an existing config file")
	positional, err := c.parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	if err != nil || len(positional) > 1 {
		return exitFatal
	}
	// init writes the file, so it mustn't need an existing one to load.
	if len(positional) == 1 && positional[0] == "init" {
		return configInit(c, *force, stdout, stderr)
	}
	cfg, path, err := c.load()
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
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
		fmt.Fprintf(stderr, "geet: unknown config subcommand %q\n", sub)
		return exitFatal
	}
	if err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	return exitOK
}

// configInit writes the config file with every setting listed at its
// default and commented out (config.Template), for the user to edit. An
// existing file is left alone unless force: it may hold the user's settings.
func configInit(c *cli, force bool, stdout, stderr io.Writer) int {
	path := c.configPath
	if path == "" {
		p, err := config.DefaultPath()
		if err != nil {
			fmt.Fprintf(stderr, "geet: %v\n", err)
			return exitFatal
		}
		path = p
	}
	if _, err := os.Stat(path); err == nil && !force {
		fmt.Fprintf(stderr, "geet: %s already exists; edit it, or run \"geet config init --force\" to replace it with the defaults\n", path)
		return exitFatal
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	if err := os.WriteFile(path, []byte(config.Template()), 0o644); err != nil {
		fmt.Fprintf(stderr, "geet: %v\n", err)
		return exitFatal
	}
	if c.json {
		if err := writeJSON(stdout, map[string]any{"path": path}); err != nil {
			fmt.Fprintf(stderr, "geet: %v\n", err)
			return exitFatal
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "Wrote %s: every setting at its default, commented out. Uncomment a line to change it.\n", path)
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

// newJSONEncoder writes JSON as consumers read it: URLs keep a literal "&"
// instead of the HTML-safe "\u0026" escape Go uses by default.
func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

func writeJSON(w io.Writer, v any) error {
	enc := newJSONEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
