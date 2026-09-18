# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state

Deliverable 1 is done: config loading (`internal/config`), the Spotify client and URL parsing (`internal/spotify`), and a `cmd/spotify-dl` whose `download` currently only resolves and prints metadata. `watch` is a stub. The docs in `docs/` are the spec, so read the relevant one before implementing a component. `docs/00-overview.md` indexes them.

Spotify client notes:
- Spotify returns absolute `next` URLs, so the fixture JSON in `internal/spotify/testdata` uses a `{{base}}` placeholder that the test server rewrites.
- Albums need a second batch `/tracks?ids=` call because album-track listings carry no ISRC.

Work proceeds in the order given in `docs/07-roadmap.md`, and **you must pause for user review after each deliverable**. After deliverable 3 (single-track download, tag and `--json`), stop and let the user use the engine by hand before starting on concurrency or the plugin. Do not build the playerctl v2 feature (`docs/05-future-v2-playerctl.md`) until plugin v1 works.

## What this is

A Go CLI that takes a Spotify track, album or playlist URL, gets metadata from the Spotify Web API, finds the matching audio on YouTube, downloads it with `yt-dlp`, transcodes it with `ffmpeg`, then tags and saves it. It behaves like spotdl but is a native binary.

## Architecture rules (non-negotiable)

- **Never reimplement YouTube extraction.** Shell out to the `yt-dlp` binary for search and download, and to `ffmpeg` for transcoding and embedding cover art. Go handles metadata resolution, match scoring, orchestration, concurrency and the CLI.
- **The engine has no Omarchy dependency.** `spotify-dl` must work the same from any terminal on any Linux box. The Omarchy/Quickshell plugin (`omarchy-plugin-spotify-dl`, a separate repo built later) only runs this binary and reads its NDJSON output. It never imports engine internals.

## Layout (planned)

- `internal/spotify`: Client Credentials OAuth2 using `client_id`/`client_secret` from `~/.config/spotify-dl/config.toml`. The user supplies this file; the tool never generates it. Also URL parsing and paginated album/playlist fetches.
- `internal/youtube`: `yt-dlp "ytsearch5:{artists} - {title}" --dump-json --no-download`, then scores the candidates. Rejects any candidate whose duration is more than 10s from Spotify's `duration_ms`, prefers official or topic channels, and penalizes live/cover/remix unless the Spotify title has the same word.
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
