#!/bin/sh
# geet installer: the same command on Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/sumdahl/geet/main/install.sh | sh
#
# In a terminal it shows what is already installed, then a menu: geet with
# the tools it needs (yt-dlp, ffmpeg, fzf), with the optional ones as well,
# a custom pick, or geet alone. It shows every command, sudo included,
# before running it. Without a terminal (CI, scripts) it installs geet only
# and reports what is missing, unless an option says otherwise:
#
#   ... | sh -s -- --yes         geet and every tool, no questions
#   ... | sh -s -- --required    geet and the required tools
#   ... | sh -s -- --no-deps     geet only
#
# Options:  --yes (-y), --all, --required, --no-deps, --version vX.Y.Z,
#           --dir DIR, --help
# Env:      GEET_VERSION, GEET_INSTALL_DIR, GEET_DEPS=all|required|none,
#           NO_COLOR
#
# Keep this file ASCII: bash 3.2 (macOS's /bin/sh) reads a non-ASCII byte
# after $var as part of the variable name. Symbols are made with printf.
set -eu

repo="sumdahl/geet"

# ---------------------------------------------------------------- output

has() { command -v "$1" >/dev/null 2>&1; }

setup_output() {
	c_off="" c_bold="" c_dim="" c_red="" c_green="" c_yellow="" c_cyan=""
	if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ]; then
		e=$(printf '\033')
		c_off="${e}[0m" c_bold="${e}[1m" c_dim="${e}[2m" c_red="${e}[31m"
		c_green="${e}[32m" c_yellow="${e}[33m" c_cyan="${e}[36m"
		fancy=1
	else
		fancy=""
	fi
	case "${LC_ALL:-${LC_CTYPE:-${LANG:-}}}" in
	*UTF-8* | *utf-8* | *UTF8* | *utf8*) unicode=1 ;;
	*) unicode="" ;;
	esac
	# macOS terminals are UTF-8 even when LANG is unset.
	if [ "$(uname -s)" = Darwin ] && [ -t 1 ]; then unicode=1; fi
	if [ -n "$unicode" ]; then
		g_ok=$(printf '\342\234\223')   # check mark
		g_no=$(printf '\342\234\227')   # ballot x
		g_dot=$(printf '\302\267')      # middle dot
		g_ptr=$(printf '\342\235\257')  # pointer
		g_on=$(printf '\342\227\217')   # filled circle
		g_off=$(printf '\342\227\213')  # empty circle
		g_line=$(printf '\342\224\200') # horizontal line
		g_up=$(printf '\342\206\221')   # up arrow
		g_down=$(printf '\342\206\223') # down arrow
		g_geet=$(printf '\340\244\227\340\245\200\340\244\244') # "geet" in Devanagari
		g_spin=$(printf '\342\240\213 \342\240\231 \342\240\271 \342\240\270 \342\240\274 \342\240\264 \342\240\246 \342\240\247 \342\240\207 \342\240\217')
	else
		g_ok="ok" g_no="x" g_dot="-" g_ptr=">" g_on="*" g_off=" " g_line="-"
		# shellcheck disable=SC1003 # a literal backslash spinner frame
		g_up="up" g_down="down" g_geet="song" g_spin='| / - \'
	fi
}

say() { printf '%s\n' "$*"; }
fail() {
	printf '\n  %s%s geet install:%s %s\n\n' "$c_red" "$g_no" "$c_off" "$*" >&2
	exit 1
}
rule() {
	i=0 line=""
	while [ $i -lt "$1" ]; do
		line="$line$g_line"
		i=$((i + 1))
	done
	printf '  %s%s%s\n' "$c_dim" "$line" "$c_off"
}
heading() {
	say ""
	say "  ${c_bold}$1${c_off}"
}
bullet() { printf '  %s%s%s %s\n' "$c_cyan" "$g_ptr" "$c_off" "$*"; }

