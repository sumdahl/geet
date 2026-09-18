# Communication contract (`--json` / NDJSON)

This is the interface the plugin (`04-plugin-spec.md`) consumes — treat it
as a stable API even before the plugin exists.

- Every subcommand supports `--json`: on success, NDJSON (one JSON object
  per line) on stdout for multi-track/streaming operations; nothing else
  goes to stdout. Human-readable progress/logs go to stderr only, so the
  two never mix.
- `spotify-dl download <url> --json` → one NDJSON line per track as it
  crosses each pipeline stage:
  `{"track":"...","stage":"resolved|downloading|tagging|done|failed","error":"..."}`
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
