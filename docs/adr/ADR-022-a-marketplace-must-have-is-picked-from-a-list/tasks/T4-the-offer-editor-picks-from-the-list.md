# Task ADR-022-T4: Pick each must-have from a dropdown on the offer editor

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the served-path change — `offerMarketplaceCard` renders one `<select>` per must-have
**Consumes:** `App.marketplaceOptions(ctx, profile)` (T3), `OfferStore.SetMarketplaceValue()` (T2), `core.Offer.MarketplaceValue()` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the empty option`, `the fallback to the configured default`, `the free-text escape when the list is empty`, `the per-offer resolution in the exporter`, `the signal binding on the select`

## Goal

Replace the free-text eBay category box with a dropdown per must-have field, defaulting to "use the
configured default" and falling back to it when nothing is chosen.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/view/offer_detail.templ` | edit | `OfferMarketplaceCard` renders the selects — **exported during execution**, because the SSE loop is in another package and S5's patch cannot reach an unexported templ |
| `internal/web/view/model.go` | edit | **Not foreseen.** `OfferDetail` needed a `Marketplace []MarketplaceChoice` read model; the card cannot compute the option list, the current answer and the fallback from an offer alone |
| `internal/web/handlers_offer.go` | edit | Accepts all three signals, not just the category |
| `internal/web/handlers_settings.go` | edit | **Not foreseen.** `ebayDefaults` was factored out so the settings panel and the editor's "Use the default — …" label cannot compute that precedence differently |
| `internal/export/ebay.go` | edit | **Not foreseen, and the task was wrong without it — see [S8].** The exporter read the offer's own value for the CATEGORY only |
| `internal/export/ebay_test.go` | edit | Real Go tests for [S8], which `internal/web` could never have |
| `internal/web/routes.go` | edit | `POST /offers/{id}/category` → `/marketplace` — **the line that selects the widened handler** |
| `internal/web/handlers_stream.go` | edit | The offer event patches the card, so a saved value shows without a reload |
| `internal/web/view/offer_fields_contract_test.go` | edit | **Not foreseen.** Its fixture built an `OfferDetail` with no `Marketplace`, which rendered a card with no controls — the fixture, not the assertion, was what needed fixing |
| `scripts/smoke.sh` | edit | The proof, since `internal/web` has no Go tests |
| `scripts/browser/marketplace.js` | edit | One walk that actually opens the dropdown and picks. ⚠ **Not `taxonomy.js` as planned**: T3 created this walk, it already has the option lists filled, and a feature's browser proof reads better in one file than split across two |

## Ordered Steps

1. [S1] Add the smoke assertions and confirm RED against the current binary — `POST /offers/{id}/marketplace` does not exist. [proof: acceptance]
2. [S2] Render one `<select>` per field from the read model, with the offer's current value selected. ⚠ The FIRST option is an explicit empty one whose label names what it falls back to ("Use the default — 11450"), because a `<select>` with no empty option makes "unset" indistinguishable from "the first entry" and would silently assign every offer the first category in the list.
3. [S3] When a field's option list is EMPTY, render the free-text input instead. ⚠ Without this the screen is strictly worse than today until an administrator has filled the list, and a warehouse mid-migration would be unable to set a category at all.
4. [S4] Widen the handler to read all three signals and call `SetMarketplaceValue` per field, skipping unchanged ones. Empty means unset, which T2 stores as a deleted row.
5. [S5] Patch the card on the offer event so a save is visible. ⚠ An SSE patch REPLACES an element already in the document — the card must be rendered unconditionally with a stable id, which is the defect shape that made the fields card unreachable on 2026-09-07.
6. [S6] Assert in smoke: picking a category stores it; leaving it empty exports under the configured default; an empty list still offers the text box. [proof: acceptance]
7. [S7] Extend the taxonomy browser walk to select an option and read the value back after a reload. ⚠ A `<select>` that renders correctly and is not wired to a signal looks identical in HTML; only a browser catches it. [proof: acceptance]
8. [S8] **Added during execution, and the task was WRONG without it.** Widen `internal/export/ebay.go` so all three must-haves resolve per offer. It read `MarketplaceValue` for the CATEGORY only; condition and location came from the profile options for every row. Shipping dropdowns on top of that would have stored a condition an operator picked, shown it back to them on the offer for ever, and exported something else — worse than having no control at all. Condition is also the one M asked for by name ("for all of these fields like 'used' 'new'"), so the gap was directly under the request. ⚠ Location keeps its up-front requirement rather than following the category's refuse-per-offer shape: every deployment has a dispatch address, and the offer's value is an override for the item that ships from elsewhere.

## Acceptance

