# geet

*geet* (गीत) is Nepali and Hindi for "song". It downloads Spotify tracks, albums and playlists as tagged audio files.

`geet` reads a link's metadata from Spotify, finds the matching upload on YouTube, downloads it with `yt-dlp`, and uses `ffmpeg` to convert it, tag it and embed the album cover. A 50-track playlist takes about a minute.

- **No Spotify account or API keys needed.** Metadata comes from Spotify's public pages. The official Web API (which needs Premium) is used only if you configure credentials.
- **Careful matching.** It avoids live versions, covers, remixes and sped-up uploads, and videos with long intros. It handles censored titles (`Ni**as`), accented names (`JAŸ-Z`) and non-Latin scripts.
- **Proper files.** Title, artists, album, album artist, track and disc number, year, ISRC and the 640 px cover are written into the file itself.
- **Your choice of quality.** Opus at YouTube's original quality (no re-encode) by default, or MP3/FLAC at a bitrate you pick. It warns you when a request can't be better than the source.
- **Never downloads twice.** Existing files are skipped. A song you already have in another playlist is hard-linked, using no bandwidth and no extra disk space.
- **Concurrent and resumable.** Searches, downloads and tagging run in parallel. Ctrl+C leaves no partial files, and a re-run continues where it stopped.
- **Scriptable.** `--json` streams NDJSON progress events. Every setting is a config key, a flag and an environment variable. It's built to be the backend of an [Omarchy](https://omarchy.org) desktop plugin.

```
$ geet download "https://open.spotify.com/playlist/37i9dQZF1E38GaNXgXwvL4"
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

**Download a release binary** (Linux x86-64 or arm64, a static binary that needs nothing but `yt-dlp` and `ffmpeg`) from [Releases](https://github.com/sumdahl/geet/releases):

```sh
curl -L https://github.com/sumdahl/geet/releases/latest/download/geet-linux-amd64 -o ~/.local/bin/geet
chmod +x ~/.local/bin/geet
geet doctor         # check that everything geet needs is in place
```

**Or build from source** (Go 1.27+):

```sh
git clone https://github.com/sumdahl/geet
cd geet
go install ./cmd/geet         # installs to $(go env GOPATH)/bin
```

Or build a binary yourself, with the release version stamped in:

```sh
go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty)" -o ~/.local/bin/geet ./cmd/geet
geet version        # geet v0.2.0 (<commit>, <date>)
```

Or run it from the source tree without installing: `go run ./cmd/geet download <url>`.

Releases follow [Semantic Versioning](https://semver.org/), and the changes are listed in [CHANGELOG.md](CHANGELOG.md).

## Usage

```sh
geet download <spotify-url>        # a track, album or playlist
geet search <words…>               # find a song by name, pick it, download it
geet doctor                        # health check: tools, setup, services, and how to fix problems
geet config                        # effective configuration (TOML; --json for JSON)
geet config path                   # where the config file lives
geet config settings               # every setting with its flag and env variable
geet version
```

Accepted links:
- `https://open.spotify.com/{track|album|playlist}/<id>`, with or without `?si=…` or a `/intl-xx/` prefix
- `spotify:{track|album|playlist}:<id>`

Share links from the Spotify app work as copied. Artist links aren't supported.

Put links in quotes: shared links contain `&` (`…?si=…&utm_source=copy_link`), and an unquoted `&` makes the shell run geet in the background, where Ctrl+C can't reach it and the progress display breaks.

Examples:

```sh
geet download "<url>" --format mp3 --bitrate 320k    # MP3 for devices without Opus
geet download "<url>" --output ~/Downloads/music     # somewhere other than ~/Music
geet download "<url>" --jobs 8                       # more parallel downloads
geet download "<url>" --json 2>/dev/null | jq .      # machine-readable progress
geet download "<url>" -v                             # debug logs, including every YouTube candidate's score
```

Exit codes: `0` all tracks succeeded, `1` some failed (the others were saved), `2` fatal (bad link, missing tool, interrupted).

### Playlists over 100 songs

