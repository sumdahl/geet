# Engine Spec — components

## 1. Spotify metadata (`internal/spotify`, `internal/deezer`)
The official Web API needs an app owned by a Spotify **Premium** account, so
it can't be the only path. Two sources, chosen by config:

- **Keyless (default)** — `spotify.Web` reads Spotify's public pages:
  - `open.spotify.com/embed/{album|playlist}/{id}`: `__NEXT_DATA__` JSON with
    album name/artist, 640px cover, track list (title, artists, ms duration).
  - `open.spotify.com/track/{id}` fetched with a link-preview crawler
    User-Agent: `music:*` meta tags give the album link, track number and
    release date (browsers don't get these tags).
  - Limits: playlists show at most 100 tracks; no ISRC or disc number.
  - `internal/deezer` then fills ISRC and disc/track numbers from Deezer's
    keyless public API, best effort: album match first (fixes multi-disc
    numbering), then per-track search matched on title + artist + duration
    (±3s). Deezer geo-filters search by the caller's country, so some tracks
    (~30% of major-label hits when tested from Nepal) get no ISRC.
- **Official API** — used when `client_id`/`client_secret` are set in
  `~/.config/spotify-dl/config.toml`: Client Credentials flow, full metadata
  including ISRC, no playlist cap, no Deezer step.
- Both parse `spotify.com/track|album|playlist` URLs and `spotify:` URIs and
  produce the same `spotify.Track`: title, artists, album, album artist,
  largest cover URL, track/disc number, release year, duration, ISRC.

## 2. YouTube resolver (`internal/youtube`)
- Query: `yt-dlp "ytsearch5:{artists} - {title}" --dump-json --no-download`.
- Score the 5 candidates: title/artist token overlap, duration delta vs
  Spotify's `duration_ms` (reject if off by >10s), prefer official/topic
  channels, penalize "live"/"cover"/"remix" unless Spotify's title has it too.
- Return the best match's URL.

## 3. Downloader (`internal/download`)
- `yt-dlp -f bestaudio --extract-audio --audio-format {opus|flac|mp3}
  --audio-quality 0` into a tmp path. Format/bitrate configurable.
- See `02-concurrency-pipeline.md` for how this stage is scheduled across a
  playlist.

## 4. Tagger (`internal/tag`)
- Fetch Spotify's album art (prefer 640x640) into memory.
- ID3v2 for mp3 (`github.com/bogem/id3v2`); Vorbis comments for flac/opus
  (`go-flac`/`flacvorbis`, or fall back to `ffmpeg -metadata ...
  -c:v mjpeg -disposition:v attached_pic` for cover art so it works across
  formats).
- Fields: title, artist, album artist, album, track/disc number, date,
  ISRC (TXXX), embedded cover.

## CLI (`cmd/spotify-dl`)
- `spotify-dl download <spotify-url>` — one-shot.
- `spotify-dl watch` — daemon: polls `wl-paste` (Wayland — not xclip/xsel)
  every ~1s, detects a new Spotify URL, downloads automatically, fires
  `notify-send` with cover art on completion/failure.
- Flags: `--format`, `--output`, `--bitrate`, `--jobs`, `--resolve-jobs`,
  `--json`.
- Config at `~/.config/spotify-dl/config.toml` (XDG base dir spec).
