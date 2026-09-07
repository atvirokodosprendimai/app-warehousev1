#!/usr/bin/env bash
# End-to-end smoke test against the real binary.
#
# It drives the actual HTTP surface rather than calling handlers directly,
# because what is most likely to be wrong here is the wiring, the session cookie
# and the SSE framing — none of which a unit test touches.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
S="$(mktemp -d)"
RUN="$S/run"
mkdir -p "$RUN"
trap 'rm -rf "$S"' EXIT

echo "== build =="
( cd "$REPO" && go build -o "$S/warehouse" ./cmd/warehouse ) || {
  echo "  FAIL could not build the binary"; exit 1; }
echo "  ok   binary built"

PORT=18080
BASE="http://127.0.0.1:$PORT"
COOKIES="$RUN/cookies.txt"

fail=0

# check asserts a substring is present in a file that MUST exist and be
# non-empty. The existence check is not paranoia: an earlier version of this
# script asserted "the owner price is absent" against a file curl had never
# written, so grep found nothing and the check passed while testing nothing.
check() { # check <name> <expected-substring> <file>
  if [ ! -s "$3" ]; then
    echo "  FAIL $1 — $3 is missing or empty, so this assertion proves nothing"
    fail=$((fail + 1))
    return
  fi
  if grep -q -- "$2" "$3"; then
    echo "  ok   $1"
  else
    echo "  FAIL $1 — expected to find: $2"
    echo "       got: $(head -c 300 "$3")"
    fail=$((fail + 1))
  fi
}

# absent is the same discipline for a negative assertion.
absent() { # absent <name> <forbidden-substring> <file>
  if [ ! -s "$3" ]; then
    echo "  FAIL $1 — $3 is missing or empty, so 'absent' proves nothing"
    fail=$((fail + 1))
    return
  fi
  if grep -q -- "$2" "$3"; then
    echo "  FAIL $1 — found what must not be there: $2"
    fail=$((fail + 1))
  else
    echo "  ok   $1"
  fi
}

echo "== build the image fixture =="
( cd "$REPO" && go run ./scripts/mkimg "$RUN/photo.png" ) || {
  echo "  FAIL could not create the test image"; exit 1; }
[ -s "$RUN/photo.png" ] && echo "  ok   image fixture created"

# ── .env, before the main server starts ──────────────────────────────────────
#
# Everything below this block sets REAL environment variables, and a real
# variable wins over a .env — so nothing else in this script could ever exercise
# the file. This runs the binary twice more, from a directory that has one, and
# asserts both halves of the rule: the file is read, and a real variable still
# beats it. The second half is the one that matters: if the file won, a one-off
# override would silently do nothing.
echo "== .env =="
EDIR="$RUN/envtest"
mkdir -p "$EDIR"
EPORT=$((PORT + 1))
{
  echo "# a comment, then a blank line"
  echo ""
  echo "ADDR=127.0.0.1:$EPORT"
  echo "PUBLIC_BASE_URL=https://from-the-file.example.com"
  echo "DB_PATH=$EDIR/e.db"
  echo "PHOTOS_DIR=$EDIR/photos"
  echo "FETCH_RATES=false"
  echo "export EBAY_LOCATION='Kaunas, Lithuania'   # quoted, exported, commented"
} > "$EDIR/.env"

# ⚠ `exec`, and the reason is not style. Without it, `( cd X && cmd ) &`
# backgrounds the whole AND-list, so $! is the SUBSHELL's pid and the server is
# its child — the kill below then reaps the wrapper and leaves the server
# holding the port. That happened: a stray process survived a run and the next
# one silently connected to it, so an assertion about the CURRENT binary passed
# or failed depending on what an earlier run had left behind. `exec` replaces
# the subshell with the server, so $! is the process this script must kill.
( cd "$EDIR" && exec "$S/warehouse" > "$EDIR/boot.log" 2>&1 ) & echo $! > "$EDIR/pid"
curl -fsS --retry 20 --retry-delay 1 --retry-all-errors \
  "http://127.0.0.1:$EPORT/healthz" -o /dev/null 2>/dev/null
kill "$(cat "$EDIR/pid")" 2>/dev/null
check ".env is read at start-up"          "configuration file loaded"        "$EDIR/boot.log"
check ".env supplies the public address"  "https://from-the-file.example.com" "$EDIR/boot.log"
check ".env supplies the listen address"  "127.0.0.1:$EPORT"                 "$EDIR/boot.log"