Spotify's public page lists only a playlist's first 100 songs. geet notices and says so:

```
warning: Spotify's public page shows only 100 of the 201 songs in this playlist.
To download all of them: in the Spotify app open the playlist, press Ctrl+A then Ctrl+C, then run:
  wl-paste | geet download "https://open.spotify.com/playlist/…" --tracks -
```

Ctrl+A then Ctrl+C in the Spotify desktop app copies a link for every song, and `--tracks -` reads them from the clipboard. The playlist link only names the folder. Songs already downloaded are skipped, so re-running this after a normal download fetches just the missing ones. `--tracks` also takes a file with one link per line (`#` starts a comment), and it works without a playlist link, saving into `output` directly.

### Progress and speed

In a terminal, each song gets an animated bar. Big runs (more than 8 songs) switch to a compact display: bars only for songs being downloaded or tagged, and one summary line for the rest:

```
[14/49] Olivia Dean - Man I Need          ━━━━━━━━━━━━━━━━──────── ⠋ downloading  65%  2.0 MiB / 3.0 MiB
[11/49] Ella Langley - Choosin' Texas     ━━━━━━━━━━━━━━━━━━━━━━━━ ⠋ tagging & cover art
⠙ 14 finding on YouTube · 11 queued · 16 downloading · 2 tagging · 12/49 done
```

Songs download 4 at a time by default. On a good connection, more is faster: 16 downloads 50 songs in about 40 seconds. Set it once in the config:

```toml
jobs = 16          # parallel downloads
resolve_jobs = 24  # parallel metadata reads and YouTube searches (keep it above jobs)
```

Past your bandwidth, more jobs just split it.

**"YouTube wants a sign-in to confirm you're not a bot"** means YouTube is limiting your IP, usually after many downloads in a short time. Waiting an hour and lowering `jobs` helps. The lasting fix is to let yt-dlp use your browser's YouTube login:

```toml
[youtube]
cookies_from_browser = "chromium"   # or firefox, brave, …: one where you're signed in to YouTube
```

`geet doctor` detects this, because it checks that audio downloads work, not just search. Interrupting with Ctrl+C is always safe: no partial files are left, and re-running the same command continues where it stopped.

### Search

Don't have a link? Search by name, pick from a menu, and it downloads like any track:

```
$ geet search blinding lights
╭──────────────────────────────────────────────────────────────╮
│ Search ›                                                     │
│ ▌ Blinding Lights — The Weeknd · After Hours (2019) 3:20     │
│   Blinding Lights — KIDZ BOP Kids · KIDZ BOP 2021 (2020) 2:59│
│   Blinding Lights — Teddy Swims (2020) 3:34                  │
│   15/15 ─────────────────────────────────────────────────────│
│ Tab: pick several · Enter: choose · Esc: cancel              │
╰──────────────────────────────────────────────────────────────╯
Selected:
  • Blinding Lights — The Weeknd · After Hours (2019) 3:20
Download 1 song? [Y/n]
```

- Songs come from the iTunes catalog: no key, any country's store (`search.country`, default `US`), and Nepali and other regional music included.
- The raw catalog order puts covers above originals, so geet merges album editions of the same recording and re-ranks. Covers, remixes, instrumentals, slowed and lullaby versions sink, and the original (listed on the most editions) rises.
- The menu is [fzf](https://github.com/junegunn/fzf), which ships with Omarchy: type to filter (`weeknd` narrows to that artist), Tab to pick several songs, which then download in parallel. Without fzf you get a numbered list (`1`, `1 3` or `2-4`).
- Before downloading, geet lists your picks and asks `Download N songs? [Y/n]`, so a stray Enter never starts a download. Skip the question with `-y` / `--yes`, or set `search.confirm = false`. `--pick` never asks.
- To get a specific artist's version, add the artist to the search: `geet search blinding lights weeknd`.
- Scripting: `geet search <words> --pick 1` chooses without a menu, and `geet search <words> --json` lists results (each with a `ref`) without downloading. `geet download itunes:<id>` and Apple Music song links download directly.
- Limits:
  - Apple's public catalog lists some explicit songs only as clean edits, whose titles are censored too ("umean" for Gunna's "fukumean"). These are marked `(clean)`, and matching still finds the right upload.
  - A few labels' catalogs aren't searchable from some regions. For those, use the Spotify link.

