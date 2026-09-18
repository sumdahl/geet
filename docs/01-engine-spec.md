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
  `~/.config/geet/config.toml`: Client Credentials flow, full metadata
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
- Nothing matched? Search once more with `youtube.fallback_query` (default
  `{artists} - {title} audio`): an official video with an intro can push
  the first page past the duration limit while the artist's separate
  "(Audio)" upload only shows up when asked for (real case: "Renegade").
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
- The file is encoded in a hidden `.geet-*` work dir inside `output`
  and renamed into place, so a finished file appears atomically and Ctrl+C
  leaves nothing behind. Existing files are skipped unless `overwrite`.

## Search (`internal/itunes`, `cmd/geet/search.go`)
- `geet search <words…>` calls the iTunes Search API (`entity=song`, 50 results,
  `search.country` store) and maps each result to a `spotify.Track`: ID
  `itunes:<trackId>`, 600px cover, the Apple Music link as `SourceURL` (and as
  the comment tag), and `Clean` for clean edits.
- `itunes.Rank` merges album editions (same normalized title and artists,
  within 2s), scores query-word coverage minus variant words (title in full,
  album/artist at half) plus an edition-count popularity bonus, and sorts
  stably. Found live: the raw order put The Weeknd's "Blinding Lights" 7th.
- Picks go through fzf (`--multi`, index in a hidden first field) or a
  numbered prompt, then `runDownload` with late Deezer tags, as for tracks.
- Clean edits have censored titles; `youtube.titleWords` gives their words a
  leading wildcard so "umean" still matches "fukumean" uploads.

## Duplicates (`internal/index`)
- `$XDG_DATA_HOME/geet/index.json` (`index_path`) maps Spotify track
  ID + format → file, plus ISRC → track, updated and saved after every
  track. A track already downloaded elsewhere (another playlist; or the
  same ISRC under another ID, e.g. album vs single) is not downloaded again;
  `duplicates` decides: `link` (default: hard link — no extra space, falls
  back to a copy across filesystems), `copy`, `skip`, or `download`.
- Only the same format counts (an mp3 request never reuses an opus).
  Entries whose file was deleted are dropped and the track downloads again.
- First run with no index: every audio file under `output` is read with
  ffprobe; files geet tagged (comment = Spotify track URL) are indexed.

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
1. `~/.config/geet/config.toml` (or `$GEET_CONFIG`, or
   `--config`) — optional; unknown keys are an error.
2. Environment: `GEET_<KEY>` with dots as underscores, e.g.
   `GEET_YOUTUBE_SEARCH_RESULTS=8`.
3. Flags: key with dots/underscores as dashes, e.g.
   `--youtube-search-results 8`.

`geet config settings` lists them all.

## CLI (`cmd/geet`)
- `geet download <spotify-url>` — one-shot.
- `geet watch` — daemon: polls `wl-paste` (Wayland — not xclip/xsel)
  every ~1s, detects a new Spotify URL, downloads automatically, fires
  `notify-send` with cover art on completion/failure. As built:
  - The clipboard at startup is the baseline and never downloaded; a job
    starts only when the text changes to something containing links.
  - Each album/playlist link is a job; song links copied together (Spotify
    Ctrl+C) are one job. Jobs run one at a time in copy order through the
    normal pipeline (queue of 32; overflow gets a `finished` with an
    error). A failed job never stops the daemon.
  - One notification per job: "Downloading" with the cover (cached under
    `$XDG_CACHE_HOME/geet/covers`), replaced in place (`notify-send -r`)
    by the result. Body text is markup-escaped (`& < >`): the Omarchy
    shell's notification server advertises `body-markup`.
  - Settings: `watch.interval` (1s), `watch.notify` (true),
    `tools.wl_paste`, `tools.notify_send`. NDJSON: `queued`/`finished`
    stages, `job`/`source` on every event (docs/03).
- `geet config [--json]` — effective config (secrets redacted);
  `config path [--json]`; `config settings [--json]` — every setting with
  flag, env name, type, default, current value and help, for building a
  settings UI.
- `geet version`.
- Common flags: `--config`, `--json`, `-v`, plus one flag per setting.
