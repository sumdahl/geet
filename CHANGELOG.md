# Changelog

All notable changes to geet are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). Until 1.0, minor versions may
still change the CLI, the configuration or the `--json` output.

## [Unreleased]

### Added
- `geet play` now plays without downloading first. A song in your library
  plays from disk; anything else streams from the same YouTube match a
  download would use, so it starts in a couple of seconds. **`d` keeps the
  song**: it downloads a tagged copy in the background while the music
  carries on. The next song in a queue is found while the current one
  plays, so there is no gap. `player.stream = false` restores the old
  download-first behaviour.

### Fixed
- Nepali (and other Indic) lyrics no longer overlap the spectrum in the
  player. Terminals draw Devanagari clusters at widths Unicode doesn't
  predict — "सम्झनामा" is 15 clusters, 19 columns and 24 runes — so those
  lyrics now get the full width with the spectrum above them, are measured
  at their worst case, and are only ever cut between clusters, never inside
  one. Latin lyrics keep the side-by-side layout.

## [0.5.0] - 2026-09-21

### Added
- `geet play` — geet is a player as well as a downloader. With no argument it
  plays your library, newest first; given words it plays what matches (and
  offers the catalogue when nothing does); given a link or an
  `itunes:`/`deezer:` ref it plays what you already have and downloads what
  you don't.
- The player screen shows the song, a progress bar, a spectrum drawn from a
  real FFT of the audio, and synced lyrics from LRCLIB that follow the beat.
  Songs without lyrics say so quietly and give the space back to the
  spectrum; unsynced lyrics are marked as such. Keys: space, ←/→ (shift for
  30s), n/p, v, y, q.
- `geet play --json` streams playback as NDJSON (`track`, `playing`,
  `paused`, `position`, `stopped`) for the Omarchy plugin.
- Playback uses mpv through its IPC socket, which is what makes the position
  exact enough for lyrics; ffplay (part of ffmpeg) is the fallback. New
  settings: `player.engine`, `player.visualizer`, `player.lyrics`,
  `player.shuffle`, `player.repeat`, `player.mpv`, `player.ffplay`.

## [0.4.6] - 2026-09-20

### Added
- Releases also carry `geet_<version>_<os>_<arch>.tar.gz` archives (with the
  LICENSE, README and changelog) beside the plain `geet-<os>-<arch>`
  binaries, and a `geet-bin` PKGBUILD for the AUR is generated from them.
  Pushing it to the AUR is still switched off (`aurs.skip_upload`): AUR
  account registration is closed at the moment.
- Homebrew: `brew install sumdahl/geet/geet` installs geet (with yt-dlp and
  ffmpeg) from the tap `sumdahl/homebrew-geet`, and `brew upgrade` keeps it
  current. Every release publishes the cask.

## [0.4.5] - 2026-09-19

### Fixed
- A song whose YouTube upload asks for a sign-in ("Please sign in", with no
  age restriction) no longer fails after retrying the same video: geet now
  tries the song's other uploads, as it does for age-restricted ones.
  "Jackson Laird - Microdose" now downloads without a sign-in. If no other
  upload works, geet says once how to use your browser's YouTube sign-in.

## [0.4.4] - 2026-09-19

### Fixed
- A song saved in two folders is no longer downloaded again after one copy
  is deleted: the download index now remembers every copy, and links from
  whichever one still exists. Only when every copy is gone does geet
  download it again. Index files from older versions still load.
- The progress display no longer leaves stale "downloading", "waiting to
  tag" or "tagging" lines behind in a playlist run with many jobs. Bars are
  now capped to what fits the terminal (other tracks wait for a free line,
  and the summary line still counts them), and a freed line is reused only
  after the finished bar has been drawn for the last time.

## [0.4.3] - 2026-09-19

### Fixed
- Songs whose artist YouTube knows by a shorter name now match: Spotify's
  "Kush Band Nepal" is "KUSH" on YouTube, and "Harayeko Graha" failed with
  "no YouTube result matched". When no upload names the artist in full, the
  distinctive part of the name counts (without band, the, official, music,
  Nepal, ...), scored below a full name, so an upload naming the artist in
  full still wins.

### Added
- `youtube.title_fallback` (on by default): when every search has failed,
  geet searches the title alone and accepts only an upload with the full
  title, within 2 s of the length, and no variant, karaoke, cover or TV-show
  words (one-word titles are excluded). The song then carries a warning.
