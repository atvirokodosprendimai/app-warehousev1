# Task ADR-022-T4: Pick each must-have from a dropdown on the offer editor

**Depends-on:** T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** the served-path change — `offerMarketplaceCard` renders one `<select>` per must-have
**Consumes:** `App.marketplaceOptions(ctx, profile)` (T3), `OfferStore.SetMarketplaceValue()` (T2), `core.Offer.MarketplaceValue()` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the empty option`, `the fallback to the configured default`, `the free-text escape when the list is empty`

## Goal

Replace the free-text eBay category box with a dropdown per must-have field, defaulting to "use the
configured default" and falling back to it when nothing is chosen.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/view/offer_detail.templ` | edit | `offerMarketplaceCard` renders the selects |
| `internal/web/handlers_offer.go` | edit | Accepts all three signals, not just the category |
| `internal/web/routes.go` | edit | `POST /offers/{id}/category` → `/marketplace` — **the line that selects the widened handler** |
| `internal/web/handlers_stream.go` | edit | The offer event patches the card, so a saved value shows without a reload |
| `scripts/smoke.sh` | edit | The proof, since `internal/web` has no Go tests |
| `scripts/browser/taxonomy.js` | edit | One walk that actually opens the dropdown and picks |

## Ordered Steps

1. [S1] Add the smoke assertions and confirm RED against the current binary — `POST /offers/{id}/marketplace` does not exist. [proof: acceptance]
2. [S2] Render one `<select>` per field from the read model, with the offer's current value selected. ⚠ The FIRST option is an explicit empty one whose label names what it falls back to ("Use the default — 11450"), because a `<select>` with no empty option makes "unset" indistinguishable from "the first entry" and would silently assign every offer the first category in the list.
3. [S3] When a field's option list is EMPTY, render the free-text input instead. ⚠ Without this the screen is strictly worse than today until an administrator has filled the list, and a warehouse mid-migration would be unable to set a category at all.
4. [S4] Widen the handler to read all three signals and call `SetMarketplaceValue` per field, skipping unchanged ones. Empty means unset, which T2 stores as a deleted row.
5. [S5] Patch the card on the offer event so a save is visible. ⚠ An SSE patch REPLACES an element already in the document — the card must be rendered unconditionally with a stable id, which is the defect shape that made the fields card unreachable on 2026-09-07.
6. [S6] Assert in smoke: picking a category stores it; leaving it empty exports under the configured default; an empty list still offers the text box. [proof: acceptance]
7. [S7] Extend the taxonomy browser walk to select an option and read the value back after a reload. ⚠ A `<select>` that renders correctly and is not wired to a signal looks identical in HTML; only a browser catches it. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
bash scripts/smoke.sh 2>&1 | tee /tmp/adr022-t4.out \
  && grep -q "SMOKE_FAILURES=0" /tmp/adr022-t4.out \
  && grep -q "an offer takes its eBay category from the list" /tmp/adr022-t4.out \
  && grep -q "an offer that chooses nothing exports under the default" /tmp/adr022-t4.out \
  && grep -q "an empty option list still offers a text box" /tmp/adr022-t4.out \
  && bash scripts/browser.sh 2>&1 | tee -a /tmp/adr022-t4.out \
  && grep -q "BROWSER_WALK_FAILURES=0" /tmp/adr022-t4.out
```

Named assertions rather than the count alone, for the reason given in T3: `SMOKE_FAILURES=0` is also
what a walk prints when nothing was added.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `an offer takes its eBay category from the list` | `scripts/smoke.sh` | A chosen option is stored and reaches the eBay CSV | — | S2, S4, S6 |
| `an offer that chooses nothing exports under the default` | `scripts/smoke.sh` | Empty still falls back — ADR-004's rule survives the dropdown | — | S2, S4 |
| `an empty option list still offers a text box` | `scripts/smoke.sh` | The escape in S3 | — | S3 |
| `the saved category is visible without a reload` | `scripts/smoke.sh` | The patch target exists in the document | — | S5 |
| `picking a category in a browser stores it` | `scripts/browser/taxonomy.js` | The select is actually bound to a signal | — | S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The card renders the selects |
| 2 — something selects it | `internal/web/routes.go`; deleting the route line makes the smoke save assertion fail |
| 3 — the caller can discover it | The dropdown IS the discovery mechanism — the operator sees the available values without knowing eBay's taxonomy |
| 4 — it is used | The browser walk picks one, which is the first end-to-end observation of the served path |

## Mutation Log

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
