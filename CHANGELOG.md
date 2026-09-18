# Changelog

All notable changes to geet are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/). Until 1.0, minor versions may
still change the CLI, the configuration or the `--json` output.

## [Unreleased]

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

[Unreleased]: https://github.com/sumdahl/geet/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/sumdahl/geet/releases/tag/v0.1.0
