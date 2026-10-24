#!/bin/sh
# Checks that the relative links and pictures in the Markdown files point at files that exist.
set -eu
cd "$(dirname "$0")/.."
bad=0
for f in README.md CONTRIBUTING.md SECURITY.md AGENTS.md docs/*.md docs/img/*.md site/*.md; do
	dir=$(dirname "$f")
	for l in $(grep -oE '(\]\(|src=")[^)" #]+' "$f" | sed -E 's/^(\]\(|src=")//' | grep -vE '^(https?:|mailto:)'); do
		[ -e "$dir/$l" ] || { echo "$f: $l does not exist"; bad=1; }
	done
done
exit $bad
