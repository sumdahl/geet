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
- When a playlist is larger than Spotify's public page shows, a `reading`
  event carries a `warning` naming the counts and the `--tracks` command.
- Before any track starts, resolving the link emits `reading` events
  (no `track`; `step`, `index`, `total`) so a long playlist shows progress.
  Consumers must ignore stages and fields they don't know.
- `geet download <url> --json` → one NDJSON line per track as it
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
- `geet watch --json` (the clipboard daemon) streams the same NDJSON
  shape continuously, one line per event, so a consumer can keep the
  process open and read a pipe instead of polling. Each copied link is a
  **job**, downloaded one at a time in copy order:
  - `queued`: a link was copied: `{"stage":"queued","job":1,"source":"<link>"}`.
    Several song links copied together are one job, with the first as `source`.
  - Then the usual `reading`/`resolved`/…/`done`/`failed` events of that
    job, each also carrying its `job` and `source`. `index`/`total` count
    within the job.
  - `finished`: exactly one per `queued` job, when it ends:
    ```json
    {"track":"","stage":"finished","job":1,"source":"https://open.spotify.com/album/…",
     "name":"Emotion (Deluxe)","total":15,"path":"/home/u/Music",
     "counts":{"saved":14,"existing":0,"failed":1}}
    ```
    | field | meaning |
    |---|---|
    | `name` | the album or playlist name, or "Artists - Title" for one song; empty if the link couldn't be read |
    | `total` | tracks in the job |
    | `path` | the folder the files went to |
    | `counts` | `saved` (downloaded), `existing` (already in the library, including reused duplicates), `failed` |
    | `error` | the job as a whole failed: an unreadable link, `interrupted` (the daemon was stopped mid-job), or the queue was full. `counts` still says what finished before. |

    A failed job never stops the daemon, and never sets `fatal`.
  - `fatal: true` appears only if the daemon itself can't go on (no
    `wl-paste`, no Wayland session, `yt-dlp`/`ffmpeg` missing at start),
    with exit code 2. Stopped by SIGINT/SIGTERM, it exits 0 after a
    `finished` event (`"error":"interrupted"`) for any job in progress.
    Jobs still queued then get no `finished`: the process exit ends them.
  - With `watch.notify` (default on) the daemon also shows desktop
    notifications. A consumer that shows its own should pass
    `--watch-notify=false`.

## Configuration for consumers
The plugin configures the engine without touching its internals:
- Per-invocation: pass flags (`--output`, `--format`, …) or `GEET_*`
  environment variables; both override the config file.
- Persistent: write `~/.config/geet/config.toml` (TOML, keys as listed
  by `geet config settings --json`).
- Discovery: `geet config settings --json` returns an array of
  `{"key","flag","env","type","default","value","secret","usage"}`, with
  `type` one of `string|int|duration|list`; `geet config --json`
  returns the effective config as one object. Secrets come back as
  `"<redacted>"`.

## Search (`geet search <words…> --json`)
Prints one JSON array (not NDJSON) of ranked results and downloads nothing:
```json
[{"index":1,"ref":"itunes:1499378607","title":"Blinding Lights","artists":["The Weeknd"],
  "album":"After Hours","album_artist":"The Weeknd","year":2019,"duration_ms":200046,
  "cover_url":"https://…/600x600bb.jpg","url":"https://music.apple.com/…","editions":7,
  "label":"Blinding Lights — The Weeknd · After Hours (2019) 3:20"}]
```
- Show `label` (or build a row from the fields) in the plugin's own menu. Then run `geet download <ref> --json` for each pick, which emits the usual NDJSON events.
- `clean: true` marks a clean edit (its title may be censored).
- `geet download` accepts `itunes:<id>` refs and Apple Music song links.
- `geet search <words…> --pick 1,3` downloads without a menu.

## Health (`geet doctor --json`)
One JSON object, for the plugin to show a status or to explain failing downloads:
```json
{"healthy":true,"version":"geet v0.1.0 (330f3ac, 2026-09-18)",
 "checks":[{"group":"Tools","name":"yt-dlp","status":"ok","detail":"2026.08.19 (30 days old)"},
           {"group":"Services","name":"YouTube","status":"fail","detail":"…","fix":"YouTube is limiting this IP: …","ms":1480}]}
```
- `status` is one of `ok`, `warn` (works, but needs attention), `fail` (downloads will fail until fixed) and `skip` (optional and absent, or not checked).
- `healthy` is false only when some check has status `fail`. The exit code matches: 0 when healthy, 1 otherwise.
- `--offline` skips the Services group, which then has a single `skip` entry.
