# spotify-dl — Project Overview

## Goal
Given a Spotify track/album/playlist URL, fetch accurate metadata from
Spotify's Web API, find the best-matching audio on YouTube, download it via
yt-dlp, transcode with ffmpeg, tag the file with correct title/artist/album/
track-number/year/album-art, and save it to a configured music library path.

spotdl's behavior, but a fast native engine binary, cleanly separated from
its Omarchy/Quickshell (Omarchy 4 "Quattro") plugin integration.

## Non-negotiable architecture decisions

1. **Don't reimplement YouTube extraction.** YouTube's stream extraction/
   signature deciphering is what yt-dlp exists to maintain against YouTube's
   constant changes — reinventing it in Go is a maintenance trap. Shell out
   to the `yt-dlp` binary for search/extraction/download, and to `ffmpeg` for
   transcoding/tag embedding. Go's job is metadata resolution, match scoring,
   orchestration, concurrency, CLI/UX, and (later) Omarchy integration — not
   video extraction.

2. **Engine first, plugin is a thin client.** Build `spotify-dl` as a fully
   standalone, general-purpose CLI binary with zero Omarchy/Quickshell
   dependencies — it must work identically from any terminal on any Linux
   box. The Omarchy plugin is a separate, later layer that only shells out
   to this binary and reads its output; it never links against the engine's
   internals directly.

## Repos / directories

- **Engine**: `spotify-dl/` — no `omarchy-` prefix, since it has no Omarchy
  dependency. GitHub: `sumdahl/spotify-dl`. Local: `~/personal/spotify-dl`.
- **Plugin** (built later, v1): `omarchy-plugin-spotify-dl/` — matches the
  naming of the existing `omarchy-plugin-nepse` / `omarchy-plugin-media`
  repos. Separate repo.
- Decide monorepo vs. permanently-separate repos once the plugin work
  actually starts and it's clear whether it needs its own release cycle.

## Doc index
- `01-engine-spec.md` — components, CLI surface, config
- `02-concurrency-pipeline.md` — staged goroutine pipeline for playlists
- `03-communication-contract.md` — the `--json`/NDJSON interface the plugin
  will consume
- `04-plugin-spec.md` — Omarchy/Quickshell plugin (v1, built after the
  engine is done)
- `05-future-v2-playerctl.md` — future scope, do not build yet
- `06-code-style-testing.md` — Go style rules and test requirements
- `07-roadmap.md` — deliverables in order, with review checkpoints
