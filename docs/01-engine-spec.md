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
- Query: `yt-dlp "ytsearch{N}:{query}" --flat-playlist --dump-json`, where
  query is `youtube.search_query` (default `{artists} - {title}`) and N is
  `youtube.search_results` (default 5). `--flat-playlist` returns in ~1.5s
  and still carries title, channel, duration, views and the verified flag.
- Score every candidate (`score.go`), keeping all verdicts for debugging
  (`-v` logs them):
  - Reject: live streams, length off by more than
    `youtube.max_duration_diff` (default 10s), <60% of the base title's
    words (base = without "(feat. X)" / " - Remastered"), no artist named in
    title or channel.
  - Reward: title coverage, primary artist named, official channel (exact
    name, "Artist - Topic", VEVO, or a verified channel using part of the
    name), verified, "audio" in title, duration closeness, search rank.
  - Penalize "live", "cover", "remix", "concert", "sped up", … unless the
    Spotify title contains the same word.
- Fixtures in `testdata/` are real yt-dlp output; add one whenever a real
  search picks wrong.

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

## Output path (`internal/library`)
- `output` (default `~/Music`) + `output_template` (default
  `{artist}/{title} - {artists}`, e.g. `Gunna/fukumean - Gunna.opus`) +
  `.{format}`. Album/cover/track metadata lives in the file's tags, not
  the folder layout; users wanting `{album_artist}/{album}/{track} {title}`
  set it in config.
- Placeholders: `{title} {artist} {artists} {album} {album_artist} {track}
  {disc} {year} {isrc} {spotify_id}`. Each `/`-separated template segment
  is exactly one path component; values are sanitized (no `/`, FAT-unsafe
  characters removed) so metadata can't escape `output`.

## Configuration (`internal/config`)
Every setting is defined once in `Config.Settings()` and is automatically
available three ways, in increasing precedence:
1. `~/.config/spotify-dl/config.toml` (or `$SPOTIFY_DL_CONFIG`, or
   `--config`) — optional; unknown keys are an error.
2. Environment: `SPOTIFY_DL_<KEY>` with dots as underscores, e.g.
   `SPOTIFY_DL_YOUTUBE_SEARCH_RESULTS=8`.
3. Flags: key with dots/underscores as dashes, e.g.
   `--youtube-search-results 8`.

`spotify-dl config settings` lists them all.

## CLI (`cmd/spotify-dl`)
- `spotify-dl download <spotify-url>` — one-shot.
- `spotify-dl watch` — daemon: polls `wl-paste` (Wayland — not xclip/xsel)
  every ~1s, detects a new Spotify URL, downloads automatically, fires
  `notify-send` with cover art on completion/failure.
- `spotify-dl config [--json]` — effective config (secrets redacted);
  `config path [--json]`; `config settings [--json]` — every setting with
  flag, env name, type, default, current value and help, for building a
  settings UI.
- `spotify-dl version`.
- Common flags: `--config`, `--json`, `-v`, plus one flag per setting.
