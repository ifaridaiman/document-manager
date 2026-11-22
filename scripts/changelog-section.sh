#!/bin/sh
# Prints the CHANGELOG.md section for one version, without its heading.
#   sh scripts/changelog-section.sh 0.2.1
# Exits 1 if the version has no "## [x.y.z]" section or the section is empty,
# which stops the release workflow before anything is published.
set -eu
cd "$(dirname "$0")/.."
version=${1:?usage: changelog-section.sh <version>}
awk -v heading="## [$version]" '
  /^## \[/ { if (found) exit; if (index($0, heading) == 1) { found = 1; next } }
  /^\[[^]]+\]: / { if (found) exit }   # link references at the bottom
  found { print; if (NF) body = 1 }
  END {
    if (!found) { print "CHANGELOG.md has no section \"" heading "\"" > "/dev/stderr"; exit 1 }
    if (!body)  { print "CHANGELOG.md section \"" heading "\" is empty" > "/dev/stderr"; exit 1 }
  }
' CHANGELOG.md
