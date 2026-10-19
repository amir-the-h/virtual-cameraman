#!/usr/bin/env bash
# Packs the deck plugin into build/deckd/me.the-h.virtualcameraman.streamDeckPlugin and writes
# build/deckd/index.json, so a release can serve as a deckd plugin source.
set -euo pipefail
cd "$(dirname "$0")"
out=../../build/deckd
mkdir -p "$out"
plugin=me.the-h.virtualcameraman.sdPlugin
rm -f "$out/${plugin%.sdPlugin}.streamDeckPlugin"
zip -qrX "$out/${plugin%.sdPlugin}.streamDeckPlugin" "$plugin"
node build-index.mjs "$out" "Virtual Cameraman" https://github.com/amir-the-h/virtual-cameraman "$plugin"
