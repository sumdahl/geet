# geet

*geet* (गीत in Devanagari) means "song". It plays music, and keeps the songs you want as tagged audio files.

`geet` reads a link's metadata from Spotify, finds the matching upload on YouTube, and either plays it straight away or downloads it with `yt-dlp` and uses `ffmpeg` to convert it, tag it and embed the album cover. A song starts playing in a couple of seconds; a 50-track playlist downloads in about a minute.

- **Listen first, keep what you like.** `geet play` starts a song in seconds without downloading it, with a spectrum drawn from the audio and lyrics that follow the beat. One key saves a proper tagged copy, without interrupting the music.
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

```
$ geet play "https://open.spotify.com/track/0VjIjW4GlUZAMYd2vXMi3b"

  Blinding Lights                                              ▶  0:34 / 3:23
  The Weeknd  ·  After Hours  ·  streaming · d to keep
  ━━━━━━━━━━━━━────────────────────────────────────────────────────────────

        ▃▃       ██                         I'm goin' through withdrawals
        ██ ▆▆    ██                       ▸ You don't even have to do too much
     ▃▃ ██ ██ ▄▄ ██ ▂▂                      You can turn me on with just a touch

  1 of 1                 space pause  ·  ←/→ seek  ·  d keep  ·  q quit
```

## Requirements