- A backward-compatibility test scores the real searches of a 201-song
  playlist (1,000 candidates) and fails if any existing score or pick
  changes.

### Changed
- Stand-ins for a song (an age-restricted upload's, or one matched by title
  alone) now also exclude competition, session and karaoke uploads,
  however the words are spelled ("karoke", "instrumentally").

## [0.4.2] - 2026-09-19

### Changed
- Reading a playlist from Spotify makes about half the requests. A
  playlist's own page lists each song's artists, length and explicit flag,
  so its songs are read from their song pages alone (album name and cover
  are there too), without album pages. `--tracks` with a playlist link does
  the same for the first 100 songs. Cold reads: a 42-song playlist went
  from 7.7 s to 5.8 s, the 201-song list from 45.5 s to 36.2 s, with the
  same metadata except the album artist of compilations and soundtracks,
  which becomes the song's main artist. Songs with no playlist listing
  still read their album page, which is where Spotify marks explicit songs.
- Deezer's explicit flag also marks a song explicit.

## [0.4.1] - 2026-09-19

### Added
- The installer creates the config file (`~/.config/geet/config.toml`; on
  macOS `~/Library/Application Support/geet/config.toml`) with every
  setting listed at its default under its explanation, commented out:
  uncomment a line to change it. An existing file is never touched. `geet
  config init` writes the same file (`--force` replaces one). Keys are
  written in full (`youtube.search_results`), so a setting added anywhere,
  even at the end of the file, works; a setting under the wrong `[section]`
  gets an error saying where it belongs.

### Fixed
- An explicit song whose official upload YouTube won't serve now still
  comes as the explicit version. In 0.4.0, Enrique Iglesias's "Tonight
  (I'm Fuckin' You)" failed in a real 201-song run: signed in, YouTube
  answers an age-restricted video with only "Requested format is not
  available", which geet didn't recognise, and it retried three times.
  Such uploads (no format, removed, private) are now tried once. geet then
  tries the other uploads of the song, then a wider search (10 results, two
  phrasings) for explicit copies, before the clean edit.
- Errors from YouTube Music and the fallbacks lead with the cause ("YouTube
  wants you to confirm you're not a bot"), and a fallback that hits the bot
  check reports that instead of "age-restricted".
- yt-dlp's warnings are no longer suppressed for downloads; geet needs
  them to recognise age restrictions and undecryptable cookies.

## [0.4.0] - 2026-09-18

### Added
- `geet search` lists both versions of explicit songs, explicit first:
  `[E]` marks the explicit version, `(clean)` the clean edit, and `--json`
  has `"explicit": true`. Results come from the Apple and Deezer catalogs at
  once. Apple often has an explicit song only as its clean edit, while
  Deezer has the explicit original ("Tonight (I'm Fuckin' You)" is only on
  Deezer). The same song from both is merged, and an explicit song is
  moved above its own clean edit. A result by an artist the query names now
  ranks above other artists' look-alike uploads, and type beats count as
  variants.
- Deezer links and `deezer:<id>` refs download like Apple Music ones, in
  `download`, `--tracks` and `watch`.
- Explicit songs download as the explicit version by default, and the clean
  edit is only a fallback. geet reads Spotify's (and Apple's) explicit
  flag, and uploads marked clean, radio edit or censored score lower. When
  YouTube age-restricts the explicit upload, geet tries other uploads of the
  same recording, including exact re-uploads (full title, within 2 s), and
  only then the clean edit, found in the Apple catalog and saved with
  " (Clean)" in its title tag and a warning. Tested live on Enrique Iglesias's
  "Tonight (I'm Fuckin' You)": without any sign-in it now saves the explicit
  audio (232.26 s against Spotify's 232.21 s) from a re-upload.
- Songs read from Spotify's public pages are cached for 30 days in
  `~/.cache/geet/spotify.json` (`spotify.cache_days`, where 0 turns it off).
  Reading a playlist again, to pick up new songs or retry skipped ones,
  reads only the songs not seen before: a 42-song playlist went from 6.2 s
  to 0.4 s. watch and a download running side by side share the cache
  safely.

### Changed
- Requests to Spotify are paced to at most 10 a second across all workers,
  because bursts are what trigger its rate limit. The default 8 workers
  stay below that pace, so only a raised `resolve_jobs` is slowed.
- Songs of the same album share a single album-page fetch, even when
  several workers want it at the same moment.
- The README no longer recommends `resolve_jobs = 24`. Measured on a
  42-song playlist, 24 wasn't faster than the default 8, and reading many
  Spotify pages at once is what causes rate limits. `resolve_jobs`'s help
  now says what it controls.

### Fixed
- Songs YouTube search can't match are now looked up on YouTube Music's
  songs (`youtube.music_fallback`, on by default), which lists the official
  studio audio at the album's exact length. Bartika Eam Rai's "Kaalpanik /
  Maayajastai" failed before: regular search found only the music video (21
  s of intro too long), a live session and uploads spelling it
  "MaayaaJastai"; YouTube Music has it on the artist's Topic channel at
  exactly 273 s. It costs about 4 s, only for songs that didn't match.