# spin LABEL CMD...: runs CMD with its output captured, showing a spinner
# in a terminal, then a check or a cross. On failure the end of CMD's
# output is shown. Returns CMD's status.
spin() {
	label=$1
	shift
	log="$tmp/step.log"
	rc=0
	quiet_on
	if [ -n "$fancy" ]; then
		"$@" >"$log" 2>&1 </dev/null &
		pid=$!
		printf '\033[?25l'
		while kill -0 "$pid" 2>/dev/null; do
			for f in $g_spin; do
				printf '\r  %s%s%s %s' "$c_cyan" "$f" "$c_off" "$label"
				sleep 0.1
				kill -0 "$pid" 2>/dev/null || break
			done
		done
		wait "$pid" || rc=$?
		printf '\r\033[2K\033[?25h'
	else
		"$@" >"$log" 2>&1 </dev/null || rc=$?
	fi
	quiet_off
	if [ "$rc" -eq 0 ]; then
		printf '  %s%s%s %s\n' "$c_green" "$g_ok" "$c_off" "$label"
	else
		printf '  %s%s%s %s\n' "$c_red" "$g_no" "$c_off" "$label"
		tail -n 15 "$log" | sed "s/^/      /"
	fi
	return "$rc"
}

# ---------------------------------------------------------------- input

# Prompts read the terminal, not stdin: with curl | sh, stdin is the script.
tty_ok() { [ -t 1 ] && (: </dev/tty) 2>/dev/null; }

ask() {
	printf '%s' "$1"
	ans=""
	IFS= read -r ans </dev/tty || ans=""
}

# confirm QUESTION DEFAULT(y|n): true for yes.
confirm() {
	if [ "$2" = y ]; then hint="Y/n"; else hint="y/N"; fi
	while :; do
		ask "  $1 ${c_dim}[$hint]${c_off} "
		case $ans in
		"") [ "$2" = y ] && return 0 || return 1 ;;
		[Yy] | [Yy][Ee][Ss]) return 0 ;;
		[Nn] | [Nn][Oo]) return 1 ;;
		esac
	done
}

# keys_on switches the terminal to one-key-at-a-time input for the menus.
keys_on() {
	stty_saved=$(stty -g </dev/tty 2>/dev/null) || return 1
	stty -icanon -echo min 1 time 0 </dev/tty 2>/dev/null || return 1
	printf '\033[?25l'
}
keys_off() {
	if [ -n "${stty_saved:-}" ]; then
		stty "$stty_saved" </dev/tty 2>/dev/null || true
		stty_saved=""
		printf '\033[?25h'
	fi
}

# quiet_on hides keys typed while a step runs, and quiet_off throws them
# away, so an impatient Enter can't answer the next question.
quiet_on() {
	tty_ok || return 0
	stty_saved=$(stty -g </dev/tty 2>/dev/null) || return 0
	stty -echo </dev/tty 2>/dev/null || true
}
quiet_off() {
	if [ -n "${stty_saved:-}" ]; then
		stty -icanon min 0 time 0 </dev/tty 2>/dev/null &&
			dd bs=4096 count=1 </dev/tty >/dev/null 2>&1 || true
		keys_off
	fi
}

# key reads one keypress into $k: up, down, space, enter, esc, or the key.
key() {
	k=$(dd bs=1 count=1 2>/dev/null </dev/tty) || k=q
	case $k in
	"$(printf '\033')")
		k=$(dd bs=1 count=2 2>/dev/null </dev/tty) || k=""
		case $k in
		"[A" | OA) k=up ;;
		"[B" | OB) k=down ;;
		*) k=esc ;;
		esac
		;;
	"") k=enter ;;
	" ") k=space ;;
	esac
}

quit() {
	keys_off
	say ""
	say "  Nothing was installed."
	say ""
	exit 0
}