```bash
set -o pipefail
go test ./internal/export/ -count=1 -v -run '^TestEBayPrefersTheOffersOwn' 2>&1 | tee /tmp/adr022-t4.out \
  && grep -q -- "--- PASS: TestEBayPrefersTheOffersOwnCategory" /tmp/adr022-t4.out \
  && grep -q -- "--- PASS: TestEBayPrefersTheOffersOwnCondition" /tmp/adr022-t4.out \
  && grep -q -- "--- PASS: TestEBayPrefersTheOffersOwnLocation" /tmp/adr022-t4.out \
  && bash scripts/smoke.sh 2>&1 | tee -a /tmp/adr022-t4.out \
  && grep -q "SMOKE_FAILURES=0" /tmp/adr022-t4.out \
  && grep -q "an offer takes its eBay category from the list" /tmp/adr022-t4.out \
  && grep -q "an offer that chooses nothing exports under the default" /tmp/adr022-t4.out \
  && grep -q "an empty option list still offers a text box" /tmp/adr022-t4.out \
  && grep -q "the saved category is visible without a reload" /tmp/adr022-t4.out \
  && grep -q "and names what choosing nothing falls back to" /tmp/adr022-t4.out \
  && grep -q "the picked value comes back selected" /tmp/adr022-t4.out \
  && grep -q "and the cleared value is really gone" /tmp/adr022-t4.out \
  && bash scripts/browser.sh marketplace 2>&1 | tee -a /tmp/adr022-t4.out \
  && grep -q "BROWSER_WALK_FAILURES=0" /tmp/adr022-t4.out \
  && grep -q "picking a category in a browser stores it" /tmp/adr022-t4.out \
  && grep -q "the must-have is a dropdown now, not a box" /tmp/adr022-t4.out \
  && grep -q "and choosing nothing again clears it" /tmp/adr022-t4.out
```

⚠ THE GO SEGMENT NAMES EACH `--- PASS:` LINE RATHER THAN TRUSTING THE EXIT CODE. `go test -run`
with a filter that selects nothing prints `ok` and exits 0, so an exit code alone cannot tell a
passing gate from a gate that ran no tests — which is the failure `adr-verify` was built to catch and
would be re-introduced here by a shorter fence.

Named assertions rather than the count alone, for the reason given in T3: `SMOKE_FAILURES=0` is also
what a walk prints when nothing was added.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `an offer takes its eBay category from the list` | `scripts/smoke.sh` | A chosen option is stored and reaches the eBay CSV | — | S2, S4, S6 |
| `an offer that chooses nothing exports under the default` | `scripts/smoke.sh` | Empty still falls back — ADR-004's rule survives the dropdown | — | S2, S4 |
| `and the cleared value is really gone` | `scripts/smoke.sh` | Clearing writes the deletion rather than leaving the old value behind | — | S4, S6 |
| `an empty option list still offers a text box` | `scripts/smoke.sh` | The escape in S3 | — | S3 |
| `and names what choosing nothing falls back to` | `scripts/smoke.sh` | The empty option names the value, not just "the default" | — | S2 |
| `the picked value comes back selected` | `scripts/smoke.sh` | The stored answer is re-rendered as the selected option | — | S2, S4 |
| `the saved category is visible without a reload` | `scripts/smoke.sh` | The patch target exists in the document | — | S5 |
| `TestEBayPrefersTheOffersOwnCategory` | `internal/export/ebay_test.go` | The category resolves per offer (ADR-016, unchanged) | — | S8 |
| `TestEBayPrefersTheOffersOwnCondition` | `internal/export/ebay_test.go` | Condition resolves per offer, so a new part and a used one can share one export | — | S8 |
| `TestEBayPrefersTheOffersOwnLocation` | `internal/export/ebay_test.go` | Location resolves per offer, for a warehouse with two sites | — | S8 |
| `picking a category in a browser stores it` | `scripts/browser/marketplace.js` | The select is actually bound to a signal | — | S7 |
| `the must-have is a dropdown now, not a box` | `scripts/browser/marketplace.js` | A filled list turns the control into a select in the rendered page | — | S2, S7 |
| `and choosing nothing again clears it` | `scripts/browser/marketplace.js` | The empty option is reachable in a browser, not merely present in the HTML | — | S2, S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The card renders the selects |
| 2 — something selects it | `internal/web/routes.go`; deleting the route line makes the smoke save assertion fail |
| 3 — the caller can discover it | The dropdown IS the discovery mechanism — the operator sees the available values without knowing eBay's taxonomy |
| 4 — it is used | The browser walk picks one, which is the first end-to-end observation of the served path |

## Mutation Log

