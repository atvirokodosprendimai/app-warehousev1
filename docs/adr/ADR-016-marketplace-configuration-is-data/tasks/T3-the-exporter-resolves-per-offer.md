# Task ADR-016-T3: The exporter resolves per offer and refuses by naming where to set it

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** the per-offer category resolution and its refusal message
**Consumes:** `core.Settings` marketplace fields (T1), `core.Offer.Categories` (T2)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the offer's own category winning over the default`, `the refusal naming the SKU and both places a value can be set`

## Goal

Each row is written with that offer's own category when it has one and the
configured default when it does not; and an offer with neither produces a refusal
that names the SKU and both places a person can fix it.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/export/export.go` | edit | `Options.Category` documented as the DEFAULT, not the value |
| `internal/export/ebay.go` | edit | Resolve per offer; move the emptiness check from options-time to row-time |
| `internal/web/handlers_misc.go` | edit | Build `Options` from settings rather than only from `Cfg`; this is what SELECTS T1's stored values |

## Ordered Steps

1. [S1] Write `TestEBayPrefersTheOffersOwnCategory` and confirm it is RED — the exporter reads only `opt.Category` today.
2. [S2] Resolve per offer in `internal/export/ebay.go`, falling back to `Options.Category`.
3. [S3] Replace the options-time emptiness refusal with a row-time one naming the SKU and both places.
4. [S4] Build `Options` from the stored settings in the export handler, so T1's values reach the exporter. [proof: human: the export screen offers eBay without the "not configured" error once a category is saved, and produces a file whose *Category cell holds it — `internal/web` has no Go tests, so a person driving the page is the check]

## Acceptance

```bash
set -o pipefail
go test ./internal/export/ -run '^TestEBayPrefersTheOffersOwnCategory$' -count=1 2>&1 | tee /tmp/adr016-t3-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr016-t3-new.out && \
go test ./internal/export/ -count=1 2>&1 | tee /tmp/adr016-t3-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr016-t3-reg.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEBayPrefersTheOffersOwnCategory` | `internal/export/ebay_test.go` | An offer naming its own category exports with it, not with the default | — | S1, S2 |
| `TestEBayFallsBackToTheConfiguredDefault` | `internal/export/ebay_test.go` | An offer with no category of its own uses the default rather than refusing | — | S2 |
| `TestEBayRefusalNamesWhereToSetTheCategory` | `internal/export/ebay_test.go` | With neither, the error names the SKU and both places a value can be set | — | S3 |
| `TestEBayNeverExportsTheOwnerPrice` | `internal/export/ebay_test.go` | ADR-003's disclosure boundary survives the change | — | — |
| `TestEBayHeaderMatchesTheFileExchangeSpec` | `internal/export/ebay_test.go` | The column set is unchanged | — | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestEBayPrefersTheOffersOwnCategory` |
| 2 — something selects it | The `*Category` cell in the eBay row builder. The mutation reverses the preference so the default wins, and the test goes red |
| 3 — the caller can discover it | The refusal message itself, which names both places — that IS the discovery mechanism the reported bug lacked |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

<Tool-written by `adr-verify … --mutant …`. Empty at authoring.>

## Invariants

- `internal/export` still reads no settings and no database: it is handed resolved values and stays a pure function, which is what lets ADR-003's byte-level assertions work.
- The owner price still appears in no column of any profile (ADR-003).
- An offer that cannot be rendered still fails the whole export naming the SKUs, rather than being silently dropped.

## Risks

- Moving the refusal from options-time to row-time means a misconfigured export now fails on the first offer rather than before any work. That is the intended trade — the message can name the offer — but it is a behaviour change worth knowing.

## Stop Condition

Stop and ask if resolving per offer would require `internal/export` to read the
database. It must not: that would break the purity ADR-003's tests depend on, and
the answer would be to resolve in the caller instead.

## Out of Scope

- Allegro and Shopify category resolution (deferred: docs/adr/BACKLOG.md)
- Validating the resolved value against eBay's taxonomy — ADR-016 rejects it with a reason.

## Verification Log

<Tool-written by `adr-verify <this-file>` — do not hand-write entries.>
