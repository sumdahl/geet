# Publishing geet to the AUR and Homebrew

Research notes, 2026-09-18. Every claim cites a primary source. **⚠** marks places where a source contradicts common belief, or where something changed recently.

## 0. What geet looks like today (from the repo)

| Fact | Where | Why it matters |
|---|---|---|
| Module `github.com/sumdahl/geet`, `go 1.27.1` | `go.mod` | See the ⚠ on toolchains below |
| Version: `var version = ""` in `package main`, set with `-ldflags "-X main.version=…"`. Unstamped builds fall back to `debug.ReadBuildInfo()` (the module version, plus `vcs.revision`/`vcs.time` when built from a git checkout). It prints the value as given, e.g. `geet v0.2.0` | `cmd/geet/main.go:36-42, 189-215` | Inject **`v`-prefixed** tags. Homebrew's `#{version}` and AUR's `$pkgver` drop the `v`, so write `v#{version}` / `v${pkgver}` |
| No cgo in geet's own code. Only stdlib `net` has cgo files, and `CGO_ENABLED=0` builds work (the current release binaries are static) | `go list -deps` | The Arch `-linkmode=external` flag just makes a dynamic PIE, which is fine |
| Runtime tools: `yt-dlp`, `ffmpeg`, and `ffprobe` (which ships with ffmpeg). `fzf` is optional (for `search`). `wl-paste`/`notify-send` for `watch`. `xdg-settings` and `pgrep` only for `youtube.cookies_from_browser = auto` | `internal/ytdlp/cookies.go`, `cmd/geet/pick.go`, `internal/{clipboard,notify}` | Sorts dependencies into depends and optdepends |
| `LICENSE`: MIT, © 2026 Sumiran Dahal | `LICENSE` | Already present, so no code change needed |
| No `.goreleaser.yaml` and no `.github/workflows`. Releases v0.1.0 and v0.2.0 were uploaded by hand as raw binaries (`geet-linux-{amd64,arm64}` + `SHA256SUMS`) built into a git-ignored `dist/` | `dist/`, `.gitignore` | ⚠ GoReleaser's default output dir is also `./dist`. It refuses to run on a non-empty dir without `--clean`, and `--clean` deletes it ([dist.go](https://github.com/goreleaser/goreleaser/blob/main/internal/pipe/dist/dist.go), [dist docs](https://goreleaser.com/customization/general/dist/)) |
| Cross-builds: `darwin/arm64` and `darwin/amd64` build, and `GOOS=darwin go vet ./...` (tests included) is clean. `windows` fails (`syscall.Stat_t` in `internal/index/index.go:126`) | local check | macOS is possible. Windows is out of scope |
| The Arch recipe (below) builds and `go test ./...` passes with Arch's `GOFLAGS` | local check | `check()` is safe to enable, because the tests are offline by project rule (CLAUDE.md) |

