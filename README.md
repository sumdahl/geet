# spotify-dl

Download Spotify tracks, albums and playlists as tagged audio files.

`spotify-dl` reads a link's metadata from Spotify, finds the matching upload on YouTube, downloads it with `yt-dlp`, and uses `ffmpeg` to convert it, tag it and embed the album cover. A 50-track playlist takes about a minute.

- **No Spotify account or API keys needed.** Metadata comes from Spotify's public pages. The official Web API (which needs Premium) is used only if you configure credentials.
- **Careful matching.** It avoids live versions, covers, remixes and sped-up uploads, and videos with long intros. It handles censored titles (`Ni**as`), accented names (`JAŸ-Z`) and non-Latin scripts.
- **Proper files.** Title, artists, album, album artist, track and disc number, year, ISRC and the 640 px cover are written into the file itself.
- **Your choice of quality.** Opus at YouTube's original quality (no re-encode) by default, or MP3/FLAC at a bitrate you pick. It warns you when a request can't be better than the source.
- **Never downloads twice.** Existing files are skipped. A song you already have in another playlist is hard-linked, using no bandwidth and no extra disk space.
- **Concurrent and resumable.** Searches, downloads and tagging run in parallel. Ctrl+C leaves no partial files, and a re-run continues where it stopped.
- **Scriptable.** `--json` streams NDJSON progress events. Every setting is a config key, a flag and an environment variable. It's built to be the backend of an [Omarchy](https://omarchy.org) desktop plugin.

```
$ spotify-dl download "https://open.spotify.com/playlist/37i9dQZF1E38GaNXgXwvL4"
playlist "Daily Mix 1": 50 track(s) → /home/you/Music/daily-mix-1
[ 1/50] Kendrick Lamar - Hood Politics             ✓ saved
[ 3/50] Post Malone - Circles                      ⧉ linked from todays-top-hits (no download)
[ 7/50] Eminem - Sing For The Moment               ━━━━━━━━━━━━──────────── ⠼ downloading  54%  1.1 MiB / 2.0 MiB
[ 8/50] Kendrick Lamar - The Art of Peer Pressure  ━━━━━━━━━━━━━━━━━━━━━━━━ ⠦ tagging & cover art
[ 9/50] Drake - Plot Twist                         ──────────────────────── · queued for download
```

## Requirements