( cd "$EDIR" && PUBLIC_BASE_URL="https://from-the-environment.example.com" \
  exec "$S/warehouse" > "$EDIR/boot2.log" 2>&1 ) & echo $! > "$EDIR/pid2"
curl -fsS --retry 20 --retry-delay 1 --retry-all-errors \
  "http://127.0.0.1:$EPORT/healthz" -o /dev/null 2>/dev/null
kill "$(cat "$EDIR/pid2")" 2>/dev/null
check  "a real variable beats .env"   "https://from-the-environment.example.com" "$EDIR/boot2.log"
absent "the .env value did not win"   "from-the-file.example.com"                "$EDIR/boot2.log"

ADDR=":$PORT" \
DB_PATH="$RUN/w.db" \
PHOTOS_DIR="$RUN/photos" \
PUBLIC_BASE_URL="$BASE" \
FETCH_RATES=false \
EBAY_CATEGORY=11450 \
EBAY_LOCATION=Kaunas \
"$S/warehouse" > "$RUN/server.log" 2>&1 &
SERVER=$!
trap 'kill $SERVER 2>/dev/null; rm -rf "$S"' EXIT

if ! curl -fsS --retry 30 --retry-delay 1 --retry-all-errors "$BASE/healthz" -o "$RUN/health.txt"; then
  echo "  FAIL server never became healthy"; cat "$RUN/server.log"; exit 1
fi
check "health" "ok" "$RUN/health.txt"

echo "== bootstrap =="
curl -fsS "$BASE/bootstrap" -o "$RUN/bootstrap.html"
check "bootstrap page offered" "Create the first administrator" "$RUN/bootstrap.html"

curl -fsS -c "$COOKIES" -X POST "$BASE/bootstrap" \
  -H 'Content-Type: application/json' \
  -d '{"authEmail":"m@example.com","authPassword":"correct-horse-battery","authName":"M"}' \
  -o "$RUN/bootstrap-post.txt"
check "bootstrap signs the new admin in" "window.location" "$RUN/bootstrap-post.txt"

echo "== bootstrap closes permanently =="
curl -fsS -L "$BASE/bootstrap" -o "$RUN/bootstrap2.html"
check "a second visit is the sign-in page" "Accounts are created by an administrator" "$RUN/bootstrap2.html"
absent "no second admin can be self-created" "Create the first administrator" "$RUN/bootstrap2.html"

echo "== signed in =="
curl -fsS -b "$COOKIES" "$BASE/" -o "$RUN/dash.html"
check "dashboard renders" "Dashboard" "$RUN/dash.html"
check "one stream per page" 'data-init="@get(&#39;/stream&#39;)"' "$RUN/dash.html"

echo "== warehouse, held by someone else in another city =="
curl -fsS -b "$COOKIES" -X POST "$BASE/warehouse" \
  -H 'Content-Type: application/json' \
  -d '{"locParent":"","locKind":"site","locCode":"KAUNAS","locLabel":"garage","locCustodian":"Jonas","locContact":"+37060000000","locCity":"Kaunas","locCountry":"LT"}' \
  -o "$RUN/loc.txt"
check "site created" "window.location" "$RUN/loc.txt"

curl -fsS -b "$COOKIES" "$BASE/warehouse" -o "$RUN/warehouse.html"
check "site listed" "KAUNAS" "$RUN/warehouse.html"
check "custodian shown" "Jonas" "$RUN/warehouse.html"
check "city shown" "Kaunas" "$RUN/warehouse.html"
check "add-inside carries the parent id" "warehouse/new?parent=" "$RUN/warehouse.html"

LOC_ID=$(grep -o 'warehouse/new?parent=[0-9a-f-]\{36\}' "$RUN/warehouse.html" | head -1 | cut -d= -f2)
curl -fsS -b "$COOKIES" "$BASE/warehouse/new?parent=$LOC_ID" -o "$RUN/newloc.html"
check "the preselected parent is in the form's signals" "$LOC_ID" "$RUN/newloc.html"
check "kind defaults to shelf, not the impossible site-inside-a-site" "locKind: &#34;shelf&#34;" "$RUN/newloc.html"

