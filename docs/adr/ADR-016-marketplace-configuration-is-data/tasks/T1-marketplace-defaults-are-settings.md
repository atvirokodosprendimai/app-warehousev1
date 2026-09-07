# Task ADR-016-T1: Marketplace defaults are settings, editable without a restart

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** `core.Settings.EbayCategory` / `EbayConditionID` / `EbayLocation`, stored under the `ebay.*` keys
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the stored value winning over the environment default`, `every settings key moving in one transaction`

## Goal

An administrator sets the eBay category, condition and item location on the
Settings page; the values take effect without a restart, survive one, and the
environment variables become the first-run default rather than the only source.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/settings.go` | edit | The three new keys, the `Settings` fields, and their validation |
| `internal/settings/repo.go` | edit | Read and write the new keys in the existing single transaction |
| `internal/web/view/settings.templ` | edit | A "Marketplaces" card beside the existing "Public address" one |
| `internal/web/handlers_misc.go` | edit | Accept and save the new fields; this is what SELECTS them — without it the card is decoration |
| `cmd/warehouse/main.go` | edit | Stored values override the environment when building `export.Options` |
| `README.md` | edit | Document all three variables as start-up defaults; they appear in no table today |

## Ordered Steps

1. [S1] Write `TestSettingsRoundTripTheMarketplaceDefaults` and confirm it is RED — `core.Settings` has no such fields yet.
2. [S2] Add the keys, fields and validation to `internal/core/settings.go`.
3. [S3] Read and write them in `internal/settings/repo.go`, inside the existing transaction.
4. [S4] Add the Marketplaces card and wire the handler that saves it. [proof: human: an administrator saves a category on the Settings page, reloads, and sees it still there — no Go test in this repository exercises a templ template or a web handler, so nothing else can distinguish a card that saves from one that only renders]
5. [S5] Make the stored value win over the environment in `cmd/warehouse/main.go`, and document the three variables in `README.md`.

## Acceptance

```bash
set -o pipefail
go test ./internal/core/ -run '^TestSettingsRoundTripTheMarketplaceDefaults$' -count=1 2>&1 | tee /tmp/adr016-t1-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr016-t1-new.out && \
go test ./internal/core/ ./internal/settings/ -run '^(TestSettingsRoundTripTheMarketplaceDefaults|TestValidatePublicBaseURLNormalises|TestValidatePublicBaseURLRefusesWhatCannotWork|TestReachablePubliclyIsAWarningNotAValidation)$' -count=1 2>&1 | tee /tmp/adr016-t1-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr016-t1-reg.out
```

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestSettingsRoundTripTheMarketplaceDefaults` | `internal/core/settings_test.go` | The three fields survive a save and a read, and an empty stored value falls through to the environment default | — | S1, S2, S3, S5 |
| `TestValidatePublicBaseURLNormalises` | `internal/core/settings_test.go` | The existing public-origin setting is unaffected by the new keys | — | — |
| `TestValidatePublicBaseURLRefusesWhatCannotWork` | `internal/core/settings_test.go` | As above | — | — |
| `TestReachablePubliclyIsAWarningNotAValidation` | `internal/core/settings_test.go` | As above | — | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestSettingsRoundTripTheMarketplaceDefaults` |
| 2 — something selects it | The handler that saves the card, listed in Affected Files. Deleting it leaves the card rendering and saving nothing — the mutation for this task targets it |
| 3 — the caller can discover it | The Settings page card, and the three variables documented in `README.md` |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

<Tool-written by `adr-verify … --mutant …`. Empty at authoring.>

## Invariants

- A stored value always wins over the environment, exactly as `PUBLIC_BASE_URL` already does.
- Every settings key still moves in ONE transaction; a partial save is not reachable.
- An unknown key in the table is still ignored rather than refused, so a downgrade starts.

## Risks

- Four sources for one value (offer, settings, environment, code default). Mitigated by the refusal message in T3 naming the order, not by anything mechanical here.

## Stop Condition

Stop and ask if the Settings page cannot express these without a `<form>` — that
would collide with ADR-008, and the answer would be a change to this task rather
than to that rule.

## Out of Scope

- Allegro's and Shopify's own settings (deferred: docs/adr/BACKLOG.md)
- Validating a category against eBay's live taxonomy — ADR-016 rejects it with a reason.

## Verification Log

<Tool-written by `adr-verify <this-file>` — do not hand-write entries.>
