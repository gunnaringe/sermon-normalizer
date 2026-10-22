#!/bin/sh
# Waits until every check of a pull request has finished, then prints one line per check and, for each one that
# failed, the end of its log. Exits non-zero when a check failed.
#
#   scripts/ci-wait.sh [pr]      default: this branch's pull request
set -eu
cd "$(dirname "$0")/.."
pr=${1:-$(gh pr view --json number --jq .number)}
# Right after a push the checks of the new commit take a moment to appear.
n=0
until [ "$(gh pr checks "$pr" --json name --jq length 2>/dev/null || echo 0)" -gt 0 ]; do
	n=$((n + 1))
	[ "$n" -lt 30 ] || { echo "#$pr has no checks after 5 minutes" >&2; exit 1; }
	sleep 10
done
while gh pr checks "$pr" --json bucket --jq '.[].bucket' | grep -q pending; do
	sleep 20
done
gh pr checks "$pr" --json bucket,name --jq '.[] | "\(.bucket)\t\(.name)"' | sort
failed=$(gh pr checks "$pr" --json bucket,link --jq '.[] | select(.bucket == "fail") | .link' | sed -nE 's|.*/job/([0-9]+).*|\1|p')
for job in $failed; do
	echo "--- the end of failed job $job ---"
	gh run view --job "$job" --log-failed | cut -f3- | sed -E 's/^[0-9T:.-]+Z //' | tail -40
done
[ -z "$failed" ]