echo "== places are editable =="
# A shelf inside the site, so a rename can be shown to cascade.
curl -fsS -b "$COOKIES" -X POST "$BASE/warehouse" \
  -H 'Content-Type: application/json' \
  -d "{\"locParent\":\"$LOC_ID\",\"locKind\":\"shelf\",\"locCode\":\"S3\",\"locLabel\":\"by the door\",\"locCustodian\":\"\",\"locContact\":\"\",\"locCity\":\"\",\"locCountry\":\"\"}" \
  -o /dev/null
curl -fsS -b "$COOKIES" "$BASE/warehouse" -o "$RUN/wh2.html"
check "the shelf is listed under the site" "KAUNAS/S3" "$RUN/wh2.html"
check "a place links to its own editor" "/warehouse/$LOC_ID" "$RUN/wh2.html"

curl -fsS -b "$COOKIES" "$BASE/warehouse/$LOC_ID" -o "$RUN/place.html"
check "the editor opens" "Held by" "$RUN/place.html"
check "and /warehouse/new still wins over the id route" "Add a place" "$RUN/newloc.html"

curl -fsS -b "$COOKIES" -X POST "$BASE/warehouse/$LOC_ID/details" \
  -H 'Content-Type: application/json' \
  -d '{"placeLabel":"the big garage","placeCustodian":"Jonas","placeContact":"+37060000001","placeCity":"Kaunas","placeCountry":"LT","placeNotes":"gate code 1234"}' \
  -o "$RUN/pdetails.txt"
check "details save" "Saved" "$RUN/pdetails.txt"
# ⚠ Assert on the RESOLVED placement, not on the field values. Inputs are bound
# to signals, so a re-rendered fragment contains empty inputs and the browser's
# own signals still hold what was typed — the values are deliberately not echoed
# back into the HTML. What the server does render is what the change MEANS.
check "the custodian now resolves through the tree" "held by Jonas" "$RUN/pdetails.txt"

# ★ Renaming a place rewrites the address of everything inside it. If that ever
# stops happening, the tree answers "where is this" with a path resolving to
# nothing — and nothing else would report it.
curl -fsS -b "$COOKIES" -X POST "$BASE/warehouse/$LOC_ID/rename" \
  -H 'Content-Type: application/json' -d '{"placeCode":"KAUNAS-GARAGE"}' -o "$RUN/prename.txt"
check "rename succeeds" "Renamed" "$RUN/prename.txt"
curl -fsS -b "$COOKIES" "$BASE/warehouse" -o "$RUN/wh3.html"
check "the child's address followed the rename" "KAUNAS-GARAGE/S3" "$RUN/wh3.html"
absent "and the old address is gone" "KAUNAS/S3" "$RUN/wh3.html"

echo "== intake: one click, no body at all, then name it (ADR-020) =="
# ⚠ THE ABSENCE OF A REQUEST BODY IS THE ASSERTION, not an economy. The button
# lives in the top bar on every page, so the create endpoint must not depend on
# signals that only one screen declared. If it ever needs a body again, the
# button silently stops working everywhere except wherever that body comes from.
curl -fsS -b "$COOKIES" -X POST "$BASE/offers" \
  -H 'Content-Type: application/json' \
  -o "$RUN/offer.txt"
check "one click creates an offer with no input at all" "/offers/" "$RUN/offer.txt"
OFFER_ID=$(grep -o "/offers/[0-9a-f-]\{36\}" "$RUN/offer.txt" | head -1 | cut -d/ -f3)
echo "  offer id: $OFFER_ID"

# The intake SCREEN is gone, not merely unlinked. An address that still answers
# would be a second way to create an offer, reachable by anyone who bookmarked
# it, and nothing else in this script would notice.
NEWCODE=$(curl -s -b "$COOKIES" -o /dev/null -w '%{http_code}' "$BASE/offers/new")
if [ "$NEWCODE" = "404" ]; then
  echo "  ok   the intake screen is gone (/offers/new returns 404)"
else
  echo "  FAIL /offers/new still answers $NEWCODE — the deleted screen is still reachable"
  fail=$((fail + 1))
fi

# Step 3 of M's flow: "all other info", starting with the name. This is an
# ordinary save through the editor's own endpoint, not a special intake path.
curl -fsS -b "$COOKIES" -X POST "$BASE/offers/$OFFER_ID" \
  -H 'Content-Type: application/json' \
  -d '{"offerTitle":"Vintage brass desk lamp","offerDescription":"","offerCondition":""}' \
  -o "$RUN/named.txt"