⚠ **Go toolchain floor.** The `go 1.27.1` directive is a *minimum*. Homebrew forces `GOTOOLCHAIN=local` during builds ([super.rb L120](https://github.com/Homebrew/brew/blob/main/Library/Homebrew/extend/ENV/super.rb)), so a formula build fails whenever brew's `go` is older than go.mod asks for ([Go toolchains](https://go.dev/doc/toolchain)). Today both Arch `go` and brew `go` are 1.27.1 ([Arch](https://archlinux.org/packages/extra/x86_64/go/), [brew](https://formulae.brew.sh/formula/go)), so this works only because they happen to match. Keep the directive at the real minimum geet needs, not the version you happen to have.

## 1. AUR

### 1.1 Build flags ([Arch Go package guidelines](https://manual.archlinux.page/package-guidelines/go/))

⚠ **The page moved.** `wiki.archlinux.org/title/Go_package_guidelines` now redirects to the Developer Manual at `manual.archlinux.page/package-guidelines/go/`. It was migrated on 2026-06-07 ([wiki history](https://wiki.archlinux.org/index.php?title=Go_package_guidelines&action=history)). The wiki's bot protection (Anubis) also blocks non-browser fetches, though `?action=raw` works.

The manual requires:
- `export CGO_CPPFLAGS="${CPPFLAGS}" CGO_CFLAGS="${CFLAGS}" CGO_CXXFLAGS="${CXXFLAGS}" CGO_LDFLAGS="${LDFLAGS}"`, because Go does not pass the system hardening flags to cgo on its own.
- `export GOFLAGS="-buildmode=pie -trimpath -ldflags=-linkmode=external -mod=readonly -modcacherw"`. PIE is for hardening, `-trimpath` for reproducibility, `-mod=readonly` so go.mod is never changed, and `-modcacherw` so the module cache is writable.
- `prepare()`: `export GOPATH="${srcdir}"; go mod download -modcacherw`.
- The sample `check()` runs `go test ./...`.
- Naming: use the plain program name (`go-<name>` only for tools tied to the Go ecosystem), all lowercase.

⚠ **ldflags gotcha.** Flags on the command line *override* `GOFLAGS` (`go help environment`: "Flags listed on the command line are applied after this list and therefore override it"). Passing `-ldflags "-X main.version=…"` on the command line therefore silently drops the `-ldflags=-linkmode=external` from `GOFLAGS`. Leave `-ldflags` out of `GOFLAGS` and put everything in one CLI `-ldflags`, as below.

### 1.2 Which package(s) ([AUR submission guidelines](https://wiki.archlinux.org/title/AUR_submission_guidelines))

- `geet`: builds a tagged release from source. "Packages that build from source using a specific version do not use a suffix."
- `geet-bin`: "Packages that use prebuilt deliverables, when the sources are available, **must** use the `-bin` suffix."
- `geet-git`: builds from VCS HEAD, and that needs the `-git` suffix. Don't commit bare `pkgver` bumps to it. It adds little value for a project that tags releases.
- `x86_64` must be supported. Don't create duplicates. Add a `# Maintainer:` line.
- ⚠ The AUR repo itself should contain a `LICENSE` (0BSD encouraged) and/or `REUSE.toml`. This is a fairly new rule, and GoReleaser does not push such a file (see §3).
- **Name availability (checked 2026-09-18 via the [AUR RPC](https://aur.archlinux.org/rpc/v5/info?arg[]=geet&arg[]=geet-bin&arg[]=geet-git)):** `geet`, `geet-bin` and `geet-git` are all free. There is no `geet` in the [official repos](https://archlinux.org/packages/?q=geet) either, which the rules also require checking. The only keyword hit is `codea-geeteedee`, which is unrelated.
- The dependency packages exist in `extra`: `yt-dlp`, `ffmpeg`, `wl-clipboard`, `libnotify`, `fzf`, `xdg-utils`, `go` ([packages search](https://archlinux.org/packages/)).

### 1.3 depends vs optdepends ([PKGBUILD](https://wiki.archlinux.org/title/PKGBUILD#Dependencies), [Arch package guidelines](https://wiki.archlinux.org/title/Arch_package_guidelines))

`optdepends` are "not needed for the software to function, but provide additional features", written as `'pkg: reason'`.

```
depends=('yt-dlp' 'ffmpeg')            # download/search can't work without them (ffprobe ships in ffmpeg)
makedepends=('go')                     # source package only
optdepends=('fzf: interactive picker for geet search'
            'wl-clipboard: clipboard watching (geet watch, wl-paste | geet download --tracks)'
            'libnotify: desktop notifications (geet watch)'
            'xdg-utils: youtube.cookies_from_browser = auto')
```

Don't list `glibc`: it is always present, and `-linkmode=external` links it dynamically. `namcap` flags missing library deps ([Arch package guidelines](https://wiki.archlinux.org/title/Arch_package_guidelines)).

### 1.4 Source PKGBUILD for `geet` (follows the manual, adapted for geet)

```bash
# Maintainer: Sumiran Dahal <… at … dot …>
pkgname=geet
pkgver=0.2.0
pkgrel=1
pkgdesc='Download Spotify tracks, albums and playlists as tagged audio (yt-dlp + ffmpeg)'
arch=('x86_64' 'aarch64')
url='https://github.com/sumdahl/geet'
license=('MIT')
depends=('yt-dlp' 'ffmpeg')
makedepends=('go')
optdepends=(…as above…)
source=("$pkgname-$pkgver.tar.gz::$url/archive/refs/tags/v$pkgver.tar.gz")
sha256sums=('…')   # updpkgsums

prepare() {
  cd "$pkgname-$pkgver"
  export GOPATH="$srcdir"
  go mod download -modcacherw
}
build() {
  cd "$pkgname-$pkgver"
  export CGO_CPPFLAGS="$CPPFLAGS" CGO_CFLAGS="$CFLAGS" CGO_CXXFLAGS="$CXXFLAGS" CGO_LDFLAGS="$LDFLAGS"
  export GOPATH="$srcdir"
  export GOFLAGS="-buildmode=pie -trimpath -mod=readonly -modcacherw"
  go build -ldflags "-linkmode=external -X main.version=v$pkgver" -o build/geet ./cmd/geet
}
check() {
  cd "$pkgname-$pkgver"
  export GOPATH="$srcdir"
  go test ./...
}
package() {
  cd "$pkgname-$pkgver"
  install -Dm755 build/geet "$pkgdir/usr/bin/geet"
  install -Dm644 LICENSE "$pkgdir/usr/share/licenses/$pkgname/LICENSE"
}
```

MIT is not one of Arch's common licenses, so you must install the license file under `/usr/share/licenses/$pkgname/` ([PKGBUILD#license](https://wiki.archlinux.org/title/PKGBUILD#license)).

### 1.5 Account, key and first push ([AUR submission guidelines](https://wiki.archlinux.org/title/AUR_submission_guidelines))

1. Register at aur.archlinux.org. Make a **dedicated** key with `ssh-keygen -f ~/.ssh/aur` (dedicated so you can revoke it on its own), and paste the public half into *My Account*.
2. Add this to `~/.ssh/config`: `Host aur.archlinux.org` / `IdentityFile ~/.ssh/aur` / `User aur`.
3. `git -c init.defaultBranch=master clone ssh://aur@aur.archlinux.org/geet.git`. The "empty repository" warning is expected.
4. `makepkg --printsrcinfo > .SRCINFO`, then `git add PKGBUILD .SRCINFO LICENSE`, commit and push. Every commit that changes metadata must carry a regenerated `.SRCINFO`, and the AUR only accepts pushes to `master`.
5. Commits carry your global git name and email, and these are "very difficult to change" after pushing. Set `git config user.name/email` per repo if needed.
6. Test locally with `makepkg -si` and `namcap PKGBUILD *.pkg.tar.zst` first ([Arch package guidelines](https://wiki.archlinux.org/title/Arch_package_guidelines)).

### 1.6 Automating AUR updates

The AUR guidelines say automation is allowed, but "can not replace manual intervention… Automated PKGBUILD updates are used at your own risk and any malfunctioning accounts and their packages may be removed without prior notice" ([guidelines, Maintaining packages](https://wiki.archlinux.org/title/AUR_submission_guidelines#Maintaining_packages)).

| Option | What it does | Notes |
|---|---|---|
| **GoReleaser `aurs`** ([docs](https://goreleaser.com/customization/publish/aur/)) | Generates a `-bin` PKGBUILD + `.SRCINFO` from the release archives (it *forces* the `-bin` suffix) and pushes to `git_url` over SSH | Default `provides`/`conflicts` = `geet`, which is right. The default `package()` installs only the binary, so override it to install `LICENSE` too |
| **GoReleaser `aur_sources`** (since v2.5, [docs](https://goreleaser.com/customization/publish/aursources/)) | Source PKGBUILD from GoReleaser's `source` archive (needs `source.enabled: true`). It strips any `-bin` suffix. The default makedepends are `go git` | ⚠ **No `check()` field.** The template has only prepare/build/package ([tmpl.go](https://github.com/goreleaser/goreleaser/blob/main/internal/pipe/aursources/tmpl.go)). ⚠ Its default `provides`/`conflicts` are the project name, i.e. the package conflicts with its own name, so override both |
| **[KSXGitHub/github-actions-deploy-aur](https://github.com/KSXGitHub/github-actions-deploy-aur)** v4.2.0 (2026-07-14, active) | Pushes a PKGBUILD *you* provide. Runs `makepkg --printsrcinfo` for you, with optional `updpkgsums: true`, a `test` build, and `assets` (e.g. LICENSE) | "does not generate PKGBUILD for you". Suits a hand-written source PKGBUILD that keeps `check()` |

Secrets (all three options): the **private** half of a new, **passphrase-less** AUR key (GoReleaser: "the key must not be password-protected"), stored as a GitHub Actions secret such as `AUR_SSH_PRIVATE_KEY`, with its public half added to the AUR account. Keep it separate from your personal AUR key, so a leak can be revoked alone.

⚠ **AUR LICENSE file:** the GoReleaser AUR pipes write only `PKGBUILD`, `.SRCINFO` and optionally `<name>.install` ([aursources.go](https://github.com/goreleaser/goreleaser/blob/main/internal/pipe/aursources/aursources.go)). Commit a `LICENSE` (0BSD) into each AUR repo once by hand. GoReleaser clones the repo and commits only its own files, so the LICENSE should survive later pushes. Verify this after the first automated release.

## 2. Homebrew

### 2.1 homebrew-core: not yet ([Package Acceptance Policy](https://docs.brew.sh/Package-Acceptance-Policy), reviewed 2026-07-18)

- Notability: **≥30 forks, 30 watchers or 75 stars**. For a **self-submission by the repo owner**, the bar is **≥90 forks, 90 watchers or 225 stars**. A repository **less than 30 days old** is normally ineligible.
- ⚠ These thresholds now live in the shared *Package Acceptance Policy* page, not in *Acceptable Formulae* (restructured in 2026). Older blog posts quoting "Acceptable Formulae → Niche stuff" are out of date.
- Core also requires building and testing on the **macOS and Linux CI matrix**, a stable immutable tag, a DFSG-compatible license and a source build ([Acceptable Formulae](https://docs.brew.sh/Acceptable-Formulae)). It keeps a "Project risk" clause (legal/infra risk may lead to rejection). A YouTube-ripping tool could draw scrutiny there, though `yt-dlp` itself is in core ([formula](https://formulae.brew.sh/formula/yt-dlp)).
- **Realistic path: a personal tap.** Software that doesn't meet the criteria "can generally be maintained in a third-party tap" (same page).

### 2.2 The tap ([How to Create and Maintain a Tap](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap))

- Repo `sumdahl/homebrew-tap`. The `homebrew-` prefix enables the short name, so `brew tap sumdahl/tap` maps to `github.com/sumdahl/homebrew-tap`. Formulae go in `Formula/`, casks in `Casks/`. `brew tap-new sumdahl/homebrew-tap` scaffolds it, including optional bottle-building workflows (`brew pr-pull`).
- Install with `brew install sumdahl/tap/geet`.
- ⚠ **Tap trust (new in Homebrew 6.0.0, 2026-06-11):** non-official taps now require explicit trust. `brew install sumdahl/tap/geet` (the fully qualified name) trusts just that formula. `brew tap sumdahl/tap && brew install geet` fails until the user runs `brew trust --formula sumdahl/tap/geet` ([Tap Trust](https://docs.brew.sh/Tap-Trust)). **Document the fully qualified install command in the README.**

### 2.3 Formula (source build) for the tap

Grounded in the [Formula Cookbook](https://docs.brew.sh/Formula-Cookbook) and [`std_go_args` source](https://github.com/Homebrew/brew/blob/main/Library/Homebrew/formula.rb). `std_go_args` already adds `-trimpath`, `-o bin/<name>` and `-s -w`. The Cookbook says to pass the helper directly rather than copy its expansion. Its `ldflags: :goreleaser` preset sets `main.version/commit/date/builtBy` **without** a `v`, which is wrong for geet, so pass the ldflags explicitly.

```ruby
class Geet < Formula
  desc "Download Spotify tracks, albums and playlists as tagged audio files"
  homepage "https://github.com/sumdahl/geet"
  url "https://github.com/sumdahl/geet/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "…"
  license "MIT"
  head "https://github.com/sumdahl/geet.git", branch: "main"   # branch: is mandatory for git heads

  depends_on "go" => :build
  depends_on "ffmpeg"
  depends_on "yt-dlp"

  def install
    system "go", "build", *std_go_args(ldflags: "-X main.version=v#{version}"), "./cmd/geet"
  end

  test do
    # Cookbook: prefer a functional test over --version/--help.
    assert_match "GEET_OUTPUT", shell_output("#{bin}/geet config settings --json")
    assert_match "not a Spotify", shell_output("#{bin}/geet download https://example.com/x 2>&1", 2)
    assert_match "v#{version}", shell_output("#{bin}/geet version")
  end
end
```

(I checked all three test commands locally: `config settings --json` exits 0, a bad URL exits 2, and neither touches the network.) `go`, `ffmpeg`, `yt-dlp`, `fzf`, `libnotify` and `wl-clipboard` all exist in homebrew-core. `wl-clipboard` is Linux-only there ([formulae.brew.sh API](https://formulae.brew.sh/formula/wl-clipboard)). For `fzf`/`wl-clipboard`, use `caveats`. `depends_on … => :optional`/`:recommended` still work, but only in taps: they are "not allowed in Homebrew/homebrew-core" ([Cookbook](https://docs.brew.sh/Formula-Cookbook)), so avoid them if a core submission is ever the goal. Put a `depends_on "wl-clipboard"` inside `on_linux do … end` only if `watch` becomes central.

Auto-bumping a hand-written tap formula: [`mislav/bump-homebrew-formula-action`](https://github.com/mislav/bump-homebrew-formula-action) v4.2 (active) rewrites `url` and `sha256` via the GitHub API. It needs a `COMMITTER_TOKEN` PAT and the `homebrew-tap: sumdahl/homebrew-tap` input (the default is homebrew-core). ⚠ Its `push-to` input "defaults to creating or reusing a personal fork", i.e. it opens a PR. Check whether it commits directly when the token owner owns the tap. The alternative is Homebrew's own [`brew bump-formula-pr`](https://docs.brew.sh/Manpage#bump-formula-pr-options-formula).

### 2.4 macOS: does geet work there?

It **builds and vets for darwin/arm64 and darwin/amd64** (§0). Linux-isms, all non-fatal:
- `watch` needs `wl-paste` (Wayland) and `notify-send`, so it is effectively Linux-only.
- `cookies_from_browser = auto` shells out to `xdg-settings`, which is absent on macOS and returns `ErrNoDefaultBrowser`. Naming a browser explicitly still works. The keyring logic (`gnome-keyring-daemon`/`kwalletd` via `pgrep`) finds nothing and correctly adds no suffix.
- `os.UserConfigDir()` is `~/Library/Application Support` on macOS, so the README's `$XDG_CONFIG_HOME/geet/config.toml` wording is Linux-specific. The index falls back to `~/.local/share` on both.
- The `--tracks` hint text says `wl-paste` (cosmetic).

**Recommendation:** have the tap formula support both macOS and Linux (no `depends_on :linux`). `download`, `search` and `doctor` are portable, and a source build avoids Gatekeeper entirely. Mark macOS as "best effort / untested" in the README until someone runs it. Don't add a macOS CI leg yet. A future homebrew-core submission would need one.

### 2.5 Formula vs Cask, and GoReleaser `brews` → `homebrew_casks`

- **Homebrew's stance:** "Use a formula for open source command-line software… Use a cask for native macOS applications and for proprietary or supported binary-only software" ([Adding Software](https://docs.brew.sh/Adding-Software-to-Homebrew)). Also, "Open-source command-line-only software normally belongs in homebrew/core as a formula built from source" ([Acceptable Casks](https://docs.brew.sh/Acceptable-Casks)). That rule binds the official taps, not your own.
- ⚠ **Casks now work on Linux:** "Casks may target macOS, Linux or both". The `binary` artifact works on either OS, and `sha256` takes `arm64_linux:`/`x86_64_linux:` ([Cask Cookbook](https://docs.brew.sh/Cask-Cookbook)). This contradicts the common belief that "casks are macOS-only".
- **GoReleaser:** `brews` was soft-deprecated in **v2.10 (2025-06-08)** and hard-deprecated (warns) in **v2.16 (2026-05-24)**, in favor of **`homebrew_casks`** (added in v2.10) ([deprecations#brews](https://goreleaser.com/resources/deprecations/#brews), [v2.16.0 notes: "feat: proper deprecate brews"](https://github.com/goreleaser/goreleaser/releases/tag/v2.16.0)). Deprecated options are removed only in a major version, so `brews` still works in v2 (latest **v2.18.2**, 2026-09-17). The reason given: brews made "hackyish" formulae installing prebuilt binaries, which was only needed for Linuxbrew before Linux casks existed. Also new in v2.18.1: the `homebrew_casks.url.verified` field is deprecated.
- ⚠ **GoReleaser cannot generate a from-source formula.** Both `brews` and `homebrew_casks` package the prebuilt archives. An unsigned binary cask on macOS needs GoReleaser's documented `xattr -dr com.apple.quarantine` post-install hook, and GoReleaser itself warns that this "bypasses macOS security" and "Apple may disable this… without notice" ([homebrew_casks docs](https://goreleaser.com/customization/publish/homebrew_casks/)).

**Verdict:** for a personal tap, choose between two options:
- **(a) A hand-written source formula** (§2.3), bumped by an action. This fits Homebrew policy, avoids Gatekeeper, and is what a future core PR would look like.
- **(b) GoReleaser `homebrew_casks`**, zero-maintenance but with the quarantine hack.

I recommend **(a)**.

## 3. GoReleaser: one config for everything

A single `.goreleaser.yaml` can produce the GitHub release archives + `checksums.txt`, a tap cask (`homebrew_casks`), an AUR `-bin` package (`aurs`) and an AUR source package (`aur_sources`). It **cannot** produce a from-source Homebrew formula (§2.5). Sketch tailored to geet:

```yaml
version: 2
project_name: geet
dist: .goreleaser-dist          # the manual release flow already uses ./dist (git-ignored); avoid --clean wiping it

before:
  hooks: [go mod tidy -diff, go test ./...]   # tidy -diff fails if go.mod is untidy

builds:
  - main: ./cmd/geet
    env: [CGO_ENABLED=0]        # static, like today's release binaries
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags: ["-s -w -X main.version={{ .Tag }}"]   # geet prints version verbatim → keep the "v"
    # commit/date come from debug.ReadBuildInfo (GoReleaser builds in a git checkout)

archives:
  - formats: [tar.gz]           # default files already include LICENSE*, README*, CHANGELOG*
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"

checksum: { name_template: checksums.txt }
source: { enabled: true }       # needed by aur_sources

aurs:
  - name: geet-bin
    homepage: https://github.com/sumdahl/geet
    description: Download Spotify tracks, albums and playlists as tagged audio files
    maintainers: ["Sumiran Dahal <… at … dot …>"]
    license: MIT
    private_key: "{{ .Env.AUR_KEY }}"
    git_url: ssh://aur@aur.archlinux.org/geet-bin.git
    skip_upload: auto           # don't push -rc tags
    depends: [yt-dlp, ffmpeg]
    optdepends: ["fzf: picker for geet search", "wl-clipboard: geet watch", "libnotify: geet watch notifications"]
    package: |-
      install -Dm755 ./geet "${pkgdir}/usr/bin/geet"
      install -Dm644 ./LICENSE "${pkgdir}/usr/share/licenses/geet-bin/LICENSE"

aur_sources:
  - name: geet
    # …same homepage/description/maintainers/license/private_key/depends/optdepends…
    git_url: ssh://aur@aur.archlinux.org/geet.git
    provides: [geet]            # set explicitly (§1.6)
    conflicts: [geet-bin]
    makedepends: [go]
    prepare: |-
      export GOPATH="${srcdir}"
      go mod download -modcacherw
    build: |-
      export CGO_CPPFLAGS="${CPPFLAGS}" CGO_CFLAGS="${CFLAGS}" CGO_CXXFLAGS="${CXXFLAGS}" CGO_LDFLAGS="${LDFLAGS}"
      export GOPATH="${srcdir}"
      export GOFLAGS="-buildmode=pie -trimpath -mod=readonly -modcacherw"
      go build -ldflags "-linkmode=external -X main.version=v${pkgver}" -o geet ./cmd/geet
    package: |-
      install -Dm755 ./geet "${pkgdir}/usr/bin/geet"
      install -Dm644 ./LICENSE "${pkgdir}/usr/share/licenses/geet/LICENSE"
    # no check() support in aur_sources; see §1.6

# Optional, only if you pick cask option (b) in §2.5:
# homebrew_casks:
#   - repository: { owner: sumdahl, name: homebrew-tap, token: "{{ .Env.TAP_GITHUB_TOKEN }}" }
#     binaries: [geet]
#     dependencies: [{ formula: yt-dlp }, { formula: ffmpeg }]
#     hooks: { post: { install: "if OS.mac?\n  system_command \"/usr/bin/xattr\", args: [\"-dr\", \"com.apple.quarantine\", \"#{staged_path}/geet\"]\nend" } }
```

Run `goreleaser check` and `goreleaser release --snapshot` locally before the first tag ([deprecations page](https://goreleaser.com/resources/deprecations/)). Note that the asset names change from `geet-linux-amd64` to archives, so update the README `curl` install line. Alternatively, add a `formats: [binary]` archive to keep the old raw-binary URLs working ([archives docs](https://goreleaser.com/customization/package/archives/)).

### Workflow (`.github/workflows/release.yml`, per [GoReleaser's Actions doc](https://goreleaser.com/customization/ci/actions/))

```yaml
on: { push: { tags: ["v*"] } }
permissions: { contents: write }
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with: { fetch-depth: 0 }          # required: GoReleaser needs full history
      - uses: actions/setup-go@v7
        with: { go-version-file: go.mod }
      - uses: goreleaser/goreleaser-action@v7   # latest v7.2.3
        with: { distribution: goreleaser, version: "~> v2", args: release --clean }
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}          # enough for this repo's release
          AUR_KEY: ${{ secrets.AUR_SSH_PRIVATE_KEY }}
          TAP_GITHUB_TOKEN: ${{ secrets.TAP_GITHUB_TOKEN }}  # only if GoReleaser writes the tap
  homebrew:                                                   # for the hand-written source formula
    needs: release
    runs-on: ubuntu-latest
    steps:
      - uses: mislav/bump-homebrew-formula-action@v4
        with: { formula-name: geet, formula-path: Formula/geet.rb, homebrew-tap: sumdahl/homebrew-tap }
        env: { COMMITTER_TOKEN: ${{ secrets.TAP_GITHUB_TOKEN }} }
```

Writing to another repo (the tap) with the default `GITHUB_TOKEN` fails with `403 Resource not accessible by integration`. You need a PAT, either for the whole run or per integration ([GoReleaser error doc](https://goreleaser.com/errors/resource-not-accessible-by-integration/)). Prefer a **fine-grained PAT scoped to `sumdahl/homebrew-tap` with Contents: read & write** over a classic `repo` token ([GitHub PAT docs](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)). The mislav action's README asks for a classic token with `repo`+`workflow` scopes, so test whether a fine-grained token works for it.

## 4. Recommended plan

**Publish:** AUR `geet` (source) and `geet-bin`, plus a `sumdahl/homebrew-tap` with a **source-built formula**. Skip `geet-git`. Don't submit to homebrew-core until the repo reaches **225 stars / 90 forks / 90 watchers** (the self-submission bar), and add macOS CI before that.

1. **Code prep (small, no behavior change):**
   - Set the `go` directive in `go.mod` to the real minimum rather than `1.27.1` (§0 ⚠).
   - Add `dist: .goreleaser-dist` (or retire the manual `dist/` flow).
   - README: add AUR/brew install lines. Use `brew install sumdahl/tap/geet`, the fully qualified form because of tap trust. Note macOS as best effort, and that config lives in `~/Library/Application Support/geet` there.
   - The version injection (`main.version`) and LICENSE are already in place.
2. **Add `.goreleaser.yaml`** (§3) with `builds`/`archives`/`checksum`/`source`/`aurs`/`aur_sources`. Check it with `goreleaser check` and `goreleaser release --snapshot --clean`.
3. **AUR, first time by hand:**
   - Create the account and the dedicated key.
   - `makepkg -si` and `namcap` on the §1.4 PKGBUILD.
   - Push `geet` and `geet-bin` (PKGBUILD + `.SRCINFO` + a 0BSD `LICENSE`) so that you own both names.
4. **Create a second, passphrase-less SSH key for CI.** Add its public half to the AUR account and store the private half as secret **`AUR_SSH_PRIVATE_KEY`**.
5. **Create the tap:**
   - `brew tap-new sumdahl/homebrew-tap`, then `gh repo create sumdahl/homebrew-tap --public --push …`.
   - Add `Formula/geet.rb` (§2.3).
   - Test with `brew install --build-from-source sumdahl/tap/geet` and `brew test geet`.
6. **Create secret `TAP_GITHUB_TOKEN`**: a fine-grained PAT with Contents: write on `homebrew-tap` only.
7. **Add `.github/workflows/release.yml`** (§3). Tag `v0.3.0` and watch the release, the AUR pushes and the tap bump. Check afterwards that the AUR `LICENSE` survived GoReleaser's push, and that `geet version` prints `v0.3.0` from all three channels.
8. **Keep an eye on it:** the AUR says automation doesn't excuse manual review. When a release adds a dependency (e.g. `watch` becoming central), update `depends`/`optdepends` in `.goreleaser.yaml` and the formula by hand.

*Alternative if you want less upkeep:* publish only `geet-bin` (via `aurs`) plus a GoReleaser `homebrew_casks` cask. That needs one config and no hand-written formula, but it relies on the macOS quarantine workaround, and there is no source package for Arch users who prefer to build from source.
