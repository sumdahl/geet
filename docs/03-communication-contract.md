# Communication contract (`--json` / NDJSON)

This is the interface the plugin (`04-plugin-spec.md`) consumes — treat it
as a stable API even before the plugin exists.

- stderr is for humans: animated bars in a terminal, plain lines otherwise
  (setting `progress`: `auto`|`always`|`never`; `auto` never animates with
  `--json`). Never parse stderr.
- Every subcommand supports `--json`: on success, NDJSON (one JSON object
  per line) on stdout for multi-track/streaming operations; nothing else
  goes to stdout. Human-readable progress/logs go to stderr only, so the
  two never mix.
- Before any track starts, resolving the link emits `reading` events
  (no `track`; `step`, `index`, `total`) so a long playlist shows progress.
  Consumers must ignore stages and fields they don't know.
- `spotify-dl download <url> --json` → one NDJSON line per track as it
  crosses each stage: `resolved → downloading → tagging → done`, or
  `failed` at any point. A track whose file already exists goes straight to
  `done` with `"skipped": true` (unless `--overwrite`).
  ```json
  {"track":"Gunna - fukumean","stage":"done","spotify_id":"4rXLjWdF2ZZpXCVTfWcshS",
   "index":1,"total":18,"youtube_url":"https://www.youtube.com/watch?v=l21wGxlWwPw",
   "path":"/home/u/Music/Gunna/fukumean - Gunna.opus",
   "warning":"320k requested but YouTube's source is 152k Opus: the file will be bigger, not better"}
  ```
  | field | when |
  |---|---|
  | `track` | always ("Artists - Title"); empty on a fatal event |
  | `stage` | always: `resolved`, `downloading`, `tagging`, `done`, `failed` |
  | `error` | `failed` only |
  | `fatal` | `true` when the whole run stopped (bad URL, missing tool, Ctrl+C); exit code is then 2 |
  | `spotify_id`, `index` (1-based), `total` | every per-track event |
  | `path` | every per-track event: where the file is/will be |
  | `youtube_url` | from `resolved` on |
  | `skipped` | `done` for an existing file, or a duplicate with `duplicates = skip` |
  | `duplicate_of` | `done` when the track wasn't downloaded because this file (from another playlist, or the same recording on another release) already had it |
  | `linked` | with `duplicate_of`: `true` = hard link (no extra disk space), absent = copy |
  | `warning` | `done`, when the requested format/bitrate can't beat YouTube's source (FLAC, or a bitrate >10% above it) |
  | `step` | `reading` only: `index` (first run: scanning existing downloads), `spotify` (reading a playlist's tracks), `tags` (Deezer lookups); `index`/`total` count items done |
  | `progress` | repeated `downloading` events, one per 10% step: `0.1` … `1` (the first `downloading` event has none) |

  Tracks are processed concurrently, so events of different tracks
  interleave: key them by `index` (or `spotify_id`), not by line order.
  Fields are only ever added, never renamed or removed; consumers must
  ignore unknown fields. Absent optional fields mean false/empty.
- Exit codes:
  - `0` — all tracks succeeded
  - `1` — partial failure (some tracks failed; check the NDJSON for which)
  - `2` — fatal (bad URL, auth failure, yt-dlp/ffmpeg missing)
- `spotify-dl watch --json` (the clipboard daemon) streams the same NDJSON
  shape continuously, one line per event, so a consumer can keep the
  process open and read a pipe instead of polling.

## Configuration for consumers
The plugin configures the engine without touching its internals:
- Per-invocation: pass flags (`--output`, `--format`, …) or `SPOTIFY_DL_*`
  environment variables; both override the config file.
- Persistent: write `~/.config/spotify-dl/config.toml` (TOML, keys as listed
  by `spotify-dl config settings --json`).
- Discovery: `spotify-dl config settings --json` returns an array of
  `{"key","flag","env","type","default","value","secret","usage"}`, with
  `type` one of `string|int|duration|list`; `spotify-dl config --json`
  returns the effective config as one object. Secrets come back as
  `"<redacted>"`.
