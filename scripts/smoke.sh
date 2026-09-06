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

echo "== intake: title only, no price =="
curl -fsS -b "$COOKIES" -X POST "$BASE/offers" \
  -H 'Content-Type: application/json' \
  -d '{"newTitle":"Vintage brass desk lamp","newSku":"","newLocation":""}' \
  -o "$RUN/offer.txt"
check "offer created from a title alone" "/offers/" "$RUN/offer.txt"
OFFER_ID=$(grep -o "/offers/[0-9a-f-]\{36\}" "$RUN/offer.txt" | head -1 | cut -d/ -f3)
echo "  offer id: $OFFER_ID"

curl -fsS -b "$COOKIES" "$BASE/offers/$OFFER_ID" -o "$RUN/offer.html"
check "offer page renders" "Vintage brass desk lamp" "$RUN/offer.html"
check "unpriced draft says so" "pricing queue" "$RUN/offer.html"
check "the editor uses the SAME one stream endpoint" "/stream?offer=$OFFER_ID" "$RUN/offer.html"

curl -fsS -b "$COOKIES" "$BASE/offers?needs_pricing=1" -o "$RUN/pricing.html"
check "unpriced draft is in the pricing queue" "Vintage brass desk lamp" "$RUN/pricing.html"

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

echo "== batches =="
curl -fsS -b "$COOKIES" -X POST "$BASE/carts" \
  -H 'Content-Type: application/json' \
  -d '{"cartName":"eBay September","cartNote":"first batch"}' -o "$RUN/cart.txt"
check "batch created" "eBay September" "$RUN/cart.txt"
CART_ID=$(curl -fsS -b "$COOKIES" "$BASE/carts" | grep -o '/carts/[0-9a-f-]\{36\}' | head -1 | cut -d/ -f3)
curl -fsS -b "$COOKIES" -X POST "$BASE/carts/$CART_ID/items/$OFFER_ID" \
  -H 'Content-Type: application/json' -d '{}' -o "$RUN/cartadd.txt"
check "offer added to the batch" "eBay September" "$RUN/cartadd.txt"

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

echo "== auth boundary =="
curl -s "$BASE/offers" -o /dev/null -w '%{http_code}' > "$RUN/anon.code"
check "anonymous is redirected away from offers" "303" "$RUN/anon.code"
curl -s "$BASE/export/shopify.csv" -o /dev/null -w '%{http_code}' > "$RUN/anonexp.code"
check "anonymous cannot download the catalogue" "303" "$RUN/anonexp.code"

echo
echo "SMOKE_FAILURES=$fail"
exit $fail
