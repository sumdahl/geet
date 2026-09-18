# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state

Deliverables 1 and 2 are done. `cmd/spotify-dl download` resolves metadata and the YouTube match for each track but doesn't download yet, and `watch` is a stub. The docs in `docs/` are the spec, so read the relevant one before implementing a component. `docs/00-overview.md` indexes them.

## Metadata sources

The user's Spotify account is free, and the Web API needs Premium, so there are two sources (details in `docs/01-engine-spec.md` §1):
- **Keyless, the default:** `spotify.Web` scrapes the embed pages' `__NEXT_DATA__` plus the track page's `music:*` meta tags. Those tags are only served to a crawler User-Agent (`facebookexternalhit/1.1`); a browser UA gets none.
  - Playlists cap at 100 tracks.
  - Album tracks are numbered by position.
  - `internal/deezer` then fills in ISRC and disc/track numbers, best effort. A Deezer failure only logs a warning.
- **Official API:** `spotify.API` is used only when the config has credentials, via `cmd/spotify-dl` `resolve()`.
- Both sources return `spotify.Track`.
- Deezer search is geo-filtered. From Nepal it hides roughly 30% of major-label tracks, even though `/track/isrc:{isrc}` finds them. Missing ISRCs are therefore expected and not a matching bug.
- Scraped pages break silently when Spotify changes its markup. Parsing failures wrap `spotify.ErrPageFormat`. When one fires, fetch the live page and update both the parser and the fixtures in `internal/spotify/testdata`.
- Spotify pages and the Deezer API both return absolute `next` URLs, so the test fixtures use a `{{base}}` placeholder that the test server rewrites.

Work proceeds in the order given in `docs/07-roadmap.md`, and **you must pause for user review after each deliverable**. After deliverable 3 (single-track download, tag and `--json`), stop and let the user use the engine by hand before starting on concurrency or the plugin. Do not build the playerctl v2 feature (`docs/05-future-v2-playerctl.md`) until plugin v1 works.

## What this is

A Go CLI that takes a Spotify track, album or playlist URL, gets its metadata (see below), finds the matching audio on YouTube, downloads it with `yt-dlp`, transcodes it with `ffmpeg`, then tags and saves it. It behaves like spotdl but is a native binary.

## Architecture rules (non-negotiable)

- **Never reimplement YouTube extraction.** Shell out to the `yt-dlp` binary for search and download, and to `ffmpeg` for transcoding and embedding cover art. Go handles metadata resolution, match scoring, orchestration, concurrency and the CLI.
- **The engine has no Omarchy dependency.** `spotify-dl` must work the same from any terminal on any Linux box. The Omarchy/Quickshell plugin (`omarchy-plugin-spotify-dl`, a separate repo built later) only runs this binary and reads its NDJSON output. It never imports engine internals.

## Layout

- `internal/config`: the engine is a backend for the Omarchy plugin, so everything is configurable.
  - `Config.Settings()` is the single list: each entry is automatically a TOML key, a `--flag` and a `SPOTIFY_DL_*` env variable (precedence flag > env > file > default), and is listed by `spotify-dl config settings --json`.
  - To add a setting, add the struct field and one `Settings()` entry. `TestSettingsCoverConfig` fails if you forget the entry.
  - The config file is optional. Unknown keys in it are an error.
- `internal/spotify` and `internal/deezer`: built. See "Metadata sources".
- `internal/youtube`: built.
  - `yt-dlp ytsearchN:<query> --flat-playlist --dump-json` (about 1.5s), then scoring in `score.go`, which keeps every candidate's score or rejection reason (`-v` logs them).
  - `testdata/*.ndjson` are real searches. When a live search picks wrong, capture it as a fixture and add a case to `TestBestOnRealSearches` before changing weights.
  - `testdata/fake-yt-dlp` stands in for the binary in tests.
- `internal/library`: output path = `output` + `output_template` + extension. Each template segment is exactly one path component, sanitized.
- `internal/textnorm`: shared title/name normalization. It keeps Unicode marks so Devanagari words don't split apart.
- `internal/download`: `yt-dlp -f bestaudio --extract-audio --audio-format {opus|flac|mp3} --audio-quality 0` into a temp path.
- `internal/tag`: ID3v2 for mp3 (`bogem/id3v2`) and Vorbis comments for flac/opus. `ffmpeg -metadata … -disposition:v attached_pic` is the cross-format fallback for cover art. ISRC goes in TXXX.
- `cmd/spotify-dl`: the subcommands `download <url>` and `watch`. `watch` is a daemon that polls `wl-paste` (Wayland, not xclip) about once a second and sends a `notify-send` notification with the cover art. Flags: `--format --output --bitrate --jobs --resolve-jobs --json`.

## Playlist pipeline

`tracks → [resolve pool] → [download pool] → [tag pool]` (see `docs/02-concurrency-pipeline.md`):
- Resolve pool defaults to 8 workers (`--resolve-jobs`), download pool to 4 (`--jobs`). The tag pool is fixed at 2 and is not configurable.
- Each stage is an `errgroup` worker set sharing one `context.Context`, so a single failure or Ctrl+C stops all three stages.
- Every channel is bounded (cap ≈ that stage's concurrency) so downloads push back on resolve.
- An NDJSON line is emitted each time a track crosses a stage boundary, not only at the end.

## `--json` contract (stable API: the plugin depends on it)

- stdout carries NDJSON only. All human-readable logs go to stderr.
- Line shape: `{"track":"...","stage":"resolved|downloading|tagging|done|failed","error":"..."}`
- `watch --json` streams the same line shape continuously.
- Exit codes: `0` all tracks succeeded, `1` partial failure, `2` fatal (bad URL, auth failure, missing `yt-dlp`/`ffmpeg`).

## Code style

- Comment only where the reason isn't obvious from the code, such as an API quirk or a workaround.
- Use `slog` for logging and wrapped errors (`%w`, checked with `errors.Is`/`errors.As`). Pass `context.Context` through every HTTP call and subprocess.
- Write `any`, not `interface{}`. Use generics only where they remove duplication, and skip interfaces for code with a single implementation.

## Testing

- Write table-driven tests for URL parsing and for YouTube scoring, using fixture JSON rather than live calls.
- Include a tag round-trip test that writes tags and reads them back.
- Include a pipeline cancellation test that cancels mid-flight and asserts no goroutines leak.
- Tests must not use the live network: mock the Spotify HTTP client and stub `yt-dlp` JSON output.
- Commands: `go test -race ./...`, a single test with `go test ./internal/spotify -run TestParseURL`, `go vet ./...`, `gofmt -l .`. Run locally with `go run ./cmd/spotify-dl download <url>` (add `-v` for debug logs).

## Repo-local skill

`golang-performance` (from `samber/cc-skills-golang`, pinned in `skills-lock.json`) lives in `.agents/skills/`. The `.claude/`, `.kiro/`, `.qwen/`, `.junie/` and `.qoder/` skill directories are symlinks to it, so edit or update it only in `.agents/`. Use it for optimization patterns once profiling or benchmarks have found a bottleneck, not for measuring.