- An age-restricted video ("Sign in to confirm your age") is reported as
  such, once, with the fix, instead of being retried three times and ending
  in yt-dlp's cut-off message. Signed in with an account that isn't
  age-verified, YouTube offers only low-quality video, and geet says that
  too.
- Songs whose official upload names the artist only in a run-together
  channel name, or writes the title with different spacing, no longer fail
  with "no YouTube result matched". A$AP Rocky's "1Train" failed on both
  counts: the verified channel is `ASAPROCKYUPTOWN`, and fan uploads write
  "1 Train". Matching now reads a `$` in a name as `s` (A$AP → ASAP, Joey
  Bada$$ → Badass), matches words split differently ("1Train" / "1 Train"),
  and accepts an artist name of five or more letters at the start of a
  channel name.

## [0.3.0] - 2026-09-18

### Added
- `geet watch`, the clipboard daemon: copy a Spotify track, album or
  playlist link (or several songs at once) and it downloads, one link after
  another. A desktop notification with the cover shows the download start
  and changes to the result. A failed link doesn't stop it. New settings:
  `watch.interval`, `watch.notify`, `tools.wl_paste`, `tools.notify_send`.
  Linux (Wayland) only.
- `watch --json` adds `queued` and `finished` events (one each per copied
  link, with `counts` of saved, existing and failed tracks), and a `job`
  and `source` on every event, so a consumer can tell links apart.
- macOS builds (`geet-darwin-amd64`, `geet-darwin-arm64`), and a one-line
  installer for Linux and macOS:
  `curl -fsSL https://raw.githubusercontent.com/sumdahl/geet/main/install.sh | sh`.
  It picks the binary for the system and verifies it against `SHA256SUMS`.
  In a terminal it then offers, from an arrow-key menu, to install the
  required tools (yt-dlp, ffmpeg, fzf) and the optional ones for `geet
  watch`, through Homebrew, pacman, apt, dnf or apk, showing every command
  (sudo included) first, and to add `~/.local/bin` to `PATH`. `--yes`,
  `--required` and `--no-deps` do the same without questions. Before this,
  the README's download command fetched the Linux binary on a Mac too,
  which fails with `exec format error`.
- `--tracks` (stdin) or `--tracks=FILE` downloads a list of song links, one
  per line, for example `wl-paste | geet download <playlist> --tracks` after
  selecting every song in Spotify (Ctrl+A, Ctrl+C). This is how playlists
  over Spotify's 100-song public limit are downloaded in full. The playlist
  link names the folder, duplicates in the list are dropped, and `#` lines
  are comments.