# menu TITLE "Label|description"...: sets $choice to the picked number.
# Arrow keys or j/k move, a number jumps, Enter picks, q quits.
menu() {
	heading "$1"
	shift
	n=$#
	i=1
	for o in "$@"; do
		eval "opt_$i=\$o"
		i=$((i + 1))
	done
	choice=1
	if ! keys_on; then
		i=1
		while [ $i -le "$n" ]; do
			eval "o=\$opt_$i"
			printf '    %s) %-16s %s%s%s\n' "$i" "${o%%|*}" "$c_dim" "${o#*|}" "$c_off"
			i=$((i + 1))
		done
		while :; do
			ask "  Choose 1-$n ${c_dim}[1]${c_off} "
			case $ans in
			"") return 0 ;;
			*[!0-9]*) ;;
			*) if [ "$ans" -ge 1 ] && [ "$ans" -le "$n" ]; then
				choice=$ans
				return 0
			fi ;;
			esac
		done
	fi
	say "  ${c_dim}$g_up/$g_down move $g_dot enter select $g_dot q quit${c_off}"
	drawn=""
	while :; do
		if [ -n "$drawn" ]; then printf '\033[%dA' "$n"; fi
		drawn=1
		i=1
		while [ $i -le "$n" ]; do
			eval "o=\$opt_$i"
			if [ "$i" -eq "$choice" ]; then
				printf '\033[2K  %s%s %s%-16s%s %s\n' "$c_cyan" "$g_ptr" "$c_bold" "${o%%|*}" "$c_off" "${o#*|}"
			else
				printf '\033[2K    %-16s %s%s%s\n' "${o%%|*}" "$c_dim" "${o#*|}" "$c_off"
			fi
			i=$((i + 1))
		done
		key
		case $k in
		up | k) choice=$((choice > 1 ? choice - 1 : n)) ;;
		down | j) choice=$((choice < n ? choice + 1 : 1)) ;;
		[1-9]) if [ "$k" -le "$n" ]; then choice=$k; fi ;;
		enter) break ;;
		q | esc) quit ;;
		esac
	done
	keys_off
}

# pick_tools is the Custom checklist over the missing tools. Sets $chosen.
# shellcheck disable=SC2086 # $items is a word list, split on purpose
pick_tools() {
	s=""
	items="$missing_required $missing_optional"
	for t in $items; do eval "sel_$t=1"; done
	count=$(printf '%s\n' $items | awk 'NF' | wc -l | tr -d ' ')
	heading "Choose tools"
	if ! keys_on; then
		chosen=""
		for t in $items; do
			if is_required "$t"; then d=y; else d=n; fi
			if confirm "Install $(tool_label "$t") ($(tool_why "$t"))?" "$d"; then
				chosen="$chosen $t"
			fi
		done
		return 0
	fi
	say "  ${c_dim}$g_up/$g_down move $g_dot space toggle $g_dot enter continue $g_dot q quit${c_off}"
	cur=1 drawn=""
	while :; do
		if [ -n "$drawn" ]; then printf '\033[%dA' "$count"; fi
		drawn=1
		i=1
		for t in $items; do
			eval "s=\$sel_$t"
			if [ "$s" = 1 ]; then box="$c_green$g_on$c_off"; else box="$c_dim$g_off$c_off"; fi
			if ! is_required "$t"; then
				note="${c_dim}optional $g_dot $(tool_why "$t")$c_off"
			elif [ "$s" = 1 ]; then
				note="${c_dim}required $g_dot $(tool_why "$t")$c_off"
			else
				note="${c_red}required: geet won't work without it$c_off"
			fi
			if [ "$i" -eq "$cur" ]; then
				printf '\033[2K  %s%s%s %s %s%-14s%s %s\n' "$c_cyan" "$g_ptr" "$c_off" "$box" "$c_bold" "$(tool_label "$t")" "$c_off" "$note"
			else
				printf '\033[2K    %s %-14s %s\n' "$box" "$(tool_label "$t")" "$note"
			fi
			i=$((i + 1))
		done
		key
		case $k in
		up | k) cur=$((cur > 1 ? cur - 1 : count)) ;;
		down | j) cur=$((cur < count ? cur + 1 : 1)) ;;
		space | x)
			t=$(printf '%s\n' $items | awk 'NF' | sed -n "${cur}p")
			eval "s=\$sel_$t"
			if [ "$s" = 1 ]; then eval "sel_$t=0"; else eval "sel_$t=1"; fi
			;;
		enter) break ;;
		q | esc) quit ;;
		esac
	done
	keys_off
	chosen=""
	for t in $items; do
		eval "s=\$sel_$t"
		if [ "$s" = 1 ]; then chosen="$chosen $t"; fi
	done
	return 0
}

