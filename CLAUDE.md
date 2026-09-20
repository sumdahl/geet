# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state

Deliverables 1–5 are done. `download` runs end to end (match, download, convert, tag, embed cover) as a concurrent three-stage pipeline (docs/02, "As built"), and `watch` runs it for each copied link (docs/01 CLI, "As built"). Deliverable 6 (the Omarchy plugin) is next. Publishing research (AUR, Homebrew, GoReleaser) is in `docs/research/publishing-brew-aur.md`. The docs in `docs/` are the spec, so read the relevant one before implementing a component. `docs/00-overview.md` indexes them.

## Metadata sources

The user's Spotify account is free, and the Web API needs Premium, so there are two sources (details in `docs/01-engine-spec.md` §1):
- **Keyless, the default:** `spotify.Web` scrapes the embed pages' `__NEXT_DATA__` plus the track page's `music:*` meta tags. Those tags are only served to a crawler User-Agent (`facebookexternalhit/1.1`); a browser UA gets none.
  - Playlists cap at 100 tracks.
  - Album tracks are numbered by position.
  - `internal/deezer` then fills in ISRC and disc/track numbers, best effort. A Deezer failure only logs a warning.
- **Official API:** `spotify.API` is used only when the config has credentials, via `cmd/geet` `resolve()`.
- Both sources return `spotify.Track`.
- Deezer search is geo-filtered. From Nepal it hides roughly 30% of major-label tracks, even though `/track/isrc:{isrc}` finds them. Missing ISRCs are therefore expected and not a matching bug.
- Scraped pages break silently when Spotify changes its markup. Parsing failures wrap `spotify.ErrPageFormat`. When one fires, fetch the live page and update both the parser and the fixtures in `internal/spotify/testdata`.
- `spotify.Web` has three protections against Spotify's rate limit (HTTP 429):
  - **Metadata cache:** `spotify.Cache` in `~/.cache/geet/spotify.json`, set by `newWeb` in `cmd/geet/main.go`, with a TTL from `spotify.cache_days`. A re-read playlist reads only songs it hasn't seen. `Save` merges with the file on disk, because `watch` and a download may run at once. Bump `cacheVersion` whenever `Track`'s fields change.
  - **Pacing:** one shared limiter of 10 requests/s (`requestsPerSecond`, an estimate). Tests must use `unpaced(NewWeb(...))` or they crawl; `TestWebPacesRequests` covers the pacing itself.
  - **One fetch per album:** concurrent workers share it through `singleflight`.
  - **Song page alone when hinted:** `Web.hints` holds the per-song details a playlist's or album's listing gives (exact artists, ms length, `isExplicit`). It's filled by `Playlist`, and by `Hint` for `--tracks` with a playlist link. `Tracks` then reads only the song page (`fromSongPage`: album name from og:description "Artists · Album · Song · Year", cover from og:image), and the album artist becomes the main artist. **Without a hint, the album page is still read,** because the song page has no explicit flag and Deezer's region-filtered search recognised 0 of 15 explicit songs (Nepal). A lone `Track()` always reads the album page.
- Spotify pages and the Deezer API both return absolute `next` URLs, so the test fixtures use a `{{base}}` placeholder that the test server rewrites.

**`main` is protected** by the ruleset "Protect main". Every change goes in through a pull request: branch, push the branch, open a PR, and let auto-merge squash it once the 5 required checks pass (lint, test and install on Ubuntu and macOS). A direct push to `main` is refused (GH013), and the PR must be up to date with `main`. Push with gh's token: `git -c credential.helper='!gh auth git-credential' push https://github.com/sumdahl/geet.git <branch>`.

Work proceeds in the order given in `docs/07-roadmap.md`, and **you must pause for user review after each deliverable**. After deliverable 3 (single-track download, tag and `--json`), stop and let the user use the engine by hand before starting on concurrency or the plugin. Do not build the playerctl v2 feature (`docs/05-future-v2-playerctl.md`) until plugin v1 works.

## What this is

A Go CLI that takes a Spotify track, album or playlist URL, gets its metadata (see below), finds the matching audio on YouTube, downloads it with `yt-dlp`, transcodes it with `ffmpeg`, then tags and saves it. It behaves like spotdl but is a native binary.