check "naming it afterwards is an ordinary save" "Saved" "$RUN/named.txt"

curl -fsS -b "$COOKIES" "$BASE/offers/$OFFER_ID" -o "$RUN/offer.html"
check "offer page renders" "Vintage brass desk lamp" "$RUN/offer.html"
# ★ The reference is written on a box by hand and typed back into a search field,
# so it is a short ordinal rather than a dated hex string.
check "the first reference is WH0000001" "WH0000001" "$RUN/offer.html"
absent "no dated hex reference survives" "WH-2026" "$RUN/offer.html"

echo "== a reference is findable the way it is typed =="
for q in WH0000001 wh0000001 WH1 1; do
  curl -fsS -b "$COOKIES" "$BASE/offers/rows" \
    --get --data-urlencode "datastar={\"searchQuery\":\"$q\",\"priceMin\":\"\",\"priceMax\":\"\",\"priceCurrency\":\"EUR\"}" \
    -o "$RUN/find-$q.txt"
  check "searching \"$q\" finds it" "Vintage brass desk lamp" "$RUN/find-$q.txt"
done
check "unpriced draft says so" "pricing queue" "$RUN/offer.html"
check "the editor uses the SAME one stream endpoint" "/stream?offer=$OFFER_ID" "$RUN/offer.html"

curl -fsS -b "$COOKIES" "$BASE/offers?needs_pricing=1" -o "$RUN/pricing.html"
check "unpriced draft is in the pricing queue" "Vintage brass desk lamp" "$RUN/pricing.html"

# ★ ADR-019: THE TWO-PERSON INTAKE, WALKED END TO END.
#
# One person photographs a thing without naming it; a second person finds it in a
# queue and names it. This is the only assertion that proves the hand-off exists
# as an ADDRESS rather than as markup — the view tests prove the menu entry is
# rendered, and only an HTTP request proves the route behind it answers.
echo "== ADR-019: one person photographs, another describes =="
curl -fsS -b "$COOKIES" -X POST "$BASE/offers" \
  -H 'Content-Type: application/json' \
  -o "$RUN/unnamed.txt"
check "an offer can be created with NO title at all" "/offers/" "$RUN/unnamed.txt"
UNNAMED_ID=$(grep -o "/offers/[0-9a-f-]\{36\}" "$RUN/unnamed.txt" | head -1 | cut -d/ -f3)
echo "  unnamed offer id: $UNNAMED_ID"

curl -fsS -b "$COOKIES" "$BASE/offers?needs_describing=1" -o "$RUN/describing.html"
check "the unnamed group is in the describing queue" "Untitled"    "$RUN/describing.html"
# The reference is what is written on the box, so it has to be allocated at
# photograph time -- otherwise the photographer has nothing to label with.
check "and it carries a reference to label the box"  "WH000000"    "$RUN/describing.html"
check "the menu entry M asked for is on the page"    "Needs describing" "$RUN/describing.html"
absent "the named offer is NOT in the describing queue" "Vintage brass desk lamp" "$RUN/describing.html"

# The second person's side: the offer editor is the describing screen (ADR-019
# rejects a second one), so naming it is an ordinary save.
curl -fsS -b "$COOKIES" -X POST "$BASE/offers/$UNNAMED_ID" \
  -H 'Content-Type: application/json' \
  -d '{"offerTitle":"Enamel advertising sign","offerDescription":"chipped at one corner","offerCondition":"used"}' \
  -o "$RUN/described.txt"

curl -fsS -b "$COOKIES" "$BASE/offers?needs_describing=1" -o "$RUN/describing2.html"
absent "once named, it LEAVES the describing queue" "Enamel advertising sign" "$RUN/describing2.html"
curl -fsS -b "$COOKIES" "$BASE/offers/$UNNAMED_ID" -o "$RUN/described.html"
check "and the name stuck" "Enamel advertising sign" "$RUN/described.html"

# ⚠ The publication guard, over HTTP. A title is not required to CREATE and IS
# required to LIST -- refused by the domain and, separately, by a database
# trigger. Without this the relaxation would be indistinguishable from having
# removed the rule.
#
# ⚠ THE OFFER MUST BE PRICED FIRST, and that is the whole subtlety: an offer with
# neither a price nor a title is refused for the PRICE, because that guard is
# checked first. Asserting on that refusal would have proved the shop-price rule
# from ADR-004 over again and said nothing about the title. Priced-but-unnamed is
# the only state that isolates this rule.
curl -fsS -b "$COOKIES" -X POST "$BASE/offers" \
  -H 'Content-Type: application/json' \
  -o "$RUN/unnamed2.txt"
