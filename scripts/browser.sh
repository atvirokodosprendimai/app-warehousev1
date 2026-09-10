#!/usr/bin/env bash
# Drive the real application through a real browser.
#
# ⚠ THIS EXISTS BECAUSE EIGHT DEFECTS HAVE NOW BEEN FOUND BY A PERSON AFTER THE
# SERVER-SIDE SUITE WAS GREEN, and every one was invisible to that suite BY
# CONSTRUCTION: an upload that never fired, photographs that could not be opened
# on a phone, a saved batch with no link to it, places that could not be edited,
# a New offer button missing from every page but two, and a questions card that
# never appeared when a category was chosen. A handler test renders markup; it
# cannot see whether datastar hydrated, whether a patch was applied, whether a
# control is reachable, or whether the thing fits on a phone.
#
# ⚠ AND THE WALKS THEMSELVES LIVED IN A TEMP DIRECTORY UNTIL 2026-09-07. Two of
# them had each already caught a real defect and both would have been deleted
# with the session that wrote them. A check that does not survive its author is
# a demonstration, not a check.
#
# Usage:
#   scripts/browser.sh              # every walk
#   scripts/browser.sh mobile       # one of them, by basename
#
# Each walk prints BROWSER_FAILURES=<n> and exits with it, so this script's own
# status is the verdict.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
HERE="$REPO/scripts/browser"
PORT="${PORT:-18099}"
SHOTS="${SHOTS:-$HERE/shots}"

if [ ! -d "$HERE/node_modules" ]; then
  echo "playwright is not installed. Run:"
  echo "  (cd $HERE && npm ci && npx playwright install chromium)"
  exit 1
fi

walks=()
if [ $# -gt 0 ]; then
  for name in "$@"; do walks+=("$HERE/$name.js"); done
else
  for f in "$HERE"/*.js; do walks+=("$f"); done
fi

S="$(mktemp -d)"
mkdir -p "$SHOTS"

echo "== build =="
if ! ( cd "$REPO" && go build -o "$S/warehouse" ./cmd/warehouse ); then
  echo "BUILD FAILED"; rm -rf "$S"; exit 1
fi

failures=0
for walk in "${walks[@]}"; do
  name="$(basename "$walk" .js)"
  echo
  echo "######## $name ########"

  # ⚠ A FRESH DATABASE PER WALK, and it is not hygiene: every walk bootstraps the
  # first administrator, and that route is closed for ever once an account
  # exists (ADR-006). A second walk against a used database fails at its first
  # step for a reason that has nothing to do with what it tests.
  D="$S/$name"
  mkdir -p "$D"

  ADDR="127.0.0.1:$PORT" \
  DB_PATH="$D/w.db" \
  PHOTOS_DIR="$D/photos" \
  PUBLIC_BASE_URL="http://127.0.0.1:$PORT" \
  FETCH_RATES=false \
  EBAY_CATEGORY=11450 \
  EBAY_LOCATION=Kaunas \
  "$S/warehouse" > "$D/server.log" 2>&1 &
  server=$!

  if ! curl -fsS --retry 40 --retry-delay 1 --retry-all-errors \
       "http://127.0.0.1:$PORT/healthz" -o /dev/null; then
    echo "  FAIL server never became healthy"
    cat "$D/server.log"
    kill $server 2>/dev/null
    failures=$((failures + 1))
    continue
  fi

  BASE="http://127.0.0.1:$PORT" SHOTS="$SHOTS" \
    node "$walk"
  # The walk's own exit status is the verdict; nothing may mask it.
  rc=$?
  if [ "$rc" -ne 0 ]; then
    failures=$((failures + 1))
    echo "  (server log tail)"
    tail -20 "$D/server.log"
  fi

  kill $server 2>/dev/null
  wait $server 2>/dev/null
  PORT=$((PORT + 1))
done

rm -rf "$S"
echo
echo "BROWSER_WALK_FAILURES=$failures"
exit $failures
