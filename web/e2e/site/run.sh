#!/bin/sh
# The website's test: site/ (mounted at /site) served as files, checked with Playwright. Runs inside the
# Playwright image; start it with: scripts/test.sh site
set -eu
cd "$(dirname "$0")/.."

# node_modules is kept between runs (it is in the mounted folder): installed again only when the lock file changes.
lock=$(sha256sum package-lock.json | cut -c1-64)
[ "$(cat node_modules/.lock-sha 2>/dev/null)" = "$lock" ] || { npm ci --silent --no-audit --no-fund && echo "$lock" >node_modules/.lock-sha; }
SITE=/site npx playwright test --config site/playwright.config.js "$@"