UNNAMED2_ID=$(grep -o "/offers/[0-9a-f-]\{36\}" "$RUN/unnamed2.txt" | head -1 | cut -d/ -f3)
curl -fsS -b "$COOKIES" -X POST "$BASE/offers/$UNNAMED2_ID/prices" \
  -H 'Content-Type: application/json' \
  -d '{"shopAmount":"45.00","shopCurrency":"EUR","ownerAmount":"","ownerCurrency":"EUR"}' \
  -o "$RUN/unnamed2-priced.txt"
curl -fsS -b "$COOKIES" -X POST "$BASE/offers/$UNNAMED2_ID/status/listed" \
  -H 'Content-Type: application/json' -d '{}' -o "$RUN/publish-unnamed.txt"
check "a priced but UNNAMED offer cannot be listed" "title" "$RUN/publish-unnamed.txt"
curl -fsS -b "$COOKIES" "$BASE/offers?needs_describing=1" -o "$RUN/describing3.html"
check "and it is still sitting in the describing queue" "Untitled" "$RUN/describing3.html"

echo "== the upload control the BROWSER will actually use =="
# ⚠ The curl upload below builds its own multipart body and therefore proves only
# that the SERVER accepts one. It says nothing about whether the page can produce
# one — and for a while it could not: the markup had no <form>, no enctype and no
# name, so datastar threw FetchFormNotFound and sent nothing at all while every
# assertion here stayed green. These three lines are that gap, closed on the
# served page. internal/web/view/upload_contract_test.go covers the same contract
# at the component level.
check "the upload sits in a real form" '<form enctype="multipart/form-data"' "$RUN/offer.html"
check "the file input is named, or FormData omits it" 'type="file" name="photos"' "$RUN/offer.html"
check "the upload posts as form encoding" "contentType: &#39;form&#39;" "$RUN/offer.html"

echo "== photograph =="
curl -fsS -b "$COOKIES" -X POST "$BASE/offers/$OFFER_ID/photos" \
  -F "file=@$RUN/photo.png" -o "$RUN/photo-post.txt"
check "photograph accepted" "Photographs added" "$RUN/photo-post.txt"

PHOTO_URL=$(grep -o '/p/[0-9a-f-]\{36\}\.png' "$RUN/photo-post.txt" | head -1)
echo "  photo url: $PHOTO_URL"
# ★ The property the whole export depends on: a marketplace fetches this with no
# session at all, so it must work WITHOUT the cookie.
curl -fsS "$BASE$PHOTO_URL" -o "$RUN/fetched.png"
if cmp -s "$RUN/photo.png" "$RUN/fetched.png"; then
  echo "  ok   photo fetches anonymously, byte-identical"
else
  echo "  FAIL an anonymous fetch of the photo did not return the original bytes"
  fail=$((fail + 1))
fi

echo "== pricing =="
curl -fsS -b "$COOKIES" -X POST "$BASE/offers/$OFFER_ID/prices" \
  -H 'Content-Type: application/json' \
  -d '{"shopAmount":"45,00","shopCurrency":"EUR","ownerAmount":"30.00","ownerCurrency":"EUR"}' \
  -o "$RUN/prices.txt"
check "margin computed" "Margin 15.00 EUR" "$RUN/prices.txt"
check "comma decimal accepted" "45.00 EUR" "$RUN/prices.txt"

curl -fsS -b "$COOKIES" -X POST "$BASE/offers/$OFFER_ID/status/listed" \
  -H 'Content-Type: application/json' -d '{}' -o "$RUN/status.txt"
check "now listed" "Listed" "$RUN/status.txt"

echo "== search =="
curl -fsS -b "$COOKIES" "$BASE/offers/rows" \
  --get --data-urlencode 'datastar={"searchQuery":"brass lamp","priceMin":"","priceMax":"","priceCurrency":"EUR"}' \
  -o "$RUN/search.txt"
check "words in any order match" "Vintage brass desk lamp" "$RUN/search.txt"

curl -fsS -b "$COOKIES" "$BASE/offers/rows" \
  --get --data-urlencode 'datastar={"searchQuery":"zzzznothing","priceMin":"","priceMax":"","priceCurrency":"EUR"}' \
  -o "$RUN/search-none.txt"
