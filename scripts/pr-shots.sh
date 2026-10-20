#!/usr/bin/env bash
# Puts screenshots where a PR description can show them, and prints the
# Markdown. The images go to the orphan branch "pr-shots" (folder = the current
# branch), never into main's history; the links point at that exact commit,
# so a later upload does not change an older PR.
#
#   scripts/test.sh e2e [-g name]        # makes web/e2e/screenshots/*.png
#   scripts/pr-shots.sh editor phone-editor /tmp/other.png
#   scripts/pr-shots.sh --pr editor      # also appends the Markdown to this branch's PR
#
# A name without a slash or .png is web/e2e/screenshots/<name>.png. Images are
# made smaller with pngquant if it is installed. Narrow images (phones) are
# shown 260 px wide, others 640 px.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

pr=false
if [ "${1:-}" = "--pr" ]; then pr=true; shift; fi
[ $# -gt 0 ] || { sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

repo="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
branch="$(git rev-parse --abbrev-ref HEAD)"
[ "$branch" != main ] || { echo "on main: make a branch for the PR first" >&2; exit 1; }
shots=pr-shots

git fetch -q origin "$shots" 2>/dev/null || true
parent="$(git rev-parse -q --verify "origin/$shots" || true)"
export GIT_INDEX_FILE
GIT_INDEX_FILE="$(mktemp)"
tmp="$(mktemp -d)"
trap 'rm -rf "$GIT_INDEX_FILE" "$tmp"' EXIT
if [ -n "$parent" ]; then git read-tree "$parent"; else git read-tree --empty; fi

md=()
for arg in "$@"; do
  file="$arg"
  case "$arg" in */* | *.png) ;; *) file="web/e2e/screenshots/$arg.png" ;; esac
  [ -f "$file" ] || { echo "no such image: $file" >&2; exit 1; }
  name="$(basename "$file" .png)"
  small="$tmp/$name.png"
  if command -v pngquant >/dev/null; then
    pngquant --quality 65-90 --strip --force --output "$small" "$file" 2>/dev/null || cp "$file" "$small"
  else
    cp "$file" "$small"
  fi
  blob="$(git hash-object -w "$small")"
  git update-index --add --cacheinfo "100644,$blob,$branch/$name.png"
  width="$(file "$small" | sed -n 's/.*PNG image data, \([0-9]*\) x.*/\1/p')"
  [ "${width:-1000}" -le 500 ] && show=260 || show=640
  md+=("$name|$show")
done

tree="$(git write-tree)"
commit="$(git commit-tree "$tree" ${parent:+-p "$parent"} -m "Screenshots for $branch")"
if ! log="$(git push -q origin "$commit:refs/heads/$shots" 2>&1)"; then echo "$log" >&2; exit 1; fi

out=""
for entry in "${md[@]}"; do
  name="${entry%|*}" show="${entry#*|}"
  out+="**$name**"$'\n\n'"<img src=\"https://raw.githubusercontent.com/$repo/$commit/$branch/$name.png\" alt=\"$name\" width=\"$show\">"$'\n\n'
done
printf '%s' "$out"

if $pr; then
  body="$(gh pr view --json body -q .body)"
  gh pr edit --body "$body"$'\n\n'"## Screenshots"$'\n\n'"$out" >/dev/null
  echo "(appended to the PR)" >&2
fi
