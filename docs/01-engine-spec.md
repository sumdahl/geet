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
- `yt-dlp --format "bestaudio[acodec=opus]/bestaudio"` into a work dir,
  printing the file path, codec and bitrate. No `--extract-audio`: yt-dlp's
  conversion silently ignores the requested bitrate when the source codec
  already matches (asked for 96k opus, got the 152k source copied).
- YouTube's best audio is ~150 kbps Opus (format 251), ~130k AAC otherwise;
  YouTube Premium cookies can unlock 256k AAC.

## 4. Encode + tag (`internal/audio`)
One ffmpeg pass per track: convert, tag, embed cover.
- opus, no bitrate, Opus source → stream copy (no quality loss); otherwise
  libopus at the bitrate (160k default). mp3 → libmp3lame at the bitrate,
  or VBR V0 without one. flac → flac.
- Tags come from an FFMETADATA file (an opus cover as base64 is ~150 KB,
  over Linux's 128 KB per-argument limit): title, artist, album_artist,
  album, track, disc, date, ISRC (ID3 `TSRC` for mp3), and the Spotify URL
  as comment.
- Cover: mp3/flac as an attached picture stream (ID3v2.3 APIC / FLAC
  PICTURE); opus as a `METADATA_BLOCK_PICTURE` tag.
- `QualityWarning`: FLAC, or a bitrate >10% above the source, gets a
  warning (once per run on stderr, per track in NDJSON).
- The file is encoded in a hidden `.spotify-dl-*` work dir inside `output`
  and renamed into place, so a finished file appears atomically and Ctrl+C
  leaves nothing behind. Existing files are skipped unless `overwrite`.

## Output path (`internal/library`)
- `output` (default `~/Music`) + `output_template` (default
  `{title} - {artists}`, flat in `output`, e.g. `fukumean - Gunna.opus`) +
  `.{format}`. Album/cover/track metadata lives in the file's tags, not
  the folder layout; users wanting `{album_artist}/{album}/{track} {title}`
  set it in config.
- Playlists (only) go one level deeper, into a folder named after the
  playlist (`playlist_folder`, default on): no spaces, words joined by `-`,
  apostrophes dropped, other punctuation/emoji removed, any script kept;
  case per `playlist_folder_case` = `lower` (default, `road-trip-mix`),
  `capitalize` (`Road-trip-mix`) or `title` (`Road-Trip-Mix`).
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