⚠ **TWO `survived` VERDICTS BELOW ARE BAD MUTANTS, NOT DECORATIVE TESTS, and the difference matters
enough to write down.** A `survived` line read on its own says the assertion proves nothing; neither
of these does.

1. **`internal/web/view/offer_detail.templ` · the empty option.** Plain Go written inside a `.templ`
   is copied verbatim into the generated `_templ.go`, and the binary is built from THAT — so an edit
   to the `.templ` reaches nothing until `templ generate` runs, which `adr-verify` does not do. The
   mutant was never compiled. Fixed by moving `mktAttr`, `defaultOptionLabel` and `IsList` into
   `model.go`, where the compiler reads them directly; the re-run killed. **Generalises: view logic
   worth proving does not belong inside a `.templ`.**
2. **`internal/web/handlers_offer.go` · the signal binding.** Lowercasing the struct tag to
   `json:"offerebaycategory"` changed nothing, because **Go's `encoding/json` matches field tags
   CASE-INSENSITIVELY** when no exact match exists (verified in a scratch test, 2026-09-09). That
   mutant INVERTS the real hazard rather than reproducing it: the actual failure is a `data-bind`
   attribute written camelCase, where the page still seeds the correctly-spelled signal, so the
   handler gets an EXACT match on an empty value — and an exact match beats the case-insensitive
   fallback. The mutant deleted the exact match instead of adding a competing one. Replaced by
   mutating `Attr`, which is what actually names the signal the control binds; that killed.

- 2026-09-09 · 5d6a4a8* · mutant killed · exit 1 · `internal/export/ebay.go` · the exporter goes back to one condition for the whole file, so a value an operator picked on the offer is shown back to them for ever and never leaves the building · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · covers:the per-offer resolution in the exporter
- 2026-09-09 · 5d6a4a8* · mutant killed · exit 1 · `internal/export/ebay.go` · the configured default stops being the fallback, so every offer nobody has touched exports with an empty must-have — which is what the dropdown deliberately-empty first option relies on · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · covers:the fallback to the configured default
- 2026-09-09 · 5d6a4a8* · mutant survived · exit 0 · `internal/web/view/offer_detail.templ` · the empty option stops naming the value it falls back to, so an operator has to open Settings to find out what they just agreed to · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · covers:the empty option
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-09 · 5d6a4a8* · mutant killed · exit 1 · `internal/web/view/model.go` · the empty option stops naming the value it falls back to, so an operator has to open Settings to find out what choosing nothing just agreed to · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · covers:the empty option
- 2026-09-09 · 5d6a4a8* · mutant killed · exit 1 · `internal/web/view/model.go` · a field with no values to pick between renders a dropdown holding only its empty option, so a warehouse whose administrator has not filled the lists yet cannot set that must-have at all · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · covers:the free-text escape when the list is empty
- 2026-09-09 · 5d6a4a8* · mutant survived · exit 0 · `internal/web/handlers_offer.go` · the select sends offerEbayCategory and the handler reads a lowercased name, which is exactly what writing the data-bind attribute camelCase would do — the page renders, the button posts, and nothing is ever stored · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · covers:the signal binding on the select
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-09 · 5d6a4a8* · mutant killed · exit 1 · `internal/web/handlers_offer.go` · the select binds a signal the page never declared and the handler never reads, so picking an option in a browser changes nothing — the failure a camelCase data-bind attribute would really produce · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · covers:the signal binding on the select

## Invariants

- Empty stays legal and always falls back to the configured default (ADR-004, ADR-016).
- The owner price is not on this card and never becomes a marketplace value.
- No `<form>`; `data-bind:` attributes stay lowercase-safe.
- `*_templ.go` regenerated, never hand-edited.

## Risks

- A `<select>` bound to a datastar signal is a pattern this codebase has not used yet; if binding proves awkward the fallback is the existing text input plus a `<datalist>`, which is strictly worse and would mean revisiting the ADR.
- Three saves where there was one: the handler must skip unchanged fields or every card save writes three rows.

## Stop Condition

Stop if the export cannot tell "this offer chose nothing" from "this offer chose the value that
happens to equal the default" — that distinction is what S2's empty option exists to preserve, and
losing it would make the fallback unobservable.

## Out of Scope

- Shopify's and Allegro's must-haves; only eBay's resolution is wired (deferred: docs/adr/BACKLOG.md)
- Per-category required item specifics (deferred: docs/adr/BACKLOG.md)

## Verification Log
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18632
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18681
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18401
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18432
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18722
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18481
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18452
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18576
- 2026-09-09 · 5d6a4a8* · exit 0 · `set -o pipefail …` · acceptance-sha256:3a8665682c67b8c65f824cc04291aed876149bfad56c9178d5bfc5513e45eb38 · ms:18456
