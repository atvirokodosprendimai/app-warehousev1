# Task ADR-021-T4: A field can be exported, and the CSV's columns are the union over the batch

**Depends-on:** T1, T3
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** the `export` flag on a field and its control on the taxonomy screen; a variable tail of columns on both CSV profiles, computed from the offers actually being written
**Consumes:** `core.Offer.Fields` (T1), the values T3 stores, and `internal/export`'s existing pure `Exporter.Write`
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a field is not exported unless it says so`, `the column set is the union over the batch`, `the column order is stable for a given batch`, `an offer without a field gets an empty cell`, `an eBay item specific is a C-prefixed column`, `the owner price still never leaves the building`

## Goal

M's request, in the words it was made in: *"in export i need to choose which
taxonomies from category to export to csv somehow"*. An operator ticks the
fields that should reach a marketplace, and they arrive as columns.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `migrations/00009_categories.sql` | edit | `category_fields.export` — ⚠ only if T1 has not yet shipped; once the migration is applied anywhere it gets its own numbered file instead |
| `internal/core/taxonomy.go` | edit | `CategoryField.Export bool`, defaulting false |
| `internal/export/export.go` | edit | the shared union-and-order helper both profiles use |
| `internal/export/ebay.go` | edit | the exported fields become `C:<Label>` columns |
| `internal/export/shopify.go` | edit | the exported fields become plain columns |
| `internal/export/export_test.go` | edit | the union, order and empty-cell assertions below |
| `internal/web/view/category.templ` | edit | the tick, beside the field it belongs to |
| `internal/web/handlers_misc.go` | edit | `GetExportFile` loads `Fields` for the offers in the batch before handing them to the exporter |
| `scripts/smoke.sh` | edit | asserts an exported value in the CSV and a non-exported one absent |

<`internal/export` is PURE — it is handed offers and an `io.Writer` and touches
no database. The values therefore have to ride on `core.Offer.Fields`, which is
the seam ADR-016 opened for `Offer.Categories`. The handler loads; the exporter
renders.>

## Ordered Steps

1. [S1] Write `TestOnlyExportedFieldsBecomeColumns` against the current exporter and confirm it is RED.
2. [S2] Add `Export bool` to the field, defaulting FALSE. ⚠ Off by default is the decision, not a convenience: a private note about where a part came from is not something to publish, and a default of true would publish every field an operator ever created without them choosing to. [proof: acceptance]
3. [S3] Add the tick to the taxonomy screen beside each field. [proof: acceptance]
4. [S4] Write the shared helper: collect the exported fields present on the offers in THIS batch, deduplicate by field id, and order by category path then `position`. ⚠ **A CSV has ONE header row and the offers in a batch do not share a category** — this is the central design point of the parent's Decision 6, and computing the header from anything other than the batch produces either a header per row or a per-category export that cannot mix. [proof: acceptance]
5. [S5] Emit the tail on eBay as `C:<Label>` — eBay's item-specific convention — and on Shopify as a plain column named for the label. [proof: acceptance]
6. [S6] Write an empty cell where an offer has no such field, so a turbocharger and a graphics card share one header and each answers only its own questions. [proof: acceptance]
7. [S7] Load `Fields` in the export handler for the offers in the batch. ⚠ T1's S7 loads them on the whole-offer read only, so an export that lists offers must ask for them explicitly or it will render a correct-looking file with every custom column empty. [proof: acceptance]
8. [S8] Extend the smoke walk: assert an exported field's value appears in both CSVs, and that a field left unticked does not. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/export/ -run '^TestOnlyExportedFieldsBecomeColumns$' -count=1 2>&1 | tee /tmp/adr021t4-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t4-new.out && \
go test ./internal/export/ -run '^(TestOnlyExportedFieldsBecomeColumns|TestTheHeaderIsTheUnionOverTheBatch|TestColumnOrderIsStableForAGivenBatch|TestAnOfferWithoutTheFieldGetsAnEmptyCell|TestAnExportedFieldIsAnEBayItemSpecific|TestOwnerPriceNeverAppears)$' -count=1 2>&1 | tee /tmp/adr021t4-named.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t4-named.out && \
go test ./... -count=1 2>&1 | tee /tmp/adr021t4-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t4-reg.out && \
bash scripts/smoke.sh 2>&1 | tee /tmp/adr021t4-smoke.out && \
grep -q "SMOKE_FAILURES=0" /tmp/adr021t4-smoke.out
```