- A playlist larger than its public page is detected ("100 of the 201
  songs"), with the exact command to get the rest.
- `youtube.cookies_from_browser = "auto"` uses the default browser's YouTube
  sign-in. For Chromium-based browsers the keyring is added automatically
  (`brave+gnomekeyring` on Omarchy), whether the browser is named or
  detected. Without it, yt-dlp decrypts no cookies outside GNOME or KDE and
  silently sends none. `geet doctor` shows the resolved value and checks that
  the cookies decrypt.
- Releases are built and published by GitHub Actions with GoReleaser, and
  every binary has signed build provenance (`gh attestation verify <file>
  -R sumdahl/geet`). CI runs lint and the tests on Linux and macOS.

### Changed
- YouTube's "confirm you're not a bot" block gives each affected track a
  short ✗ line, and the run explains the fix once, depending on the cookie
  setup: turn cookies on, fix a keyring that can't decrypt them, or sign in
  to YouTube in that browser. Those downloads aren't retried, since retrying
  only prolongs the block.
- `geet doctor` checks that YouTube serves audio, not only search results,
  because the bot check blocks downloads while search keeps working.
- `geet doctor` reports Spotify rate-limiting (HTTP 429) as such, at once,
  with how long to wait, instead of timing out or blaming the network.
- Warnings print as plain `warning: …` lines instead of timestamped log
  records. `-v` still shows everything.

### Fixed
- Reading many songs from Spotify at once could hit HTTP 429 and abort the
  whole run, after 188 of 201 songs had been read. Now every worker pauses
  together, honouring Retry-After and backing off up to 30 s, and a song
  that still can't be read is skipped with a warning while the rest
  download. Running the same command again fetches the skipped ones.

## [0.2.0] - 2026-09-18

### Added
- `geet doctor`: a health check of everything geet depends on. It covers
  yt-dlp (with age), ffmpeg and its encoders, ffprobe, fzf, the config, the
  library folder (writable, free space) and the index, plus a small live
  request to Spotify, YouTube, Deezer and iTunes. Each problem comes with a
  fix. `--offline` skips the network, and `--json` is for the plugin. The
  exit code is 1 when something needs fixing.

### Changed
- Big runs (more than 8 tracks) use a compact display. Only tracks that are
  downloading or tagging get a progress bar, and one summary line at the
  bottom counts the rest: `14 finding on YouTube · 11 queued · 16
  downloading · 2 tagging · 5/49 done`. At `--jobs 16` the live area went
  from about 45 lines to at most 20, so it fits a normal terminal.
- When the same song exists in several places, duplicate linking prefers a
  copy on the same drive as the destination, so it can be hard-linked
  instead of copied. The index now remembers every copy of a recording, not
  just the most recent one.

### Fixed
- A flag mistake (such as `--output` with no value) prints the error and a
  one-line hint instead of the full list of 38 flags. `-h` still shows them
  all.
- After Ctrl+C (or any fatal error) in a terminal, the `geet: interrupted`
  line is printed again. It was written to the progress display after that
  display had shut down, and lost.
- `geet help` lists `download`'s Apple Music links and `itunes:` refs, and
  marks `watch` as not built yet.

## [0.1.0] - 2026-09-18

First release.

### Added
- `geet download <link>` for Spotify tracks, albums and playlists: matches
  each song on YouTube, downloads it with yt-dlp, and writes the tags (title,
  artists, album, album artist, track and disc number, year, ISRC, source
  link) and a 640 px cover with ffmpeg.
- Metadata without a Spotify account or API keys, from Spotify's public
  pages, with ISRC and disc numbers from Deezer. The official Web API is used
  if credentials are configured.
- `geet search <words…>`: finds songs in the iTunes catalog, merges album
  editions, ranks originals above covers, lets you pick in fzf (or a numbered
  list), and confirms before downloading. `--pick` and `--json` are there for
  scripts, and `geet download` accepts `itunes:<id>` and Apple Music links.
- YouTube matching that rejects wrong lengths, other songs and uploads not
  naming the artist, prefers official, Topic and VEVO channels, penalizes
  live, cover, remix and sped-up uploads, and handles censored titles, accents
  and Devanagari. If nothing matches, it searches again for the "audio"
  upload.
- Audio as Opus at YouTube's original quality (no re-encode), or MP3/FLAC at
  a chosen bitrate, with a warning when the result can't beat the source.
- Playlists download into their own folder with a hyphenated name, and tracks
  are resolved, downloaded and tagged in parallel (`--jobs`,
  `--resolve-jobs`).
- A download index: existing files are skipped, and a song already
  downloaded elsewhere is hard-linked instead of downloaded again.
- Animated progress bars in a terminal, plain lines elsewhere, and NDJSON
  progress events with `--json`.
- Every setting is available as a config key
  (`~/.config/geet/config.toml`), a flag and a `GEET_*` environment variable,
  and `geet config settings --json` lists them.
- Retries for failed downloads and for rate limiting by Spotify, iTunes and
  YouTube. Ctrl+C leaves no partial files, and re-running resumes.
- `geet version` shows the release, commit and date.

[Unreleased]: https://github.com/sumdahl/geet/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/sumdahl/geet/compare/v0.4.6...v0.5.0
[0.4.6]: https://github.com/sumdahl/geet/compare/v0.4.5...v0.4.6
[0.4.5]: https://github.com/sumdahl/geet/compare/v0.4.4...v0.4.5
[0.4.4]: https://github.com/sumdahl/geet/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/sumdahl/geet/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/sumdahl/geet/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/sumdahl/geet/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/sumdahl/geet/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/sumdahl/geet/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/sumdahl/geet/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sumdahl/geet/releases/tag/v0.1.0
