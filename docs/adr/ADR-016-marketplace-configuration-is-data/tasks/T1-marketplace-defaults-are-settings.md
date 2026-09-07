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
| `internal/web/app.go` | edit | `ebayMarketplace(ctx)` — the per-request resolution, mirroring the existing `publicBase(ctx)` |
| `README.md` | edit | Document all three variables as start-up defaults; they appear in no table today |

## Ordered Steps

1. [S1] Write `TestSettingsRoundTripTheMarketplaceDefaults` and confirm it is RED — `core.Settings` has no such fields yet.
2. [S2] Add the keys, fields and validation to `internal/core/settings.go`.
3. [S3] Read and write them in `internal/settings/repo.go`, inside the existing transaction.
4. [S4] Add the Marketplaces card and wire the handler that saves it. [proof: human: an administrator saves a category on the Settings page, reloads, and sees it still there — no Go test in this repository exercises a templ template or a web handler, so nothing else can distinguish a card that saves from one that only renders]
5. [S5] Resolve stored-over-environment PER REQUEST in `internal/web/app.go`, mirroring `publicBase(ctx)`, and document the three variables in `README.md` as start-up defaults. ⚠ AMENDED DURING EXECUTION: this step originally said to resolve in `cmd/warehouse/main.go`. That is impossible — main builds `export.Options` ONCE at start-up and cannot see a settings row written afterwards, which is the whole failure ADR-016 exists to fix. The environment still reaches `Cfg.Export` there, unchanged; only the resolution moved.

## Acceptance

```bash
set -o pipefail
go test ./internal/settings/ -run '^TestSettingsRoundTripTheMarketplaceDefaults$' -count=1 2>&1 | tee /tmp/adr016-t1-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr016-t1-new.out && \
go test ./internal/settings/ ./internal/core/ -run '^(TestSettingsRoundTripTheMarketplaceDefaults|TestSavingOneMarketplaceFieldLeavesTheOthers|TestTheReadHandleRefusesToWrite|TestMarketplaceResolveTakesTheDefaultOnlyForEmptyFields|TestValidatePublicBaseURLRefusesWhatCannotWork)$' -count=1 2>&1 | tee /tmp/adr016-t1-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr016-t1-reg.out
```

⚠ AMENDED DURING EXECUTION: the round-trip test was planned for
`internal/core/settings_test.go` and belongs in `internal/settings`, because a
round trip needs a database and `core` has none. `core` keeps the pure half —
the precedence rule — which is the part worth testing without one.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestSettingsRoundTripTheMarketplaceDefaults` | `internal/settings/repo_test.go` | The three fields survive a save and a read, and a fresh installation with no rows comes back empty rather than erroring | — | S1, S3 |
| `TestMarketplaceResolveTakesTheDefaultOnlyForEmptyFields` | `internal/core/settings_test.go` | Precedence is per FIELD, so setting one value does not blank its siblings | — | S2, S5 |
| `TestSavingOneMarketplaceFieldLeavesTheOthers` | `internal/settings/repo_test.go` | The read-modify-write an editing screen performs does not blank the other keys | — | S3 |
| `TestTheReadHandleRefusesToWrite` | `internal/settings/repo_test.go` | ADR-001's read-only port holds for this package too | — | — |
| `TestValidatePublicBaseURLRefusesWhatCannotWork` | `internal/core/settings_test.go` | The setting that was already there is undisturbed by the three new keys | — | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestSettingsRoundTripTheMarketplaceDefaults` |
| 2 — something selects it | The handler that saves the card, listed in Affected Files. Deleting it leaves the card rendering and saving nothing — the mutation for this task targets it |
| 3 — the caller can discover it | The Settings page card, and the three variables documented in `README.md` |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

- 2026-09-07 · e26f862* · mutant killed · exit 1 · `internal/core/settings.go` · a field that never falls through to the default means an administrator who sets only the category silently blanks the condition and location the environment supplied · acceptance-sha256:8518d9e730a537dc8c6aec82cfccda25266fd51dcd1d1b7a99a32b216c80d6c6 · covers:the stored value winning over the environment default

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

- 2026-09-07 · e26f862* · exit 0 · `set -o pipefail …` · acceptance-sha256:8518d9e730a537dc8c6aec82cfccda25266fd51dcd1d1b7a99a32b216c80d6c6 · ms:2029
- 2026-09-07 · e26f862* · exit 0 · `set -o pipefail …` · acceptance-sha256:8518d9e730a537dc8c6aec82cfccda25266fd51dcd1d1b7a99a32b216c80d6c6 · ms:1908