# ---------------------------------------------------------------- system

detect_system() {
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
	# An Intel shell on Apple Silicon (Rosetta) reports x86_64.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" = 1 ]; then
		arch=arm64
	fi

	pm=""
	if [ "$os" = darwin ]; then
		os_name="macOS $(sw_vers -productVersion 2>/dev/null || true)"
		if find_brew; then pm=brew; fi
	else
		os_name="Linux"
		if [ -r /etc/os-release ]; then
			# shellcheck source=/dev/null
			os_name=$(. /etc/os-release && printf '%s' "${PRETTY_NAME:-${NAME:-Linux}}")
		fi
		for m in pacman apt-get dnf apk; do
			if has "$m"; then
				pm=$m
				break
			fi
		done
	fi
}

# find_brew: Homebrew is often installed but not on a script's PATH.
find_brew() {
	has brew && return 0
	for b in /opt/homebrew/bin/brew /usr/local/bin/brew; do
		if [ -x "$b" ]; then
			eval "$("$b" shellenv)"
			return 0
		fi
	done
	return 1
}

pm_name() { printf '%s' "$pm" | sed 's/-get$//'; }

# as_root CMD...: runs CMD as root, through sudo or doas when needed.
as_root() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	elif has sudo; then
		sudo "$@"
	elif has doas; then
		doas "$@"
	else
		echo "needs root: run as root, or install sudo or doas"
		return 1
	fi
}
root_prefix() {
	if [ "$(id -u)" -eq 0 ]; then
		return 0
	elif has doas && ! has sudo; then
		printf 'doas '
	else
		printf 'sudo '
	fi
}

# ---------------------------------------------------------------- tools

required_tools="ytdlp ffmpeg fzf"
optional_tools_linux="wlclip notify"

tool_label() {
	case $1 in
	ytdlp) printf 'yt-dlp' ;;
	ffmpeg) printf 'ffmpeg' ;;
	fzf) printf 'fzf' ;;
	wlclip) printf 'wl-clipboard' ;;
	notify) printf 'libnotify' ;;
	esac
}
tool_why() {
	case $1 in
	ytdlp) printf 'finds and downloads the audio' ;;
	ffmpeg) printf 'converts and tags the files' ;;
	fzf) printf 'the geet search menu' ;;
	wlclip) printf 'geet watch: reads the clipboard' ;;
	notify) printf 'geet watch: desktop notifications' ;;
	esac
}
tool_present() {
	case $1 in
	ytdlp) has yt-dlp ;;
	ffmpeg) has ffmpeg && has ffprobe ;;
	fzf) has fzf ;;
	wlclip) has wl-paste ;;
	notify) has notify-send ;;
	esac
}
tool_version() {
	case $1 in
	ytdlp) yt-dlp --version 2>/dev/null | head -n 1 ;;
	ffmpeg) ffmpeg -version 2>/dev/null | awk 'NR == 1 { print $3 }' ;;
	fzf) fzf --version 2>/dev/null | awk '{ print $1 }' ;;
	wlclip) wl-paste --version 2>/dev/null | awk 'NR == 1 { print $2 }' ;;
	notify) notify-send --version 2>/dev/null | awk '{ print $2 }' ;;
	esac
}
is_required() {
	case " $required_tools " in *" $1 "*) return 0 ;; esac
	return 1
}

# pkg_of TOOL prints its package in this system's package manager, or
# nothing. Debian and Ubuntu package a yt-dlp too old for YouTube, so there
# (and wherever no package exists) yt-dlp is the official standalone binary.
pkg_of() {
	case $pm:$1 in
	brew:ytdlp | pacman:ytdlp | dnf:ytdlp | apk:ytdlp) printf 'yt-dlp' ;;
	brew:ffmpeg | pacman:ffmpeg | apt-get:ffmpeg | apk:ffmpeg) printf 'ffmpeg' ;;
	dnf:ffmpeg) printf 'ffmpeg-free' ;;
	brew:fzf | pacman:fzf | apt-get:fzf | dnf:fzf | apk:fzf) printf 'fzf' ;;
	pacman:wlclip | apt-get:wlclip | dnf:wlclip | apk:wlclip) printf 'wl-clipboard' ;;
	apt-get:notify) printf 'libnotify-bin' ;;
	pacman:notify | dnf:notify | apk:notify) printf 'libnotify' ;;
	esac
}