## Platforms

Linux is the target and macOS is best effort, where everything but `watch` (Wayland `wl-paste`, `notify-send`) should work. The user decided not to pursue other platforms for now: the BSDs and Windows don't compile (`syscall.Statfs` in `internal/doctor`, `syscall.Stat_t` in `internal/index`), and that's accepted. Keep new code building for `GOOS=darwin`. Releases come only from `.github/workflows/release.yml` (GoReleaser, `.goreleaser.yaml`) on a `v*` tag pushed after CI passes on `main`. The release steps are in the README, under "Continuous integration and releases". Never hand-build a release. Asset names must stay `geet-<os>-<arch>` plus `SHA256SUMS`: `install.sh` (the README's `curl … | sh` one-liner) downloads `geet-<os>-<arch>` and verifies it against `SHA256SUMS`, and v0.2.0 first shipped Linux-only, which gave a Mac user `exec format error`.

## Homebrew tap (`sumdahl/homebrew-geet`)

- A **cask**, not a formula: GoReleaser deprecated `brews` (formulas shipping a prebuilt binary) in v2.10 in favour of `homebrew_casks`. The generated cask carries a `postflight` `xattr -dr com.apple.quarantine` hook, because the binaries aren't signed or notarized and macOS would otherwise kill geet as "damaged".
- It's pushed to the tap repo with the `HOMEBREW_TAP_TOKEN` secret (a PAT with Contents: write on `homebrew-geet`); the workflow's own `GITHUB_TOKEN` can't write to another repository. **`skip_upload` is still `true`** until that secret exists.
- Users then run `brew install sumdahl/geet/geet`. homebrew-core (plain `brew install geet`) needs real notability (roughly 75+ stars) and is a later move.

## AUR (`geet-bin`)

- GoReleaser's `aurs` block generates the PKGBUILD from the `targz` archives and pushes it to `ssh://aur@aur.archlinux.org/geet-bin.git` with the `AUR_KEY` secret (release.yml). **`skip_upload` is still `true`**: AUR registration was closed on 2026-09-20, so the account doesn't exist yet. To go live: create the account, add `~/.ssh/aur.pub`, put the private key in the `AUR_KEY` repo secret, set `skip_upload: false`, and release.
- Don't hand-write a PKGBUILD: GoReleaser owns it. Test a change by running `goreleaser release --snapshot --clean`, then `makepkg` on `build/aur/geet-bin.pkgbuild` with the source URL pointed at the local tarball.
- The plain `geet-<os>-<arch>` assets must stay: `install.sh` matches those exact names in `SHA256SUMS`.

## Installer (`install.sh`)

- It's POSIX `sh`, ASCII only (bash 3.2 on macOS breaks on a non-ASCII byte after `$var`), and shellcheck-clean (`docker run --rm -v $PWD/install.sh:/i.sh:ro koalaman/shellcheck:stable -s sh /i.sh`).
- Prompts read `/dev/tty`, since stdin is the script under `curl | sh`. Terminal modes are restored by the EXIT trap, which must keep the original exit status.
- Required tools are yt-dlp, ffmpeg and fzf. All three are Homebrew formulae, not casks. On apt systems yt-dlp comes from its official build, because Debian's is too old.
- `.github/workflows/installer.yml` runs `--yes` on a real Mac and in Ubuntu, Debian, Fedora, Arch and Alpine containers.
- To test the interactive menus locally, run the installer in `tmux` inside a docker container, drive it with `tmux send-keys`, and read the screen with `tmux capture-pane -p`.

## Architecture rules (non-negotiable)

- **Never reimplement YouTube extraction.** Shell out to the `yt-dlp` binary for search and download, and to `ffmpeg` for transcoding and embedding cover art. Go handles metadata resolution, match scoring, orchestration, concurrency and the CLI.
- **The engine has no Omarchy dependency.** `geet` must work the same from any terminal on any Linux box. The Omarchy/Quickshell plugin (`omarchy-plugin-geet`, a separate repo built later) only runs this binary and reads its NDJSON output. It never imports engine internals.

## Layout

- `internal/config`: the engine is a backend for the Omarchy plugin, so everything is configurable.
  - `Config.Settings()` is the single list: each entry is automatically a TOML key, a `--flag` and a `GEET_*` env variable (precedence flag > env > file > default), and is listed by `geet config settings --json`.
  - To add a setting, add the struct field and one `Settings()` entry. `TestSettingsCoverConfig` fails if you forget the entry.
  - The config file is optional. Unknown keys in it are an error, and `misplaced` hints when a setting sits under the wrong `[section]`.
  - `config.Template` (`geet config init`, run by the installer when no file exists) lists every setting from `Settings()` commented out, with full dotted keys and no `[table]` headers, so a line appended at the end can't land in the last table. `TestTemplateListsEverySetting` uncomments each line and checks it loads.
- `internal/spotify` and `internal/deezer`: built. See "Metadata sources".
- **Explicit first, clean as a fallback** (the user's rule).
  - `Track.Explicit` comes from Spotify's embed `isExplicit`, the Spotify API's `explicit`, and iTunes `trackExplicitness`. It's part of the cache, so bump `cacheVersion` if `Track` changes.
  - On `ytdlp.ErrAgeRestricted`, `ErrUnplayable` or `ErrSignInRequired` ("Please sign in" for one upload, with no age gate; other videos still play), `downloader.tryOtherUploads` tries `youtube.Alternatives`, then `Resolver.MoreAlternatives` (a wider search), then `itunes.CleanEdit`. A stand-in must be the same recording: no variant words, and the full title minus "feat.", because explicit and clean often differ only inside the brackets ("Lovin'" / "Fuckin'").
  - A saved clean edit gets " (Clean)" in its title tag and a warning.
  - Heavy live testing trips YouTube's bot check for this IP ("not a bot" on every video). When that happens, stop live runs and rely on the recorded fixtures.
- `internal/youtube`: built. When both queries fail, `resolveMusic` searches YouTube Music's `#songs` (flat listing, then full details for up to 3 results whose title fits). A single video's full JSON puts a media stream in `url`, so `parseCandidates` takes `webpage_url` or builds the watch URL. Songs still rejected after that (another performer's recording, a different edit) are correct rejections, not bugs.
  - `yt-dlp ytsearchN:<query> --flat-playlist --dump-json` (about 1.5s), then scoring in `score.go`, which keeps every candidate's score or rejection reason (`-v` logs them).
  - `testdata/*.ndjson` are real searches. When a live search picks wrong, capture it as a fixture and add a case to `TestBestOnRealSearches` before changing weights.
  - **Backward-compatibility guard:** `TestCorpusBackwardCompatible` scores `testdata/corpus/playlist4.json` (the real first-query searches of the user's 201-song playlist) plus the hand-picked fixtures, and compares the result with `golden.json`. The only change it allows is a candidate going from "no artist named" to accepted. Any other score, verdict or pick change fails it. Regenerate the golden file (`GEET_UPDATE_GOLDEN=1`) only for a deliberate scoring change, and say so in the PR.
  - Artist matching levels: `artistPrimary`, `artistFeatured`, then `artistCore` (the name minus `genericNameWords`, tried only when no full name matches, scored lowest).
  - `resolveTitleOnly` is the last resort, after both queries and YouTube Music: a title-only search, accepting only `exactReupload` (`sameRecording`: full title, no variant, other-edit, performance or version-stem words, within 2 s). Picks are `TitleOnly` and get a warning.
  - `testdata/fake-yt-dlp` stands in for the binary in tests.
- `internal/library`: output path = `output` (plus `FolderName(playlist)` for playlists) + `output_template` + extension. Each template segment is exactly one path component, sanitized.
- Resolving a link returns a `spotify.Collection` (ref, name, tracks). The name is what the playlist folder is named after.
- `internal/index`: the download index, which stops the same song being downloaded twice (hard link, copy or skip, per `duplicates`).
  - It keeps every copy of a recording (ISRC maps to a list of keys), and every saved copy of a song (`Entry.Also`), so deleting one copy re-links from another instead of downloading again.
  - `Lookup` prefers a copy on the destination's filesystem, because only that can be hard-linked.
  - It relies on the comment tag holding the Spotify track URL, which `internal/audio` writes. The first-run `Scan` finds old downloads that way, so never drop that tag.
- `internal/itunes`, `internal/deezer` (`catalog.go`) and `cmd/geet/search.go`/`pick.go`: `geet search`. It queries the keyless Apple and Deezer catalogs in parallel (`searchCatalog`), ranks them together (`itunes.Rank`), picks with fzf or a numbered list, and runs the same pipeline.
  - Apple often has an explicit song only as its clean edit; Deezer has explicit originals but is region-filtered. `Rank` merges the same song from both (title minus "feat.", primary artist, ±2 s) but never a clean edit with its explicit original, and `explicitFirst` puts an explicit song above its own clean edit.
  - A picked Deezer result is re-read with `deezer.Lookup`, because search results lack featured artists, year and ISRC.
  - Deezer refs are `deezer:<id>`, parsed by `deezer.ParseRef` alongside Apple's `itunes:<id>`.
  - Raw iTunes order is unusable: covers come before originals, and each album edition repeats. `Rank` merges editions and uses the edition count as popularity.
  - Search downloads carry the Apple Music URL in the comment tag, and the index maps it to `itunes:<id>`.
  - Fixtures in `internal/itunes/testdata` are real catalog responses.
- `internal/ytdlp/cookies.go`: `ytdlp.CookieSource` resolves `youtube.cookies_from_browser` ("auto" means the default browser via xdg-settings, and the Chromium keyring comes from `<browser>-flags.conf` `--password-store`, else from the running keyring service).
  - Without the `+keyring` suffix, yt-dlp decrypts nothing on Hyprland and only warns about it. A bot-check error carries that warning (`CookieTrouble`) so the advice can name the real cause.
- `internal/doctor` and `cmd/geet/doctor.go`: `geet doctor`.
  - Parsing and verdicts (yt-dlp age, ffmpeg encoders, error to fix) are pure functions, and tests use fake tool scripts.
  - The service checks make real requests to known-stable public items (a CC-licensed Spotify track, an iTunes ID). Keep them to one small request each.
- `internal/textnorm`: shared title/name normalization. It keeps Unicode marks so Devanagari words don't split apart.
- `internal/ytdlp`: the shared yt-dlp runner (cookies and extra args), used by both search and download.
- `internal/download`: fetches the raw best audio only. It deliberately avoids `--extract-audio`, which ignores the requested bitrate when the codecs match.
- `internal/audio`: one ffmpeg pass that converts or copies, tags and embeds the cover (see docs/01 §4 for the opus cover and argument-length details). Its round-trip tests run real ffmpeg on a generated tone and skip when ffmpeg is missing.
- `cmd/geet/download.go`: the per-track stages and the NDJSON `event` type. Only ever add event fields; docs/03 has the schema.
- Before downloads start, resolving a playlist is slow (about 1s of page reads per track, then the Deezer lookups). `resolveMetadata` reports both steps, which drive `ui.phase` lines and NDJSON `reading` events.
- Downloads retry `download_retries` times (default 2), because YouTube fails transiently (403s, throttling). yt-dlp errors lead with yt-dlp's own reason so it survives truncation in the display.
- `cmd/geet/ui.go`: the stderr display, using mpb bars (`barUI`) in a terminal and plain lines (`plainUI`) otherwise.
  - A finished track's bar is removed and a permanent result line is logged above the live area, because mpb never draws a bar that completes before its first refresh.
  - Runs of more than 8 tracks use compact mode: a bar only while downloading or tagging, and a bottom summary line (`BarPriority(MaxInt32)`) with per-state counts.
  - The summary never completes on its own, so `close()` aborts it before `p.Wait()`.
  - `"downloaded"` is a UI-only stage (not in the NDJSON), so a track waiting for a tag worker stops counting as downloading.
  - Track bars are capped to the terminal's height minus `reservedRows` (`acquire`/`release`; extra tracks queue for a line). A bar's slot is freed only after `bar.Wait()` (its last draw, in `drop`): when a redraw has more rows than the terminal, mpb v8.16.1 undercounts the lines and leaves stale bars in the scrollback. `GEET_UI_DEMO=1` runs `TestUIDemo`; build it with `go test -c` (go test captures stderr), run it under `script` at several `stty rows`, and replay the output into pyte, since tmux's capture misreports this.
  - Never call into a `*mpb.Bar` while holding `barTrack.mu`: the render goroutine takes that lock in `status`.
  - To test the animation headlessly, the pty needs a size: `script -qefc "stty cols 150 rows 40; <cmd>" /dev/null`. With 0 rows mpb draws nothing.
- `cmd/geet/watch.go`, `internal/clipboard`, `internal/notify`: `geet watch`.
  - Jobs (one per copied link) run one at a time on the main goroutine; the clipboard goroutine only queues them. It logs through `rep.ui`, so swap the display with `reporter.setUI` (under `rep.mu`), never by assigning `rep.ui`.
  - Every queued job gets exactly one `finished` event, even when dropped or interrupted. The plugin relies on that pairing.
  - `runDownload` returns an `outcome` and an error; only `download`/`search` turn those into exit codes (`reporter.exit`), since `watch` must survive a failed link.
  - `clipboard.capped` must not embed `bytes.Buffer`: its promoted `ReadFrom` lets `io.Copy` skip the size cap.
  - Omarchy's `SUPER + SHIFT + Y` is its YouTube webapp, so the documented keybind unbinds it first.
- `cmd/geet`: the subcommands `download <url>` and `watch`. `watch` is a daemon that polls `wl-paste` (Wayland, not xclip) about once a second and sends a `notify-send` notification with the cover art. Flags: `--format --output --bitrate --jobs --resolve-jobs --json`.

## Playlist pipeline (implemented in `internal/pipeline` and `cmd/geet/download.go`)

`tracks → [resolve pool] → [download pool] → [tag pool]` (see `docs/02-concurrency-pipeline.md`):
- Resolve pool defaults to 8 workers (`--resolve-jobs`), download pool to 4 (`--jobs`). The tag pool is fixed at 2 and is not configurable.
- Each stage is an `errgroup` worker set sharing one `context.Context`, so a single failure or Ctrl+C stops all three stages.
- Every channel is bounded (cap ≈ that stage's concurrency) so downloads push back on resolve.
- An NDJSON line is emitted each time a track crosses a stage boundary, not only at the end.

## `--json` contract (stable API: the plugin depends on it)

- stdout carries NDJSON only. All human-readable logs go to stderr.
- Line shape: `{"track":"...","stage":"resolved|downloading|tagging|done|failed","error":"..."}`
- `watch --json` streams the same line shape continuously.
- Exit codes: `0` all tracks succeeded, `1` partial failure, `2` fatal (bad URL, auth failure, missing `yt-dlp`/`ffmpeg`).

## Code style

- Comment only where the reason isn't obvious from the code, such as an API quirk or a workaround.
- Use `slog` for logging and wrapped errors (`%w`, checked with `errors.Is`/`errors.As`). Pass `context.Context` through every HTTP call and subprocess.
- Write `any`, not `interface{}`. Use generics only where they remove duplication, and skip interfaces for code with a single implementation.

## Testing

- Write table-driven tests for URL parsing and for YouTube scoring, using fixture JSON rather than live calls.
- Include a tag round-trip test that writes tags and reads them back.
- Include a pipeline cancellation test that cancels mid-flight and asserts no goroutines leak.
- Tests must not use the live network: mock the Spotify HTTP client and stub `yt-dlp` JSON output.
- CI (`.github/workflows/ci.yml`) runs golangci-lint (`.golangci.yml`), vet and race tests on Linux **and macOS**. It's the only real Mac we have, so keep tests free of GNU-only shell (`sed -i`, `readlink -f`, `stat -c`).
- Commands: `golangci-lint run`, `go test -race ./...`, a single test with `go test ./internal/spotify -run TestParseURL`, `go vet ./...`, `gofmt -l .`. Run locally with `go run ./cmd/geet download <url>` (add `-v` for debug logs).

## Repo-local skill

`golang-performance` (from `samber/cc-skills-golang`, pinned in `skills-lock.json`) lives in `.agents/skills/`. The `.claude/`, `.kiro/`, `.qwen/`, `.junie/` and `.qoder/` skill directories are symlinks to it, so edit or update it only in `.agents/`. Use it for optimization patterns once profiling or benchmarks have found a bottleneck, not for measuring.
