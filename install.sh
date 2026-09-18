#!/bin/sh
# Installs geet, the same command on Linux and macOS:
#
#   curl -fsSL https://raw.githubusercontent.com/sumdahl/geet/main/install.sh | sh
#
# It picks the binary for this OS and CPU from the GitHub release, checks it
# against the release's SHA256SUMS, and puts it in ~/.local/bin. No sudo.
#
# Environment:
#   GEET_VERSION      a release tag such as v0.2.0 (default: the latest)
#   GEET_INSTALL_DIR  where to put geet (default: ~/.local/bin)
set -eu

repo="sumdahl/geet"

say() { printf '%s\n' "$*"; }
fail() {
	printf 'geet install: %s\n' "$*" >&2
	exit 1
}
has() { command -v "$1" >/dev/null 2>&1; }

# Everything runs from main, called on the last line, so a download cut
# short midway through never runs half a script.
main() {
	has curl || fail "curl is needed"

	case $(uname -s) in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) fail "$(uname -s) isn't supported: geet runs on Linux and macOS" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "$(uname -m) CPUs aren't supported: geet is built for x86-64 and arm64" ;;
	esac
	# An Intel shell on Apple Silicon (Rosetta) reports x86_64; the native
	# binary is the better choice.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
		arch=arm64
	fi

	version=${GEET_VERSION:-}
	if [ -z "$version" ]; then
		# The latest release's page redirects to .../releases/tag/<tag>.
		version=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
			fail "can't reach GitHub to find the latest release"
		version=${version##*/}
		case $version in
		v*) ;;
		*) fail "couldn't find the latest release of $repo" ;;
		esac
	fi

	asset="geet-$os-$arch"
	base="https://github.com/$repo/releases/download/$version"
	dir=${GEET_INSTALL_DIR:-$HOME/.local/bin}

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t geet)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "Downloading geet $version for $os/$arch…"
	curl -fsSL "$base/$asset" -o "$tmp/$asset" ||
		fail "$version has no $asset download (see https://github.com/$repo/releases)"
	curl -fsSL "$base/SHA256SUMS" -o "$tmp/SHA256SUMS" ||
		fail "couldn't download $version's SHA256SUMS"

	want=$(awk -v f="$asset" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
	[ -n "$want" ] || fail "SHA256SUMS has no entry for $asset"
	if has sha256sum; then
		got=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
	elif has shasum; then
		got=$(shasum -a 256 "$tmp/$asset" | awk '{ print $1 }')
	else
		fail "need sha256sum or shasum to verify the download"
	fi
	[ "$got" = "$want" ] || fail "checksum mismatch for $asset: the download is corrupt or was tampered with"

	mkdir -p "$dir" || fail "can't create $dir (set GEET_INSTALL_DIR to install elsewhere)"
	chmod +x "$tmp/$asset"
	mv -f "$tmp/$asset" "$dir/geet" || fail "can't write $dir/geet"
	installed=$("$dir/geet" version 2>/dev/null) ||
		fail "installed $dir/geet, but it doesn't run on this system ($os/$arch)"
	say "Installed $installed to $dir/geet"

	case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		case ${SHELL:-} in
		*/zsh) rc="~/.zshrc" ;;
		*/bash) rc="~/.bashrc" ;;
		*/fish) rc="" ;;
		*) rc="your shell's startup file" ;;
		esac
		say ""
		say "$dir isn't on your PATH yet."
		if [ -z "$rc" ]; then
			say "Run:  fish_add_path $dir"
		else
			say "Add this line to $rc, then open a new terminal:"
			say "  export PATH=\"$dir:\$PATH\""
		fi
		;;
	esac

	missing=""
	for tool in yt-dlp ffmpeg; do
		has "$tool" || missing="$missing $tool"
	done
	if [ -n "$missing" ]; then
		say ""
		say "geet also needs:$missing"
		if [ "$os" = darwin ]; then
			say "  brew install$missing"
		elif has pacman; then
			say "  sudo pacman -S$missing"
		elif has apt-get; then
			say "  sudo apt install$missing   (Debian's yt-dlp is often too old: pipx install yt-dlp is safer)"
		elif has dnf; then
			say "  sudo dnf install$missing"
		else
			say "  install them with your package manager"
		fi
	fi

	say ""
	say "Next: geet doctor   (checks that everything geet needs is in place)"
}

main "$@"