pm_command() {
	case $pm in
	brew) printf 'brew install %s' "$1" ;;
	pacman) printf '%spacman -S --needed --noconfirm %s' "$(root_prefix)" "$1" ;;
	apt-get) printf '%sapt-get install -y %s' "$(root_prefix)" "$1" ;;
	dnf) printf '%sdnf install -y %s' "$(root_prefix)" "$1" ;;
	apk) printf '%sapk add %s' "$(root_prefix)" "$1" ;;
	esac
}

# run_pm PACKAGES installs a space-separated package list.
run_pm() {
	# shellcheck disable=SC2086 # the list is split on purpose
	case $pm in
	brew) HOMEBREW_NO_ENV_HINTS=1 HOMEBREW_NO_INSTALL_CLEANUP=1 brew install $1 ;;
	pacman) as_root pacman -S --needed --noconfirm $1 ;;
	apt-get)
		as_root env DEBIAN_FRONTEND=noninteractive apt-get update -q &&
			as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends $1
		;;
	dnf) as_root dnf install -y -q $1 ;;
	apk) as_root apk add -q $1 ;;
	esac
}

# ---------------------------------------------------------------- steps

resolve_version() {
	if [ -n "$version" ]; then return 0; fi
	# The latest release page redirects to .../releases/tag/<tag>.
	version=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
		fail "can't reach GitHub to find the latest release"
	version=${version##*/}
	case $version in
	v*) ;;
	*) fail "couldn't find the latest release of $repo" ;;
	esac
}

sha256_of() {
	if has sha256sum; then
		sha256sum "$1" | awk '{ print $1 }'
	elif has shasum; then
		shasum -a 256 "$1" | awk '{ print $1 }'
	else
		return 1
	fi
}

# fetch_verified URL SUMS_URL NAME DEST downloads URL, checks it against the
# NAME line of the checksum file at SUMS_URL, and installs it as DEST.
fetch_verified() {
	curl -fsSL "$1" -o "$tmp/dl" || {
		echo "download failed: $1"
		return 1
	}
	curl -fsSL "$2" -o "$tmp/sums" || {
		echo "download failed: $2"
		return 1
	}
	want=$(awk -v f="$3" '$2 == f || $2 == "*" f { print $1 }' "$tmp/sums")
	[ -n "$want" ] || {
		echo "no checksum for $3"
		return 1
	}
	got=$(sha256_of "$tmp/dl") || {
		echo "need sha256sum or shasum to verify downloads"
		return 1
	}
	[ "$got" = "$want" ] || {
		echo "checksum mismatch for $3: the download is corrupt or was tampered with"
		return 1
	}
	mkdir -p "$(dirname "$4")" && chmod +x "$tmp/dl" && mv -f "$tmp/dl" "$4"
}

install_geet() {
	asset="geet-$os-$arch"
	base="https://github.com/$repo/releases/download/$version"
	fetch_verified "$base/$asset" "$base/SHA256SUMS" "$asset" "$dir/geet" || return 1
	"$dir/geet" version >/dev/null 2>&1 || {
		echo "installed $dir/geet, but it doesn't run on this system ($os/$arch)"
		return 1
	}
}

install_ytdlp_binary() {
	case $os-$arch in
	linux-amd64) f=yt-dlp_linux ;;
	linux-arm64) f=yt-dlp_linux_aarch64 ;;
	darwin-*) f=yt-dlp_macos ;;
	esac
	base="https://github.com/yt-dlp/yt-dlp/releases/latest/download"
	fetch_verified "$base/$f" "$base/SHA2-256SUMS" "$f" "$dir/yt-dlp"
}

install_homebrew() {
	say ""
	say "  ${c_dim}Running Homebrew's official installer; it asks for your password.${c_off}"
	/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" </dev/tty
	find_brew
}