check "a non-matching search returns nothing, not everything" "Nothing here yet" "$RUN/search-none.txt"

echo "== price range =="
curl -fsS -b "$COOKIES" "$BASE/offers/rows" \
  --get --data-urlencode 'datastar={"searchQuery":"","priceMin":"100","priceMax":"","priceCurrency":"EUR"}' \
  -o "$RUN/price-out.txt"
check "below the minimum is excluded" "Nothing here yet" "$RUN/price-out.txt"

curl -fsS -b "$COOKIES" "$BASE/offers/rows" \
  --get --data-urlencode 'datastar={"searchQuery":"","priceMin":"10","priceMax":"100","priceCurrency":"EUR"}' \
  -o "$RUN/price-in.txt"
check "inside the range is included" "Vintage brass desk lamp" "$RUN/price-in.txt"

curl -fsS -b "$COOKIES" "$BASE/offers/rows" \
  --get --data-urlencode 'datastar={"searchQuery":"","priceMin":"10","priceMax":"100","priceCurrency":"JPY"}' \
  -o "$RUN/price-cur.txt"
check "a range in another currency does not match a EUR price" "Nothing here yet" "$RUN/price-cur.txt"

echo "== carts are reachable and buildable from the listing =="
# The feature was unusable because nothing linked to it: no nav entry, and Add
# lived only on a card at the bottom of an already-opened offer.
curl -fsS -b "$COOKIES" "$BASE/offers" -o "$RUN/offers.html"
check "the sidebar links to carts" 'href="/carts"' "$RUN/offers.html"
check "the listing carries a cart bar" 'id="cart-bar"' "$RUN/offers.html"
check "and an Add control per row" "/carts/add/$OFFER_ID" "$RUN/offers.html"

# ★ No cart exists yet. Adding must CREATE one rather than refusing, which is the
# flow as described: collect things, then name what you collected.
curl -fsS -b "$COOKIES" -X POST "$BASE/carts/add/$OFFER_ID" \
  -H 'Content-Type: application/json' -d '{"activeCart":""}' -o "$RUN/cartadd1.txt"
check "adding with no cart creates one" "Cart " "$RUN/cartadd1.txt"
CART_ID=$(curl -fsS -b "$COOKIES" "$BASE/carts" | grep -o '/carts/[0-9a-f-]\{36\}' | head -1 | cut -d/ -f3)
echo "  cart id: $CART_ID"

curl -fsS -b "$COOKIES" "$BASE/carts/$CART_ID" -o "$RUN/cartpage.html"
check "the item is in the cart" "Vintage brass desk lamp" "$RUN/cartpage.html"

# A second add must reuse the same cart, not mint another.
curl -fsS -b "$COOKIES" -X POST "$BASE/carts/add/$OFFER_ID" \
  -H 'Content-Type: application/json' -d "{\"activeCart\":\"$CART_ID\"}" -o /dev/null
if [ "$(curl -fsS -b "$COOKIES" "$BASE/carts" | grep -o '/carts/[0-9a-f-]\{36\}' | sort -u | wc -l)" = "1" ]; then
  echo "  ok   a second add reuses the same cart"
else
  echo "  FAIL a second add created another cart"
  fail=$((fail + 1))
fi

curl -fsS -b "$COOKIES" -X POST "$BASE/carts" \
  -H 'Content-Type: application/json' \
  -d '{"cartName":"eBay September","cartNote":"first cart"}' -o "$RUN/cart.txt"
check "a cart can also be created and named up front" "eBay September" "$RUN/cart.txt"

echo "== export =="
curl -fsS -b "$COOKIES" "$BASE/export" -o "$RUN/export.html"
check "export page says how many are ready" "1 ready" "$RUN/export.html"

curl -fsS -b "$COOKIES" "$BASE/export/shopify.csv" -o "$RUN/shopify.csv"
check "shopify header" "Variant Price" "$RUN/shopify.csv"
check "shopify carries the shop price" "45.00" "$RUN/shopify.csv"
check "shopify carries an absolute photo URL" "$BASE/p/" "$RUN/shopify.csv"
absent "the OWNER price never reaches the shopify export" "30.00" "$RUN/shopify.csv"

