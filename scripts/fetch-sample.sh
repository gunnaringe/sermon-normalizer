#!/bin/sh
# Downloads the speech the documentation screenshots are made from: President Kennedy's
# inaugural address (1961, about 14 minutes), a US government work in the public domain,
# from Wikimedia Commons. Not committed (6 MB): web/e2e/fixtures/ is ignored.
set -eu
cd "$(dirname "$0")/../web/e2e/fixtures"
[ -s jfk-inaugural.ogg ] && exit 0
curl -fsSL -o jfk-inaugural.ogg 'https://upload.wikimedia.org/wikipedia/commons/d/d5/JFK_inaugural_address.ogg'
echo "fetched $(pwd)/jfk-inaugural.ogg"