The pre-existing owner-price test is inside the fence because this task adds
columns to both profiles, and a new column set is exactly the change that could
carry a private number into a public file without anybody looking.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestOnlyExportedFieldsBecomeColumns` | `internal/export/export_test.go` | A field with `Export` false produces no column and no cell, in either profile | — | S1, S2 |
| `TestTheHeaderIsTheUnionOverTheBatch` | `internal/export/export_test.go` | Two offers under different categories produce one header carrying both their exported fields | — | S4 |
| `TestColumnOrderIsStableForAGivenBatch` | `internal/export/export_test.go` | The same batch in a different input order produces a byte-identical header | — | S4 |
| `TestAnOfferWithoutTheFieldGetsAnEmptyCell` | `internal/export/export_test.go` | Every row has the same cell count as the header | — | S6 |
| `TestAnExportedFieldIsAnEBayItemSpecific` | `internal/export/ebay_test.go` | The eBay column is `C:<Label>` and the Shopify one is the bare label | — | S5 |
| `TestOwnerPriceNeverAppears` | `internal/export/export_test.go` | The pre-existing guard (ADR-003) still holds with the new columns present | — | S5, S6 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestOnlyExportedFieldsBecomeColumns` renders a CSV and reads its header |
| 2 — something selects it | The union helper picks the fields out of the batch it was handed |
| 3 — the caller can discover it | The tick is on the taxonomy screen beside the field it governs, so the operator decides where they define the question rather than on a separate export screen |
| 4 — it is used | The smoke walk exports both profiles and finds the value in the file — ⚠ the ONLY segment that proves the HANDLER loaded the values, because the exporter is pure and a unit test hands it whatever it likes |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.

## Invariants

- `Export` defaults to false.
- The owner price never appears in any exported file, in any column (ADR-003).
- Every row carries exactly as many cells as the header.
- The exporter stays pure: no database, no network, no clock.

## Risks

- **An export's header depends on which offers are in it.** Two runs a minute apart differ if the batch differs. The parent ADR states this rather than designing it away, and `TestColumnOrderIsStableForAGivenBatch` pins the half that IS guaranteed — stability for a given batch, not across batches.
- **The LABEL reaches the marketplace, so renaming a field renames a published column.** Nothing here breaks, because the internal `code` never moves; a listing tool on the far side might. Named in the parent's Risks.
- ⚠ **The exporter cannot notice a missing load.** If S7 is forgotten the file is well-formed with every custom column empty, and every unit test still passes because a unit test supplies its own `Offer.Fields`. That is why the smoke assertion in S8 is the reachability proof and not a formality.
- **A field label containing a comma or a quote** goes through `encoding/csv`, which quotes it. No new escaping is written.

## Stop Condition

Stop and ask if the union turns out to exceed what a marketplace accepts as a
column count. Neither eBay nor Shopify documents a hard limit this repository has
verified, so a cap invented here would be a number with no reason behind it —
and the answer might be a per-category export, which is a different decision.

## Out of Scope

- Whether a SHARED marketplace field (brand, GTIN/EAN, MPN, weight) becomes a real column on `offers` — ADR-021 defers it to `BACKLOG.md`.
- Allegro and Shopify category resolution — already deferred by ADR-016 in `BACKLOG.md`.
- Choosing the export batch itself: this task changes what a batch's CSV contains, never which offers are in it.

## Verification Log

To be completed by `adr-verify` during execution.