curl -fsS -b "$COOKIES" "$BASE/export/ebay.csv" -o "$RUN/ebay.csv"
check "ebay header" "PicURL" "$RUN/ebay.csv"
check "ebay carries the shop price" "45.00" "$RUN/ebay.csv"
absent "the OWNER price never reaches the ebay export" "30.00" "$RUN/ebay.csv"

curl -fsS -b "$COOKIES" "$BASE/export/shopify.csv?cart=$CART_ID" -o "$RUN/cart.csv"
check "a batch exports on its own" "Vintage brass desk lamp" "$RUN/cart.csv"

echo "== submissions inbox =="
STAFF="$RUN/staff.txt"
curl -fsS -b "$COOKIES" -X POST "$BASE/users" \
  -H 'Content-Type: application/json' \
  -d '{"userEmail":"petras@example.com","userName":"Petras","userPassword":"another-long-one","userAdmin":false}' \
  -o "$RUN/newuser.txt"
check "admin created a staff account" "Created petras@example.com" "$RUN/newuser.txt"

curl -fsS -c "$STAFF" -X POST "$BASE/login" -H 'Content-Type: application/json' \
  -d '{"authEmail":"petras@example.com","authPassword":"another-long-one"}' -o "$RUN/stafflogin.txt"
check "staff can sign in" "window.location" "$RUN/stafflogin.txt"

curl -fsS -b "$STAFF" "$BASE/" -o "$RUN/staffdash.html"
absent "staff do not see the admin inbox link" '"/inbox"' "$RUN/staffdash.html"

curl -fsS -b "$STAFF" -X POST "$BASE/submit" -H 'Content-Type: application/json' \
  -d '{"subTitle":"Oak dining chair","subNote":"one of six, sound joints","subAmount":"20.00","subCurrency":"EUR"}' \
  -o "$RUN/submit.txt"
check "staff submission accepted" "/submit/" "$RUN/submit.txt"
SUB_ID=$(grep -o "/submit/[0-9a-f-]\{36\}" "$RUN/submit.txt" | head -1 | cut -d/ -f3)
echo "  submission id: $SUB_ID"

curl -fsS -b "$STAFF" -X POST "$BASE/submit/$SUB_ID/photos" -F "file=@$RUN/photo.png" -o "$RUN/subphoto.txt"
check "photograph attached to the submission" "Saved" "$RUN/subphoto.txt"
SUB_PHOTO=$(grep -o '/p/[0-9a-f-]\{36\}\.png' "$RUN/subphoto.txt" | head -1)
echo "  submission photo: $SUB_PHOTO"

echo "== the admin sees it, live =="
curl -fsS -b "$COOKIES" "$BASE/" -o "$RUN/dash2.html"
check "the banner counts the waiting submission" "new submission waiting" "$RUN/dash2.html"
curl -fsS -b "$COOKIES" "$BASE/inbox" -o "$RUN/inbox.html"
check "inbox lists it" "Oak dining chair" "$RUN/inbox.html"
check "inbox shows who sent it" "Petras" "$RUN/inbox.html"
check "inbox shows what they want" "20.00 EUR" "$RUN/inbox.html"

echo "== a staff member cannot decide =="
curl -s -b "$STAFF" -X POST "$BASE/submit/$SUB_ID/accept" -H 'Content-Type: application/json' \
  -d '{}' -o /dev/null -w '%{http_code}' > "$RUN/staffaccept.code"
check "staff are refused the accept route" "403" "$RUN/staffaccept.code"

echo "== decline needs a reason =="
curl -fsS -b "$COOKIES" -X POST "$BASE/submit/$SUB_ID/decline" -H 'Content-Type: application/json' \
  -d '{"decNote":""}' -o "$RUN/decline-empty.txt"
check "a bare no is refused" "Give a reason" "$RUN/decline-empty.txt"

echo "== accept with a note, and it becomes a listing =="
curl -fsS -b "$COOKIES" -X POST "$BASE/submit/$SUB_ID/accept" \
  -H 'Content-Type: application/json' \
  -d "{\"decNote\":\"agreed 20 EUR, collecting Tuesday\",\"decShopAmount\":\"60.00\",\"decShopCurrency\":\"EUR\",\"decOwnerAmount\":\"\",\"decOwnerCurrency\":\"EUR\",\"decLocation\":\"$LOC_ID\",\"decSku\":\"\",\"decCondition\":\"used - good\",\"decDescription\":\"Solid oak.\"}" \
  -o "$RUN/accept.txt"