rc_file() {
	case ${SHELL:-} in
	*/zsh) printf '%s' "${ZDOTDIR:-$HOME}/.zshrc" ;;
	*/bash) if [ "$os" = darwin ]; then printf '%s' "$HOME/.bash_profile"; else printf '%s' "$HOME/.bashrc"; fi ;;
	*/fish) printf 'fish' ;;
	*) printf '%s' "$HOME/.profile" ;;
	esac
}

# pretty shows a path with ~ for the home directory.
# shellcheck disable=SC2088 # a literal ~ is the point
pretty() {
	case $1 in
	"$HOME"/*) printf '~/%s' "${1#"$HOME"/}" ;;
	*) printf '%s' "$1" ;;
	esac
}

add_to_path() {
	file=$(rc_file)
	if [ "$file" = fish ]; then
		fish -c "fish_add_path '$dir'"
		return
	fi
	case $dir in
	"$HOME"/*) line="export PATH=\"\$HOME/${dir#"$HOME"/}:\$PATH\"" ;;
	*) line="export PATH=\"$dir:\$PATH\"" ;;
	esac
	if [ -f "$file" ] && grep -qF "$line" "$file"; then return 0; fi
	printf '\n# Added by the geet installer\n%s\n' "$line" >>"$file"
}

on_path() {
	case ":$2:" in *":$1:"*) return 0 ;; esac
	return 1
}

tool_row() { # SYMBOL-COLOR SYMBOL NAME NOTE-COLOR NOTE [WHY]
	printf '  %s%s%s %-14s %s%-14s%s %s\n' "$1" "$2" "$c_off" "$3" "$4" "$5" "$c_off" "${6:-}"
}

# ---------------------------------------------------------------- main

usage() {
	cat <<'EOF'
geet installer

  curl -fsSL https://raw.githubusercontent.com/sumdahl/geet/main/install.sh | sh

Options, after "sh -s --":
  -y, --yes          install geet and every tool, without asking
      --all          geet and every tool (required and optional)
      --required     geet and the required tools: yt-dlp, ffmpeg, fzf
      --no-deps      geet only
      --version TAG  a release such as v0.2.0 (default: the latest)
      --dir DIR      where to put geet (default: ~/.local/bin)
  -h, --help         this help

Environment: GEET_VERSION, GEET_INSTALL_DIR, GEET_DEPS=all|required|none,
NO_COLOR.
EOF
}

main() {
	version=${GEET_VERSION:-}
	dir=${GEET_INSTALL_DIR:-$HOME/.local/bin}
	deps=${GEET_DEPS:-}
	yes=""
	setup_output
	while [ $# -gt 0 ]; do
		case $1 in
		-y | --yes) yes=1 ;;
		--all) deps=all ;;
		--required) deps=required ;;
		--no-deps) deps=none ;;
		--version)
			[ $# -ge 2 ] || fail "--version needs a tag such as v0.2.0"
			version=$2
			shift
			;;
		--dir)
			[ $# -ge 2 ] || fail "--dir needs a directory"
			dir=$2
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		*) fail "unknown option $1 (see --help)" ;;
		esac
		shift
	done
	if [ -n "$yes" ] && [ -z "$deps" ]; then deps=all; fi
	case $deps in "" | all | required | none) ;; *) fail "GEET_DEPS must be all, required or none" ;; esac

	has curl || fail "curl is needed"
	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t geet)
	stty_saved=""
	# Keep the exit status: bash 3.2 reports the trap's own otherwise.
	trap 'rc=$?; keys_off; rm -rf "$tmp"; exit $rc' EXIT
	trap 'exit 130' INT TERM

	interactive=""
	if [ -z "$deps" ] && [ -z "$yes" ] && tty_ok; then interactive=1; fi

	detect_system
	optional_tools=""
	if [ "$os" = linux ]; then optional_tools=$optional_tools_linux; fi

	# ---- overview
	say ""
	say "  ${c_bold}${c_green}geet${c_off} ${c_dim}$g_geet $g_dot installer${c_off}"
	rule 44
	if [ -n "$fancy" ]; then
		printf '  %s%s%s finding the latest release' "$c_cyan" "${g_spin%% *}" "$c_off"
	fi
	resolve_version
	if [ -n "$fancy" ]; then printf '\r\033[2K'; fi
	printf '  %-9s %s %s %s\n' "System" "$os_name" "$g_dot" "$arch"
	printf '  %-9s %s %s %s\n' "geet" "$version" "$g_dot" "$(pretty "$dir")"
	if [ -n "$pm" ]; then
		printf '  %-9s %s\n' "Packages" "$(pm_name)"
	elif [ "$os" = darwin ]; then
		printf '  %-9s %s%s%s\n' "Packages" "$c_yellow" "Homebrew isn't installed" "$c_off"
	else
		printf '  %-9s %s%s%s\n' "Packages" "$c_yellow" "no supported package manager" "$c_off"
	fi

	heading "Tools geet uses"
	missing_required="" missing_optional=""
	for t in $required_tools $optional_tools; do
		if tool_present "$t"; then
			v=$(tool_version "$t" || true)
			tool_row "$c_green" "$g_ok" "$(tool_label "$t")" "$c_dim" "${v:-installed}" "$(tool_why "$t")"
		elif is_required "$t"; then
			missing_required="$missing_required $t"
			tool_row "$c_red" "$g_no" "$(tool_label "$t")" "$c_red" "required" "$(tool_why "$t")"
		else
			missing_optional="$missing_optional $t"
			tool_row "$c_dim" "$g_dot" "$(tool_label "$t")" "$c_dim" "optional" "$(tool_why "$t")"
		fi
	done

	# ---- what to install besides geet
	chosen=""
	if [ -n "$interactive" ] && [ -n "$missing_required$missing_optional" ]; then
		if [ -n "$missing_optional" ]; then
			menu "What should be installed?" \
				"Recommended|geet, the required tools and the optional ones" \
				"Required only|geet with yt-dlp, ffmpeg and fzf" \
				"Custom|choose each tool" \
				"geet only|no tools" \
				"Quit|install nothing"
			case $choice in
			1) chosen="$missing_required $missing_optional" ;;
			2) chosen=$missing_required ;;
			3) pick_tools ;;
			5) quit ;;
			esac
		else
			menu "What should be installed?" \
				"Recommended|geet with the tools it needs" \
				"Custom|choose each tool" \
				"geet only|no tools" \
				"Quit|install nothing"
			case $choice in
			1) chosen=$missing_required ;;
			2) pick_tools ;;
			4) quit ;;
			esac
		fi
	else
		case $deps in
		all) chosen="$missing_required $missing_optional" ;;
		required) chosen=$missing_required ;;
		esac
	fi

	# Homebrew is what installs ffmpeg and fzf on macOS.
	if [ "$os" = darwin ] && [ -z "$pm" ] && [ -n "$interactive" ]; then
		need_brew=""
		for t in $chosen; do
			if [ "$t" != ytdlp ]; then need_brew=1; fi
		done
		if [ -n "$need_brew" ]; then
			say ""
			say "  ffmpeg and fzf come from Homebrew, which isn't installed."
			if confirm "Install Homebrew first?" y; then
				if install_homebrew; then pm=brew; fi
			fi
		fi
	fi

	# Split the choice: package-manager packages, the yt-dlp binary, and
	# what can't be installed here.
	pkgs="" ytdlp_binary="" unavailable=""
	for t in $chosen; do
		p=$(pkg_of "$t")
		if [ -n "$p" ]; then
			pkgs="$pkgs $p"
		elif [ "$t" = ytdlp ]; then
			ytdlp_binary=1
		else
			unavailable="$unavailable $t"
		fi
	done
	pkgs=${pkgs# }

	path_step=""
	if [ -n "$interactive" ] && ! on_path "$dir" "$PATH"; then path_step=ask; fi

	# ---- plan
	heading "Plan"
	bullet "geet $version to $(pretty "$dir")/geet"
	if [ -n "$ytdlp_binary" ]; then bullet "yt-dlp, the official build, to $(pretty "$dir")/yt-dlp"; fi
	if [ -n "$pkgs" ]; then bullet "$(pm_command "$pkgs")"; fi
	for t in $unavailable; do
		printf '  %s!%s %s: no package for it here; install it yourself\n' "$c_yellow" "$c_off" "$(tool_label "$t")"
	done
	skipped=""
	for t in $missing_required; do
		case " $chosen " in *" $t "*) ;; *) skipped="$skipped $(tool_label "$t")" ;; esac
	done
	if [ -n "$skipped" ]; then
		printf '  %s!%s not installing%s: geet needs them to download\n' "$c_yellow" "$c_off" "$skipped"
	fi
	if [ -n "$path_step" ]; then bullet "add $(pretty "$dir") to your PATH in $(pretty "$(rc_file)")"; fi

	if [ -n "$interactive" ]; then
		say ""
		confirm "Go ahead?" y || quit
	fi

	# ---- install
	heading "Installing"
	failed=""
	spin "geet $version" install_geet || fail "geet couldn't be installed (see above)"
	if [ -n "$ytdlp_binary" ]; then
		spin "yt-dlp (official build)" install_ytdlp_binary || failed="$failed yt-dlp"
	fi
	if [ -n "$pkgs" ]; then
		ready=1
		# Ask for the password now, in the open, not behind the spinner.
		if [ "$pm" != brew ] && [ "$(id -u)" -ne 0 ] && has sudo && tty_ok; then
			say "  ${c_dim}\$ $(pm_command "$pkgs")${c_off}"
			# shellcheck disable=SC2024 # sudo reads the password from the terminal
			sudo -v </dev/tty || ready=""
		fi
		if [ -n "$ready" ]; then
			spin "$pkgs ($(pm_name))" run_pm "$pkgs" || failed="$failed $pkgs"
		else
			failed="$failed $pkgs"
		fi
	fi
	if [ "$path_step" = ask ]; then
		say ""
		if confirm "Add $(pretty "$dir") to your PATH in $(pretty "$(rc_file)")?" y && add_to_path; then
			path_step=added
		else
			path_step=""
		fi
	fi

	# ---- result
	original_path=$PATH
	if ! on_path "$dir" "$PATH"; then PATH="$dir:$PATH"; fi
	heading "Installed"
	tool_row "$c_green" "$g_ok" "geet" "$c_dim" "$("$dir/geet" version | awk '{ print $2 }')"
	still_missing=""
	for t in $required_tools $optional_tools; do
		if tool_present "$t"; then
			v=$(tool_version "$t" || true)
			tool_row "$c_green" "$g_ok" "$(tool_label "$t")" "$c_dim" "${v:-installed}"
		elif is_required "$t"; then
			still_missing="$still_missing $t"
			tool_row "$c_red" "$g_no" "$(tool_label "$t")" "$c_red" "missing" "geet needs it"
		else
			tool_row "$c_dim" "$g_dot" "$(tool_label "$t")" "$c_dim" "not installed" "optional"
		fi
	done

	if [ -n "$still_missing" ]; then
		heading "Still needed"
		for t in $still_missing; do
			p=$(pkg_of "$t")
			if [ -n "$p" ]; then
				say "  $(pm_command "$p")"
			elif [ "$t" = ytdlp ]; then
				say "  curl -fsSL https://raw.githubusercontent.com/$repo/main/install.sh | sh -s -- --required"
			elif [ "$os" = darwin ]; then
				say "  install Homebrew (https://brew.sh), then: brew install $(tool_label "$t")"
			else
				say "  install $(tool_label "$t") with your package manager"
			fi
		done
	fi

	heading "Next"
	if [ "$path_step" = added ]; then
		say "  Open a new terminal (your PATH changed), then run:"
	elif ! on_path "$dir" "$original_path"; then
		say "  $(pretty "$dir") isn't on your PATH. Add this to your shell's startup file:"
		say "    export PATH=\"$dir:\$PATH\""
		say "  Then run:"
	fi
	say "    geet doctor                   ${c_dim}checks that everything works${c_off}"
	say "    geet search blinding lights   ${c_dim}finds a song and downloads it${c_off}"
	say "    geet download <spotify link>  ${c_dim}a song, an album or a playlist${c_off}"
	say ""
	[ -z "$failed" ]
}

main "$@"
