#!/bin/sh
# Lands a pull request the user has said may be merged: waits for its checks (scripts/ci-wait.sh), squash-merges
# it, checks that main has each of its files as the pull request had them, deletes its branch and brings the
# local main up to date.
#
#   scripts/pr-land.sh [pr]      default: this branch's pull request
set -eu
cd "$(dirname "$0")/.."
pr=${1:-$(gh pr view --json number --jq .number)}
scripts/ci-wait.sh "$pr"
branch=$(gh pr view "$pr" --json headRefName --jq .headRefName)
gh pr view "$pr" --json files --jq '.files[].path' > "${TMPDIR:-/tmp}/pr-$pr-files"
gh pr merge "$pr" --squash --delete-branch >/dev/null
git fetch -q origin main "pull/$pr/head:refs/pr-land/$pr"
bad=
while IFS= read -r f; do
	if git cat-file -e "refs/pr-land/$pr:$f" 2>/dev/null; then
		git diff --quiet "refs/pr-land/$pr" origin/main -- "$f" || { echo "main's $f is not the pull request's" >&2; bad=1; }
	elif git cat-file -e "origin/main:$f" 2>/dev/null; then
		echo "$f was removed in the pull request, but main has it" >&2
		bad=1
	fi
done < "${TMPDIR:-/tmp}/pr-$pr-files"
git update-ref -d "refs/pr-land/$pr"
[ -z "$bad" ] || exit 1
git switch -q main && git pull -q --ff-only
git branch -D "$branch" >/dev/null 2>&1 || true
echo "#$pr is in main: $(git log --oneline -1)"
