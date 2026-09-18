# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state

Deliverables 1–3 are done: `download` works end to end (match, download, convert, tag, embed cover), one track at a time. `watch` is a stub. **Per the roadmap, the user uses the engine by hand now, before deliverable 4 (concurrency) starts.** The docs in `docs/` are the spec, so read the relevant one before implementing a component. `docs/00-overview.md` indexes them.

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
- `internal/library`: output path = `output` (plus `FolderName(playlist)` for playlists) + `output_template` + extension. Each template segment is exactly one path component, sanitized.
- Resolving a link returns a `spotify.Collection` (ref, name, tracks). The name is what the playlist folder is named after.
- `internal/textnorm`: shared title/name normalization. It keeps Unicode marks so Devanagari words don't split apart.
- `internal/ytdlp`: the shared yt-dlp runner (cookies and extra args), used by both search and download.
- `internal/download`: fetches the raw best audio only. It deliberately avoids `--extract-audio`, which ignores the requested bitrate when the codecs match.
- `internal/audio`: one ffmpeg pass that converts or copies, tags and embeds the cover (see docs/01 §4 for the opus cover and argument-length details). Its round-trip tests run real ffmpeg on a generated tone and skip when ffmpeg is missing.
- `cmd/spotify-dl/download.go`: the per-track stages and the NDJSON `event` type. Only ever add event fields; docs/03 has the schema.
- Before downloads start, resolving a playlist is slow (about 1s of page reads per track, then the Deezer lookups). `resolveMetadata` reports both steps, which drive `ui.phase` lines and NDJSON `reading` events.
- Downloads retry `download_retries` times (default 2), because YouTube fails transiently (403s, throttling). yt-dlp errors lead with yt-dlp's own reason so it survives truncation in the display.
- `cmd/spotify-dl/ui.go`: the stderr display, using mpb bars (`barUI`) in a terminal and plain lines (`plainUI`) otherwise.
  - A finished track's bar is removed and a permanent result line is logged above the live area, because mpb never draws a bar that completes before its first refresh.
  - Never call into a `*mpb.Bar` while holding `barTrack.mu`: the render goroutine takes that lock in `status`.
  - To test the animation headlessly, the pty needs a size: `script -qefc "stty cols 150 rows 40; <cmd>" /dev/null`. With 0 rows mpb draws nothing.
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