check "accepting navigates to the new listing" "/offers/" "$RUN/accept.txt"
NEW_OFFER=$(grep -o "/offers/[0-9a-f-]\{36\}" "$RUN/accept.txt" | head -1 | cut -d/ -f3)
echo "  new offer id: $NEW_OFFER"

curl -fsS -b "$COOKIES" "$BASE/offers/$NEW_OFFER" -o "$RUN/converted.html"
check "the listing carries the title" "Oak dining chair" "$RUN/converted.html"
check "the shop price is what was agreed" "60.00" "$RUN/converted.html"
# ★ The owner price fell back to what the submitter ASKED for, because the
# accept form left it blank. That fallback is the whole reason Asking exists.
check "the owner price defaulted to what they asked" "20.00" "$RUN/converted.html"
# ★ The photograph kept its id, so a link shared before acceptance still works.
check "the photo id survived conversion" "$SUB_PHOTO" "$RUN/converted.html"
curl -fsS "$BASE$SUB_PHOTO" -o "$RUN/moved.png"
if cmp -s "$RUN/photo.png" "$RUN/moved.png"; then
  echo "  ok   the photo still serves at the same public URL after conversion"
else
  echo "  FAIL the photo URL broke when the submission became an offer"
  fail=$((fail + 1))
fi

curl -fsS -b "$COOKIES" "$BASE/submit/$SUB_ID" -o "$RUN/subafter.html"
check "the agreed note is kept on the submission" "agreed 20 EUR, collecting Tuesday" "$RUN/subafter.html"

curl -fsS -b "$COOKIES" "$BASE/" -o "$RUN/dash3.html"
absent "the banner clears once nothing is waiting" "new submission waiting" "$RUN/dash3.html"

echo "== the public domain is settable, and it reaches the export =="
curl -fsS -b "$COOKIES" "$BASE/settings" -o "$RUN/settings.html"
check "settings shows the address in use" "$BASE" "$RUN/settings.html"
check "and warns a marketplace cannot fetch from it" "only resolves on this machine" "$RUN/settings.html"

curl -s -b "$STAFF" "$BASE/settings" -o /dev/null -w '%{http_code}' > "$RUN/staffset.code"
check "staff cannot open settings" "403" "$RUN/staffset.code"

# A trailing slash is sent on purpose: the value must come back normalised, and
# the box must show what was STORED rather than what was typed.
curl -fsS -b "$COOKIES" -X POST "$BASE/settings" -H 'Content-Type: application/json' \
  -d '{"setPublicBase":"https://warehouse.example.com/"}' -o "$RUN/setpost.txt"
check "domain saved, trailing slash removed" "https://warehouse.example.com<" "$RUN/setpost.txt"
absent "the unreachable warning clears once a real domain is set" "only resolves on this machine" "$RUN/setpost.txt"

curl -fsS -b "$COOKIES" -X POST "$BASE/settings" -H 'Content-Type: application/json' \
  -d '{"setPublicBase":"warehouse.example.com"}' -o "$RUN/setbad.txt"
check "an address with no scheme is refused" "must be an absolute address" "$RUN/setbad.txt"

# ★ The point of the whole feature: the stored domain must reach the exported
# file. Without this the setting could save, display correctly, and change
# nothing that anybody outside this machine ever sees.
curl -fsS -b "$COOKIES" "$BASE/export/shopify.csv" -o "$RUN/shopify2.csv"
check "the export now builds photo links from the saved domain" "https://warehouse.example.com/p/" "$RUN/shopify2.csv"
absent "and no longer from the start-up default" "$BASE/p/" "$RUN/shopify2.csv"

# Put it back, so anything added after this still sees a fetchable address.
curl -fsS -b "$COOKIES" -X POST "$BASE/settings" -H 'Content-Type: application/json' \
  -d "{\"setPublicBase\":\"$BASE\"}" -o /dev/null

echo "== auth boundary =="
curl -s "$BASE/offers" -o /dev/null -w '%{http_code}' > "$RUN/anon.code"
check "anonymous is redirected away from offers" "303" "$RUN/anon.code"
curl -s "$BASE/export/shopify.csv" -o /dev/null -w '%{http_code}' > "$RUN/anonexp.code"
check "anonymous cannot download the catalogue" "303" "$RUN/anonexp.code"

echo
echo "SMOKE_FAILURES=$fail"
exit $fail
