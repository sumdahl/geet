# Engine Spec — components

## 1. Spotify metadata client (`internal/spotify`)
- OAuth2 Client Credentials flow, reading `client_id`/`client_secret` from
  `~/.config/spotify-dl/config.toml` (provided by the user, not generated).
- Parse `spotify.com/track|album|playlist` URLs.
- Fetch: title, artists, album, largest album art URL, track/disc number,
  release year, `duration_ms`, ISRC. Paginate for album/playlist.

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
