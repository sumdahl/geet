#!/bin/sh
# Prints the CHANGELOG.md section for a release, without its heading:
#   scripts/release-notes.sh v0.3.0
# Fails when the section is missing, so a release can't go out without one.
set -eu
version=${1#v}
notes=$(awk -v v="$version" '
	index($0, "## [" v "]") == 1 { found = 1; next }
	found && (/^## \[/ || /^\[[^]]+\]: /) { exit }
	found { print }
' "${2:-CHANGELOG.md}")
# Trim blank lines at both ends.
notes=$(printf '%s\n' "$notes" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')
if [ -z "$notes" ]; then
	echo "release-notes: CHANGELOG.md has no \"## [$version]\" section; add one before tagging v$version" >&2
	exit 1
fi
printf '%s\n' "$notes"