### Health check

When downloads start failing, run `geet doctor`. It checks everything geet depends on outside its own code, in about 2 seconds, and says how to fix whatever is broken:

```
$ geet doctor
Tools
  ✓ yt-dlp     2026.08.19 (30 days old)
  ✓ ffmpeg     9.0.1 (opus ✓ mp3 ✓ flac ✓)
  ✓ ffprobe    9.0.1
  ✓ fzf        0.74.3 (search menu)

Setup
  ✓ config     ~/.config/geet/config.toml · opus · jobs 16, resolve_jobs 24
  ✓ library    ~/Music writable, 291.1 GB free
  ✓ index      219 songs known

Services
  ✓ Spotify    read "Monkeys Spinning Monkeys" by Kevin MacLeod · 1.4s
  ✓ YouTube    search works · 1.5s
  ✓ Deezer     reachable (searches as NP: some label catalogs are regional) · 0.4s
  ✓ iTunes     reachable (US store, found "Blinding Lights") · 0.2s

All good.
```

- **Tools:** an old yt-dlp is flagged (YouTube breaks old versions), and a missing ffmpeg encoder for your format is an error.
- **Setup:** config errors come with the exact line, the library folder is tested for writing and free space, and the index is checked for corruption.
- **Services:** each is exercised with one small real request (no audio is downloaded). That catches Spotify changing its pages or YouTube blocking your IP.
- `--offline` skips the network, and `--json` is for the plugin. The exit code is `0` when nothing failed and `1` when something needs fixing.

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
2. **Environment variable:** `GEET_YOUTUBE_SEARCH_RESULTS=8`
3. **Config file:** `~/.config/geet/config.toml` (or `$GEET_CONFIG`, or `--config`). The file is optional, and an unknown key in it is an error.

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
| `index_path` | `--index-path` | string | `$XDG_DATA_HOME/geet/index.json` | The download index used for duplicates |
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
| `search.country` | `--search-country` | string | `US` | iTunes store `geet search` looks in |
| `search.limit` | `--search-limit` | int | `15` | Results offered to pick from |
| `search.picker` | `--search-picker` | string | `auto` | `auto` (fzf if installed), `fzf` or `list` |
| `search.confirm` | `--search-confirm` | bool | `true` | Ask before downloading menu picks |
| `tools.yt_dlp` | `--tools-yt-dlp` | string | `yt-dlp` | Executables |
| `tools.ffmpeg` | `--tools-ffmpeg` | string | `ffmpeg` | |
| `tools.ffprobe` | `--tools-ffprobe` | string | `ffprobe` | |

`geet config settings --json` prints this table as JSON (key, flag, env variable, type, default, current value, description), so tools can build a settings UI without hard-coding it.

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
                       geet download <url>
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
| `cmd/geet` | CLI: subcommands, flags generated from the settings list, the per-track stages (`download.go`), search and the fzf/numbered picker (`search.go`, `pick.go`), the stderr display (`ui.go`) |
| `internal/config` | Settings, defined once in `Config.Settings()`. Each becomes a TOML key, a `--flag` and a `GEET_*` variable, and is validated. |
| `internal/spotify` | Link parsing. `Web` scrapes the public pages (keyless); `API` uses the official Web API. Both return a `Collection`. |
| `internal/deezer` | Fills in what the public pages lack (ISRC, disc and track numbers) from Deezer's keyless API. Best effort: album match first, then per-track search. |
| `internal/itunes` | Keyless catalog search and lookup for `geet search`. Merges album editions and ranks originals above covers. |
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

## License

[MIT](LICENSE) © 2026 Sumiran Dahal. geet runs `yt-dlp` and `ffmpeg` as separate programs. Their own licenses apply to them, and neither is bundled with geet.