- Linux (other Unix-likes probably work too, but only Linux is tested)
- Go 1.27+ to build
- [`yt-dlp`](https://github.com/yt-dlp/yt-dlp), `ffmpeg` and `ffprobe` on `PATH`. The paths are configurable.

```sh
sudo pacman -S yt-dlp ffmpeg        # Arch / Omarchy
```

## Install

```sh
git clone https://github.com/sumdahl/spotify-dl
cd spotify-dl
go install ./cmd/spotify-dl         # installs to $(go env GOPATH)/bin
```

Or run from the source tree without installing: `go run ./cmd/spotify-dl download <url>`.

## Usage

```sh
spotify-dl download <spotify-url>        # a track, album or playlist
spotify-dl config                        # effective configuration (TOML; --json for JSON)
spotify-dl config path                   # where the config file lives
spotify-dl config settings               # every setting with its flag and env variable
spotify-dl version
```

Accepted links:
- `https://open.spotify.com/{track|album|playlist}/<id>`, with or without `?si=…` or a `/intl-xx/` prefix
- `spotify:{track|album|playlist}:<id>`

Share links from the Spotify app work as copied. Artist links aren't supported.

Examples:

```sh
spotify-dl download "<url>" --format mp3 --bitrate 320k    # MP3 for devices without Opus
spotify-dl download "<url>" --output ~/Downloads/music     # somewhere other than ~/Music
spotify-dl download "<url>" --jobs 8                       # more parallel downloads
spotify-dl download "<url>" --json 2>/dev/null | jq .      # machine-readable progress
spotify-dl download "<url>" -v                             # debug logs, including every YouTube candidate's score
```

Exit codes: `0` all tracks succeeded, `1` some failed (the others were saved), `2` fatal (bad link, missing tool, interrupted).

### Where files go

| Link | Saved as |
|---|---|
| Track or album | `~/Music/<title> - <artists>.opus` |
| Playlist | `~/Music/<playlist-name>/<title> - <artists>.opus` |

Playlist folder names have no spaces: `Today’s Top Hits` becomes `todays-top-hits`, and `chill 🔥 vibes!! 2024` becomes `chill-vibes-2024`. Letters of every script are kept. The letter case can be `lower`, `capitalize` or `title`.

`output_template` changes the layout. For example, `{album_artist}/{album}/{track} {title}` gives an album-folder library. Placeholders: `{title} {artist} {artists} {album} {album_artist} {track} {disc} {year} {isrc} {spotify_id}`. Values are sanitized, so metadata can never create extra folders or escape the output directory.

### Audio quality

YouTube's best audio is about 130–160 kbps Opus (256 kbps AAC with YouTube Premium cookies). Everything downloaded is at most that good.

| Choice | Result |
|---|---|
| `--format opus` (default, no bitrate) | YouTube's Opus stream copied as-is. Best quality, no generation loss. |
| `--format opus --bitrate 96k` | Re-encoded, smaller |
| `--format mp3` | VBR V0 (~245 kbps) |
| `--format mp3 --bitrate 320k` | Works, with a warning: the file gets bigger but not better |
| `--format flac` | Works, with a warning: lossless packaging of lossy audio |

## Configuration

Every setting can be given in three ways. When the same setting is set in more than one place, the flag wins over the environment variable, which wins over the config file:

1. **Flag:** `--youtube-search-results 8`
2. **Environment variable:** `SPOTIFY_DL_YOUTUBE_SEARCH_RESULTS=8`
3. **Config file:** `~/.config/spotify-dl/config.toml` (or `$SPOTIFY_DL_CONFIG`, or `--config`). The file is optional, and an unknown key in it is an error.

```toml
output = "~/Music"
format = "opus"
jobs = 8
playlist_folder_case = "title"

[youtube]
cookies_from_browser = "firefox"   # for age-restricted videos

[spotify]                          # optional: official Web API (needs Premium)
client_id = "..."
client_secret = "..."
```

| Key | Flag | Type | Default | What it does |
|---|---|---|---|---|
| `output` | `--output` | string | `~/Music` | Music library directory |
| `output_template` | `--output-template` | string | `{title} - {artists}` | File path under `output`, without extension |
| `playlist_folder` | `--playlist-folder` | bool | `true` | Put a playlist's tracks in a folder named after it |
| `playlist_folder_case` | `--playlist-folder-case` | string | `lower` | `lower`, `capitalize` or `title` |
| `format` | `--format` | string | `opus` | `opus`, `flac` or `mp3` |
| `bitrate` | `--bitrate` | string | *(best)* | e.g. `320k` |
| `overwrite` | `--overwrite` | bool | `false` | Re-download tracks whose file already exists |
| `duplicates` | `--duplicates` | string | `link` | A track already downloaded elsewhere: `link` (hard link), `copy`, `skip` or `download` |
| `index_path` | `--index-path` | string | `$XDG_DATA_HOME/spotify-dl/index.json` | The download index used for duplicates |
| `progress` | `--progress` | string | `auto` | Animated bars: `auto` (in a terminal, not with `--json`), `always` or `never` |
| `download_retries` | `--download-retries` | int | `2` | Extra attempts when YouTube refuses a download |
| `jobs` | `--jobs` | int | `4` | Parallel downloads |
| `resolve_jobs` | `--resolve-jobs` | int | `8` | Parallel metadata reads and YouTube searches |
| `spotify.client_id` | `--spotify-client-id` | string | | Optional Web API credentials (Premium only) |
| `spotify.client_secret` | `--spotify-client-secret` | string | | |
| `youtube.search_query` | `--youtube-search-query` | string | `{artists} - {title}` | YouTube search text |
| `youtube.fallback_query` | `--youtube-fallback-query` | string | `{artists} - {title} audio` | Second search when nothing matches; empty disables it |
| `youtube.search_results` | `--youtube-search-results` | int | `5` | Results scored per search |
| `youtube.max_duration_diff` | `--youtube-max-duration-diff` | duration | `10s` | Reject uploads whose length differs more than this |
| `youtube.cookies_file` | `--youtube-cookies-file` | string | | Netscape cookies file for yt-dlp |
| `youtube.cookies_from_browser` | `--youtube-cookies-from-browser` | string | | Browser yt-dlp reads cookies from |
| `youtube.extra_args` | `--youtube-extra-args` | list | | Extra yt-dlp arguments, space-separated |
| `tools.yt_dlp` | `--tools-yt-dlp` | string | `yt-dlp` | Executables |
| `tools.ffmpeg` | `--tools-ffmpeg` | string | `ffmpeg` | |
| `tools.ffprobe` | `--tools-ffprobe` | string | `ffprobe` | |

`spotify-dl config settings --json` prints this table as JSON (key, flag, env variable, type, default, current value, description), so tools can build a settings UI without hard-coding it.

## JSON output

With `--json`, stdout carries only NDJSON: one event per line, as each track moves through `resolved → downloading → tagging → done`, or `failed`. Human-readable output goes to stderr only.

```json
{"track":"Gunna - fukumean","stage":"done","spotify_id":"4rXLjWdF2ZZpXCVTfWcshS","index":1,"total":1,
 "youtube_url":"https://www.youtube.com/watch?v=l21wGxlWwPw","path":"/home/you/Music/fukumean - Gunna.opus"}
```

`reading` events report progress before any track starts. Repeated `downloading` events carry a `progress` from 0.1 to 1. Tracks run concurrently, so key events by `index`. The full schema and its compatibility rules are in [docs/03-communication-contract.md](docs/03-communication-contract.md).

## Architecture

### Design rules

1. **Don't reimplement YouTube.** Stream extraction changes constantly, and keeping up with it is yt-dlp's job. This program runs the `yt-dlp` and `ffmpeg` binaries. The Go code does metadata, matching, orchestration, concurrency and the CLI.
2. **The engine stands alone.** It has no desktop dependencies and works the same in any terminal. The planned Omarchy plugin is a thin client: it runs the binary and reads the NDJSON stream, and never imports engine internals.

### Data flow

```
                       spotify-dl download <url>
                                  │
                        spotify.ParseURL(url)
                                  │
           ┌──────────────────────┴───────────────────────┐
           │ metadata                                      │
           │   credentials? ── yes ──▶ spotify.API (Web API)│
           │        │ no                                   │
           │        ▼                                      │
           │   spotify.Web: public embed/track pages       │
           │   (playlist tracks read in parallel)          │
           └──────────────────────┬───────────────────────┘
                                  │ spotify.Collection {name, tracks}
                                  ▼
   ┌──────────────────────────── pipeline ─────────────────────────────┐
   │                                                                     │
   │  resolve ×8 ──────────▶ download ×4 ──────────▶ tag ×2 ───▶ done   │
   │  · file exists? skip     · yt-dlp bestaudio      · cover art        │
   │  · Deezer: ISRC, disc    · retries on 403        · ffmpeg: convert, │
   │  · index: link a dup     · live progress           tag, embed       │
   │  · YouTube search+score                          · rename into place│
   │                                                  · record in index  │
   │  bounded channels: a slow stage backpressures the one before it     │
   │  one context: a missing tool or Ctrl+C stops every stage            │
   └─────────────────────────────────────────────────────────────────────┘
                                  │
             events ──▶ reporter ─┬─▶ stderr: mpb bars (terminal) or plain lines
                                  └─▶ stdout: NDJSON (--json)
```

### Packages

| Package | Role |
|---|---|
| `cmd/spotify-dl` | CLI: subcommands, flags generated from the settings list, the per-track stages (`download.go`), the stderr display (`ui.go`) |
| `internal/config` | Settings, defined once in `Config.Settings()`. Each becomes a TOML key, a `--flag` and a `SPOTIFY_DL_*` variable, and is validated. |
| `internal/spotify` | Link parsing. `Web` scrapes the public pages (keyless); `API` uses the official Web API. Both return a `Collection`. |
| `internal/deezer` | Fills in what the public pages lack (ISRC, disc and track numbers) from Deezer's keyless API. Best effort: album match first, then per-track search. |
| `internal/youtube` | Builds the query and scores candidates from yt-dlp's flat search. Keeps every candidate's score or rejection reason for debugging. |
| `internal/ytdlp` | Shared yt-dlp runner (cookies, extra args, streaming output, error reasons) |
| `internal/download` | Fetches the best audio stream unmodified, reporting progress |
| `internal/audio` | One ffmpeg pass: copy or convert, write tags, embed the cover. Also the quality warning. |
| `internal/library` | Output path from the template, sanitizing, playlist folder names |
| `internal/index` | Remembers every saved file by Spotify ID and ISRC, so duplicates are linked instead of downloaded. Can rebuild itself from file tags. |
| `internal/pipeline` | Generic staged worker pools with bounded channels and shared cancellation |
| `internal/textnorm` | Title and name normalization shared by matching: accent folding, censored-word wildcards, Devanagari-safe |

### Why it's built this way

- **Keyless metadata.** The Web API now requires the app owner to have Spotify Premium. Spotify's embed pages carry the track list, durations and cover in their `__NEXT_DATA__` JSON. The track page's `music:*` meta tags (served only to link-preview crawlers) carry the album and track number. What's still missing, ISRC and disc number, comes from Deezer. Deezer filters search results by country, so ISRCs are sometimes missing; the other tags are unaffected. Public playlist pages list at most 100 tracks.
- **Matching** (`internal/youtube/score.go`):
  - Candidates are rejected if their length differs by more than 10 s, less than 60% of the song's base title matches, or no artist is named.
  - Scores reward the official, "Topic" or VEVO channel, "audio" uploads, closeness in length, and search rank.
  - Variant words (live, cover, remix, sped up, …) are penalized unless the Spotify title has them too.
  - If nothing matches, a second search asks for the "audio" upload, which catches official videos whose intro pushes them past the length limit.
  - Real searches that once picked wrong are kept as test fixtures.
- **One ffmpeg pass per track.** Download only fetches the raw stream, because yt-dlp's own conversion silently ignores the requested bitrate when the codecs already match. Opus has no picture stream, so its cover goes in a `METADATA_BLOCK_PICTURE` tag. The tags travel in an `FFMETADATA` file because a base64 cover exceeds Linux's 128 KB per-argument limit.
- **Atomic, resumable output.** Each track is built in a hidden work directory inside the library and renamed into place, so a file either exists complete or not at all. The index is saved after every track.
- **Duplicates by hard link.** Two names point to one file, so no extra space is used and deleting one leaves the other intact. It falls back to a copy across filesystems. Every file carries its Spotify URL in the comment tag, which lets the index be rebuilt by scanning a library.

## Development

```sh
go test -race ./...                                           # everything (audio tests need ffmpeg; skipped without it)
go test ./internal/youtube -run TestBestOnRealSearches        # one test
go test ./internal/library -run '^$' -fuzz FuzzPath -fuzztime 30s   # fuzz: also FuzzText, FuzzParseURL
go vet ./... && gofmt -l .
```

- Tests never touch the network. Spotify, Deezer and YouTube responses are fixtures under `testdata/`, served by `httptest` or replayed by a fake `yt-dlp` script.
- **When a real search picks the wrong upload,** capture it (`yt-dlp "ytsearch5:<query>" --flat-playlist --dump-json > internal/youtube/testdata/<name>.ndjson`) and add a case to `TestBestOnRealSearches` before changing any weights.
- The design docs in [`docs/`](docs/) are the spec and roadmap. Next come the clipboard `watch` daemon and the Omarchy plugin.

## Legal

This tool is for personal use with music you have the right to download. You're responsible for complying with YouTube's and Spotify's terms of service and with copyright law where you live.