- Linux: supported and tested. macOS: best effort, and everything except `geet watch` (which needs Wayland) should work, but it isn't tested there. Other platforms (BSDs, Windows) aren't supported.
- Go 1.27+ to build
- [`yt-dlp`](https://github.com/yt-dlp/yt-dlp), `ffmpeg` and `ffprobe` on `PATH`. The paths are configurable.
- For `geet watch` only: a Wayland session with `wl-paste` (wl-clipboard), and `notify-send` (libnotify) for notifications.
- For `geet play`: [`mpv`](https://mpv.io) is recommended. Without it geet falls back to `ffplay`, which comes with ffmpeg but cannot seek or report an exact position, so lyrics follow less closely.

```sh
sudo pacman -S yt-dlp ffmpeg wl-clipboard libnotify   # Arch / Omarchy
brew install yt-dlp ffmpeg                            # macOS
```

## Install

**One command, on Linux or macOS:**

```sh
curl -fsSL https://raw.githubusercontent.com/sumdahl/geet/main/install.sh | sh
```

The [installer](install.sh) first shows your system and which of geet's tools are already installed, then a menu you drive with the arrow keys:

```
  Tools geet uses
  ✓ yt-dlp         2026.08.19     finds and downloads the audio
  ✗ ffmpeg         required       converts and tags the files
  ✗ fzf            required       the geet search menu
  · wl-clipboard   optional       geet watch: reads the clipboard

  What should be installed?
  ❯ Recommended      geet, the required tools and the optional ones
    Required only    geet with yt-dlp, ffmpeg and fzf
    Custom           choose each tool
    geet only        no tools
    Quit             install nothing
```

- **Required tools:** yt-dlp, ffmpeg and fzf. **Optional, Linux only:** wl-clipboard and libnotify, which `geet watch` uses.
- **Before anything runs,** it shows the plan with the exact commands, `sudo` included, and asks to go ahead. It then offers to add `~/.local/bin` to your `PATH`.
- **Where the tools come from:**
  - macOS: Homebrew, `brew install` (all three are formulae, not casks). If Homebrew is missing, it offers to install it first.
  - Arch: pacman. Fedora: dnf, where ffmpeg is `ffmpeg-free`. Alpine: apk.
  - Debian and Ubuntu: apt, except yt-dlp. Their yt-dlp package is too old for YouTube, so it gets yt-dlp's official build instead.
- **geet itself** is the release binary for your system (Linux or macOS, x86-64 or arm64), checked against the release's `SHA256SUMS`, installed to `~/.local/bin` without sudo. Run the installer again to update.

Without a terminal (scripts, CI) it asks nothing: it installs geet, and only reports missing tools. Options go after `sh -s --`:

```sh
curl -fsSL https://raw.githubusercontent.com/sumdahl/geet/main/install.sh | sh -s -- --yes        # geet and every tool, no questions
curl -fsSL https://raw.githubusercontent.com/sumdahl/geet/main/install.sh | sh -s -- --required   # geet, yt-dlp, ffmpeg, fzf
curl -fsSL https://raw.githubusercontent.com/sumdahl/geet/main/install.sh | sh -s -- --no-deps    # geet only
```

`--version v0.2.0` pins a release, `--dir DIR` installs elsewhere, and `--help` lists everything. The same settings are available as `GEET_VERSION`, `GEET_INSTALL_DIR` and `GEET_DEPS` (`all`, `required` or `none`). Then run `geet doctor` to check that everything works.

**Or with Homebrew** (macOS or Linux), which handles upgrades along with the rest of your packages:

```sh
brew install sumdahl/geet/geet
```

It brings yt-dlp and ffmpeg with it. `brew upgrade` then keeps geet current.

Or download a binary yourself: `geet-linux-amd64`, `geet-linux-arm64`, `geet-darwin-amd64` or `geet-darwin-arm64` from [Releases](https://github.com/sumdahl/geet/releases). Each is a single static file. On macOS, a binary saved from a web browser is quarantined, and macOS won't open it until you run `xattr -d com.apple.quarantine <file>`. `exec format error` means the binary is for another system, such as the Linux one on a Mac.

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
geet play [words… | link]          # play your library (or a song), with a spectrum and lyrics
geet trending                      # what people are playing now: pick one and listen
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
  wl-paste | geet download "https://open.spotify.com/playlist/…" --tracks
```

Ctrl+A then Ctrl+C in the Spotify desktop app copies a link for every song, and `--tracks` reads them from the clipboard. The playlist link only names the folder. Songs already downloaded are skipped, so re-running this after a normal download fetches just the missing ones. `--tracks=FILE` reads a file instead, with one link per line (`#` starts a comment). Either form works without a playlist link, saving into `output` directly.

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
```

Past your bandwidth, more jobs just split it.

`resolve_jobs` (default 8) is how many songs are read from Spotify and looked up on YouTube at once. Leave it at 8. On a 42-song playlist with `jobs = 16`, `resolve_jobs = 24` read the playlist faster (3.2 s against 5.8 s) but didn't finish sooner (40 s against 33 s), because downloading takes most of the time. Reading many Spotify pages at once is also what makes Spotify rate-limit you (HTTP 429). geet waits that out, but the waits cost more than the extra parallelism saves. Raise it only for long lists on a connection where reading is visibly the slow part.

Songs read from Spotify are remembered for 30 days (`spotify.cache_days`, in `~/.cache/geet/spotify.json`), so reading a playlist again is almost instant: re-reading that 42-song playlist took 0.4 s instead of 6.2 s. Re-running a playlist to pick up new songs, or to retry skipped ones, then asks Spotify only about the songs it hasn't seen. Requests are also spread out to at most 10 a second, because bursts are what Spotify rate-limits. Set `spotify.cache_days = 0` to always read fresh metadata.

**"YouTube wants you to confirm you're not a bot"** means YouTube is limiting your IP, usually after many downloads in a short time. geet shows the fix once and doesn't retry those songs, since retrying only prolongs the block. Waiting an hour and lowering `jobs` usually clears it. The lasting fix is to send your browser's YouTube sign-in:

```toml
[youtube]
cookies_from_browser = "auto"   # your default browser; or name one: brave, chromium, chrome, firefox, vivaldi, edge, opera
```

- `auto` finds your default browser. For Chromium-based browsers geet also adds the keyring that holds the cookie key (`brave+gnomekeyring` on Omarchy). Without it, yt-dlp can't decrypt any cookies outside GNOME or KDE and silently sends none.
- It's off by default: with cookies, YouTube sees the downloads as your account's, and heavy downloading could get that account flagged.
- **Explicit songs come as the explicit version by default.** Spotify marks them, and uploads marked clean, radio edit or censored score lower. YouTube often age-restricts the official explicit upload, playing it only to a signed-in, age-verified account. When it does, geet tries, in order:
  1. Other uploads of the same recording that passed matching, then exact re-uploads: the full title and within 2 s of the length, even from a channel that doesn't name the artist.
  2. **The clean edit, as the last resort.** It's found in the Apple catalog ("Tonight (I'm Lovin' You)" for "Tonight (I'm Fuckin' You)", "umean" for "fukumean"). It's saved with " (Clean)" added to its title tag and a warning, so a clean file is never mistaken for the explicit one.

  geet also says once how to get the explicit version from the official upload: set `cookies_from_browser = "auto"`, with a YouTube account whose age is verified. A remix, cover or live version never stands in for the song.
- `geet doctor` shows what `auto` resolved to and checks that the cookies decrypt. It also checks that audio downloads work, not just search, because the block allows search. Interrupting with Ctrl+C is always safe: no partial files are left, and re-running the same command continues where it stopped.

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

- **Both versions of explicit songs are listed, explicit first:**
  ```
  $ geet search enrique iglesias tonight
    1  Tonight (I'm Fuckin' You) — Enrique Iglesias · Euphoria 3:54 [E]
    2  Tonight (I'm Lovin' You) [feat. Ludacris & DJ Frank E] — Enrique Iglesias · Euphoria (Collector's Edition) (2010) 3:51
  ```
  `[E]` marks the explicit version, `(clean)` a clean edit. When a song's clean edit would rank higher (it's on more albums), the explicit original is moved above it.
- **Songs come from two keyless catalogs at once,** because each lacks what the other has:
  - **Apple** (any country's store, `search.country`, default `US`) often lists an explicit song only as its clean edit.
  - **Deezer** lists explicit originals, but its search is region-filtered (from Nepal it hides some major-label songs).

  The same song from both is shown once, and if one catalog doesn't answer, the other's results still come. Nepali and other regional music is included.
- The raw catalog order puts covers above originals, so geet merges album editions of the same recording and re-ranks:
  - Covers, remixes, instrumentals, karaoke, type beats, slowed and lullaby versions sink.
  - The original (listed on the most editions) rises.
  - A result by an artist you named ranks above other artists' uploads that only mention them in the title ("Fukumean Gunna" by someone else).
- The menu is [fzf](https://github.com/junegunn/fzf), which ships with Omarchy: type to filter (`weeknd` narrows to that artist), Tab to pick several songs, which then download in parallel. Without fzf you get a numbered list (`1`, `1 3` or `2-4`).
- Before downloading, geet lists your picks and asks `Download N songs? [Y/n]`, so a stray Enter never starts a download. Skip the question with `-y` / `--yes`, or set `search.confirm = false`. `--pick` never asks.
- To get a specific artist's version, add the artist to the search: `geet search blinding lights weeknd`.
- Scripting: `geet search <words> --pick 1` chooses without a menu, and `geet search <words> --json` lists results (each with a `ref`, and `"explicit": true` or `"clean": true`) without downloading. `geet download itunes:<id>` / `deezer:<id>`, Apple Music song links and Deezer track links download directly.
- Limits:
  - Apple's public catalog lists some explicit songs only as clean edits, whose titles are censored too ("umean" for Gunna's "fukumean"). These are marked `(clean)`, and matching still finds the right upload.
  - A few labels' catalogs aren't searchable from some regions. For those, use the Spotify link.

### Clipboard daemon

`geet watch` downloads every Spotify link you copy, until you stop it with Ctrl+C:

```
$ geet watch
Watching the clipboard: copy a Spotify link to download it. Ctrl+C stops.
Copied https://open.spotify.com/album/1DFixLWuPkv3KT3TnV35m3
…
✓ Emotion (Deluxe): 15 saved → /home/you/Music
Watching the clipboard.
```

- Track, album and playlist links all work, as do Apple Music song links. Several songs selected in Spotify and copied together (Ctrl+C) download as one batch.
- Links download one after another, in the order you copied them. Each still uses the full download pipeline.
- A desktop notification shows the cover when a download starts, then changes to the result: saved, already in your library, some failed, or failed with the reason. Turn it off with `watch.notify = false`.
- Whatever was on the clipboard when `watch` started is ignored, so an old link doesn't start a download. Copying the same link again downloads it again, which costs nothing when the files already exist.
- A failed link only fails that link: the daemon keeps watching. It exits `0` when stopped with Ctrl+C or SIGTERM, and `2` if it can't read the clipboard at all (no `wl-paste`, or no Wayland session).
- It checks the clipboard every `watch.interval` (1s). Wayland only: X11 clipboards (xclip, xsel) aren't read.

If you'd rather not keep a process running, bind a key to download whatever is on the clipboard. Without a terminal to watch, the `notify-send` calls say how it went. On Omarchy 4, in `~/.config/hypr/bindings.lua` (`SUPER + SHIFT + Y` is Omarchy's YouTube shortcut, so unbind it first or pick another key):

```lua
hl.unbind("SUPER + SHIFT + Y")
o.bind("SUPER + SHIFT + Y", "Download copied song",
  [[geet download "$(wl-paste)" && notify-send geet "Download finished" || notify-send -u critical geet "Download failed"]])
```

With a classic `hyprland.conf`:

```
bindd = SUPER SHIFT, Y, Download copied song, exec, geet download "$(wl-paste)" && notify-send geet "Download finished" || notify-send -u critical geet "Download failed"
```

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
  ✓ config     ~/.config/geet/config.toml · opus · jobs 16, resolve_jobs 8
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

### Playing (`geet play`)

geet is a player as well as a downloader, and it plays straight away:

```sh
geet play                          # your whole library, newest first
geet play bartika najeek           # whatever in your library matches those words
geet play <spotify-url>            # a song, album or playlist — starts in seconds
geet play ~/Music/90s-mix          # a folder
```

**Nothing is downloaded to listen.** A song you already have plays from the
library, offline and instantly. Anything else streams: geet finds the same
YouTube match a download would use and plays it, which takes a couple of
seconds. Press **`d`** while it plays to keep a proper tagged copy — the
download runs in the background and the music doesn't stop. Set
`player.stream = false` to go back to downloading before playing.

The screen shows the song, how far along it is, a spectrum drawn from the
audio itself, and lyrics that follow the beat:

```
  Najeek                                                     ▶  1:42 / 5:12
  Bartika Eam Rai  ·  Bimbaakash
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━─────────────────────────────────────────

     ▁▃▅▇█▇▅▃▁▂▄▆█▆▄▂▁▃▅▇                 timi sanga bitayeko
     ▂▄▆█▆▄▂▁▃▅▇█▇▅▃▁▂▄▆                ▸ ti din haru najeek
     ▃▅▇█▇▅▃▁▂▄▆█▆▄▂▁▃▅▇                  pheri farkera aaunchha ki

  3 of 18                     space pause  ·  ←/→ seek  ·  n next  ·  q quit
```

| Key | What it does |
|---|---|
| `space` | pause and resume |
| `←` `→` | back or forward 5 seconds (`shift` for 30) |
| `n` `p` | next song, or previous (`p` restarts the song first) |
| `d` | keep the streaming song: download it, tagged, without interrupting playback |
| `v` `y` | show or hide the spectrum, the lyrics |
| `q` | quit |

- **Lyrics** come from [LRCLIB](https://lrclib.net), which needs no account.
  Plenty of songs have none — instrumentals, new releases, local-language
  music — and that is not an error: the pane says so and the spectrum takes
  the space. Lyrics without timings are shown and marked as such. What is
  found (and what isn't) is cached in `~/.cache/geet/lyrics`.
- **The spectrum** is geet's own: ffmpeg decodes the playing file to raw
  audio and geet runs an FFT over it, so there is nothing else to install.
- **mpv** plays the audio through its control socket, which is what makes
  the position exact enough for lyrics. With `mpv-mpris` installed, whatever
  shows your now-playing (the Omarchy bar, playerctl) sees geet too.
- `geet play --json` plays with no screen and streams what it is doing, for
  the Omarchy plugin.

### What's trending (`geet trending`)

```sh
geet trending                      # pick one from the menu and it plays
geet trending --json               # the same list, for a front end
geet trending --play 1,2           # listen to the top two, no menu
geet trending --pick 3             # download the third
geet trending --limit 50 --refresh # a longer list, read again
```

- Songs come from **Deezer's chart**, which is localised by where you are —
  from Nepal that mixes Nepali songs in with the global hits — topped up
  from **Apple's most-played feed** for your `search.country` store. Set
  `trending.source` to `deezer` or `apple` to use just one.
- The list is cached for `trending.cache_for` (6 hours by default), so
  opening it again is instant. `--refresh` reads it anew, and if the
  network fails geet shows the last chart it has rather than nothing.
- Picking a song **plays it straight away** (streamed, as everywhere else);
  `d` while it plays keeps it.

## Configuration

Every setting can be given in three ways. When the same setting is set in more than one place, the flag wins over the environment variable, which wins over the config file:

1. **Flag:** `--youtube-search-results 8`
2. **Environment variable:** `GEET_YOUTUBE_SEARCH_RESULTS=8`
3. **Config file:** `~/.config/geet/config.toml` on Linux, `~/Library/Application Support/geet/config.toml` on macOS (or `$GEET_CONFIG`, or `--config`). `geet config path` prints where it is.

**The installer creates the config file** with every setting listed at its default, each under a short explanation and commented out. To change a setting, remove the `# ` in front of its line and edit the value:

```toml
# ---- General ----------------------------------------------------------

# concurrent downloads
jobs = 16                       # was: # jobs = 4

# ---- YouTube ----------------------------------------------------------

# send your YouTube sign-in from a browser, to get past "confirm you're not
# a bot": auto (the default browser), or brave, chromium, chrome, firefox, …
youtube.cookies_from_browser = "auto"
```

- **Commented lines keep geet's defaults,** including when a new version improves them.
- **Keys are written in full** (`youtube.cookies_from_browser`), so a line works wherever you put it, even appended at the end.
- **Your file is never overwritten,** by the installer or anything else. Without the installer, or to start over, run `geet config init` (`--force` replaces an existing file).
- **Mistakes are errors, not silently ignored.** An unknown key is an error, and a setting placed under the wrong `[section]` gets a hint saying where it belongs.
- `geet config` shows the values in effect.

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
| `resolve_jobs` | `--resolve-jobs` | int | `8` | Songs read from Spotify and looked up on YouTube at once. Higher risks Spotify rate limits. |
| `spotify.client_id` | `--spotify-client-id` | string | | Optional Web API credentials (Premium only) |
| `spotify.cache_days` | `--spotify-cache-days` | int | `30` | Days to remember songs read from Spotify's pages. `0` turns the cache off. |
| `spotify.client_secret` | `--spotify-client-secret` | string | | |
| `youtube.search_query` | `--youtube-search-query` | string | `{artists} - {title}` | YouTube search text |
| `youtube.fallback_query` | `--youtube-fallback-query` | string | `{artists} - {title} audio` | Second search when nothing matches; empty disables it |
| `youtube.search_results` | `--youtube-search-results` | int | `5` | Results scored per search |
| `youtube.max_duration_diff` | `--youtube-max-duration-diff` | duration | `10s` | Reject uploads whose length differs more than this |
| `youtube.cookies_file` | `--youtube-cookies-file` | string | | Netscape cookies file for yt-dlp |
| `youtube.cookies_from_browser` | `--youtube-cookies-from-browser` | string | *(off)* | Send your YouTube sign-in to get past the bot check: `auto` (default browser) or a browser name; the keyring is added automatically |
| `youtube.title_fallback` | `--youtube-title-fallback` | bool | `true` | Last resort: match by title and exact length alone |
| `youtube.music_fallback` | `--youtube-music-fallback` | bool | `true` | When YouTube search finds no match, look on YouTube Music |
| `youtube.extra_args` | `--youtube-extra-args` | list | | Extra yt-dlp arguments, space-separated |
| `search.country` | `--search-country` | string | `US` | iTunes store `geet search` looks in |
| `search.limit` | `--search-limit` | int | `15` | Results offered to pick from |
| `search.picker` | `--search-picker` | string | `auto` | `auto` (fzf if installed), `fzf` or `list` |
| `search.confirm` | `--search-confirm` | bool | `true` | Ask before downloading menu picks |
| `watch.interval` | `--watch-interval` | duration | `1s` | How often `watch` checks the clipboard |
| `watch.notify` | `--watch-notify` | bool | `true` | Desktop notifications from `watch` |
| `tools.yt_dlp` | `--tools-yt-dlp` | string | `yt-dlp` | Executables |
| `tools.ffmpeg` | `--tools-ffmpeg` | string | `ffmpeg` | |
| `tools.ffprobe` | `--tools-ffprobe` | string | `ffprobe` | |
| `tools.wl_paste` | `--tools-wl-paste` | string | `wl-paste` | Clipboard reader for `watch` |
| `tools.notify_send` | `--tools-notify-send` | string | `notify-send` | Notifications for `watch` |

`geet config settings --json` prints this table as JSON (key, flag, env variable, type, default, current value, description), so tools can build a settings UI without hard-coding it.

## JSON output

With `--json`, stdout carries only NDJSON: one event per line, as each track moves through `resolved → downloading → tagging → done`, or `failed`. Human-readable output goes to stderr only.

```json
{"track":"Gunna - fukumean","stage":"done","spotify_id":"4rXLjWdF2ZZpXCVTfWcshS","index":1,"total":1,
 "youtube_url":"https://www.youtube.com/watch?v=l21wGxlWwPw","path":"/home/you/Music/fukumean - Gunna.opus"}
```

`reading` events report progress before any track starts. Repeated `downloading` events carry a `progress` from 0.1 to 1. Tracks run concurrently, so key events by `index`.

`geet watch --json` streams the same events for as long as it runs. Each copied link is announced by a `queued` event and ends with exactly one `finished` event, and all events in between carry its `job` number and `source` link:

```json
{"track":"","stage":"queued","job":1,"source":"https://open.spotify.com/album/1DFixLWuPkv3KT3TnV35m3"}
{"track":"","stage":"finished","job":1,"source":"https://open.spotify.com/album/1DFixLWuPkv3KT3TnV35m3",
 "name":"Emotion (Deluxe)","total":15,"path":"/home/you/Music","counts":{"saved":15,"existing":0,"failed":0}}
```

The full schema and its compatibility rules are in [docs/03-communication-contract.md](docs/03-communication-contract.md).

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
| `cmd/geet` | CLI: subcommands, flags generated from the settings list, the per-track stages (`download.go`), search and the fzf/numbered picker (`search.go`, `pick.go`), the clipboard daemon (`watch.go`), the stderr display (`ui.go`) |
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
| `internal/clipboard` | Reads the Wayland clipboard with `wl-paste` and reports changes, for `watch` |
| `internal/notify` | Desktop notifications through `notify-send`, with markup escaped |
| `internal/pipeline` | Generic staged worker pools with bounded channels and shared cancellation |
| `internal/textnorm` | Title and name normalization shared by matching: accent folding, censored-word wildcards, Devanagari-safe |

### Why it's built this way

- **Keyless metadata.** The Web API now requires the app owner to have Spotify Premium. Spotify's embed pages carry the track list, durations and cover in their `__NEXT_DATA__` JSON. The track page's `music:*` meta tags (served only to link-preview crawlers) carry the album and track number. What's still missing, ISRC and disc number, comes from Deezer. Deezer filters search results by country, so ISRCs are sometimes missing; the other tags are unaffected. Public playlist pages list at most 100 tracks.
- **Matching** (`internal/youtube/score.go`):
  - Candidates are rejected if their length differs by more than 10 s, less than 60% of the song's base title matches, or no artist is named.
  - Scores reward the official, "Topic" or VEVO channel, "audio" uploads, closeness in length, and search rank.
  - Variant words (live, cover, remix, sped up, …) are penalized unless the Spotify title has them too.
  - If nothing matches, a second search asks for the "audio" upload, which catches official videos whose intro pushes them past the length limit.
  - If that finds nothing either, geet searches **YouTube Music's songs** (`youtube.music_fallback`, on by default). YouTube Music lists the official studio audio (the artist's "- Topic" channel) at the album's exact length, where regular search buries it under music videos with intros, lyric uploads and fan edits. Romanized titles (Nepali "Maayajastai" / "Maayaajastai"), classical pieces and small artists are the usual cases. Only the song results whose titles fit are opened, about 4 s for that song only, and they're scored by the same rules. So a performance by another pianist, or someone else's slowed edit, is still rejected rather than saved under the wrong name.
  - `$` in a name reads as `s` (A$AP → ASAP), words split differently still match ("1Train" / "1 Train"), and an artist name at the start of a channel name counts (`ASAPROCKYUPTOWN`).
  - **An artist known by a shorter name:** Spotify's "Kush Band Nepal" is "KUSH" on YouTube. If no upload names the artist in full, the distinctive part of the name counts, without words like *band, the, official, music, Nepal*. It scores below a full name, so an upload that names the artist in full still wins.
  - **By title alone, as the last resort** (`youtube.title_fallback`): when every search has failed, geet searches the song's title alone. It accepts only an upload with the full title, within 2 s of the length, and no variant, karaoke, cover or TV-show words. Titles of one word are excluded. Such a song carries a warning saying how it was matched.
  - Every change to matching is checked against the real searches of a 201-song playlist (`internal/youtube/testdata/corpus`). The test fails if any existing score or pick changes.
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
- The design docs in [`docs/`](docs/) are the spec and roadmap. The Omarchy plugin comes next.
- Lint with [golangci-lint](https://golangci-lint.run/) v2 (`golangci-lint run`, configured in `.golangci.yml`).

### Continuous integration and releases

Every push to `main` and every pull request runs [CI](.github/workflows/ci.yml): golangci-lint, then `go vet` and `go test -race` on both Linux and macOS, and a smoke test of the built binary. A separate job runs `install.sh` against the latest release on a real Linux machine and a real Mac. Whenever `install.sh` changes, and weekly, the [installer workflow](.github/workflows/installer.yml) installs geet and every tool with `--yes` on a real Mac (Homebrew) and in fresh Ubuntu, Debian, Fedora, Arch and Alpine containers.

Releases are built by [GoReleaser](https://goreleaser.com/) in [release.yml](.github/workflows/release.yml), never by hand. To release version 0.3.0:

1. In `CHANGELOG.md`, rename `## [Unreleased]` to `## [0.3.0] - <date>`, start a new empty `## [Unreleased]` above it, and update the compare links at the bottom.
2. Commit, push to `main`, and wait for CI to pass.
3. Tag and push the tag:
   ```sh
   git tag -a v0.3.0 -m "geet v0.3.0"
   git push origin v0.3.0
   ```

The tag starts the release workflow:
- It runs all of CI again and checks that the tag is on `main`.
- It builds `geet-{linux,darwin}-{amd64,arm64}` with the version stamped in, plus `SHA256SUMS`.
- It publishes the GitHub release, with that version's `CHANGELOG.md` section as its notes. A tag without a changelog section fails.
- It signs [build provenance](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) for every binary.

The installer and "latest" links then serve the new version. Tags with a suffix, such as `v0.3.0-rc.1`, become pre-releases, which "latest" skips.

To see what a release would build, without publishing anything: `goreleaser release --snapshot --clean`, whose output goes to `build/`.

## Legal

This tool is for personal use with music you have the right to download. You're responsible for complying with YouTube's and Spotify's terms of service and with copyright law where you live.

## License

[MIT](LICENSE) © 2026 Sumiran Dahal. geet runs `yt-dlp` and `ffmpeg` as separate programs. Their own licenses apply to them, and neither is bundled with geet.
