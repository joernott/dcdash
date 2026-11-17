#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Prints the section of CHANGELOG.md for one version, e.g. "0.1.0", as release
# notes. Fails if the section is missing or empty.
set -euo pipefail
version="${1:?usage: release-notes.sh <version>}"
notes=$(awk -v v="$version" '
  /^## \[/ { if (found) exit; found = index($0, "## [" v "]") == 1; next }
  /^\[[^]]+\]: / { next }
  found { print }
' "$(dirname "$0")/../CHANGELOG.md" | sed -e '/./,$!d')
if [[ -z "${notes//[$'\n ']/}" ]]; then
  echo "CHANGELOG.md has no section for $version" >&2
  exit 1
fi
printf '%s\n' "$notes"
