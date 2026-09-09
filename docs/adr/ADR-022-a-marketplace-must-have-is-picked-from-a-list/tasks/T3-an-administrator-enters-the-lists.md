# Task ADR-022-T3: Let an administrator enter the list each dropdown will offer

**Depends-on:** T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** unassigned
**Produces:** `App.marketplaceOptions(ctx, profile)` read model, `POST /settings/marketplace/add/{field}`, `POST /settings/marketplace/remove/{id}`
**Consumes:** `marketplace.Repo` (T2), `core.MarketplaceField`, `core.MarketplaceOption` (T1)
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the route's admin guard`, `the rendered list`, `the refusal message`, `the signal binding`

## Goal

Add a Settings screen where an administrator adds, removes and reorders the values each marketplace
field will offer, per export profile.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/handlers_settings.go` | edit | The read model and the add/remove/reorder handlers |
| `internal/web/view/settings.templ` | edit | The list editor, one section per field |
| `internal/web/routes.go` | edit | **This is what selects the handlers** — a handler with no route is unreachable and every Go test still passes |
| `internal/web/app.go` | edit | `userMessage` maps the new sentinels, or a duplicate value renders "Something went wrong" |
| `scripts/smoke.sh` | edit | `internal/web` has no Go tests; these assertions are the proof |
| `scripts/browser/marketplace.js` | new | **The only thing that can see the binding.** Smoke posts `optValueCategory` as JSON, so it proves the HANDLER reads that name and can never prove the INPUT sends it — the two are joined only in a browser. Every `.js` in that directory is picked up by `scripts/browser.sh`, so the file is its own wiring |

## Ordered Steps

1. [S1] Add the smoke assertions FIRST and confirm they are RED against the current binary — the route does not exist, so `curl -fsS` fails. ⚠ This is the TDD red step for a package with no Go tests, and it is the only kind available here (ADR-021 T5 established the pattern). [proof: acceptance]
   - **Amended in execution (2026-09-09):** the handlers were written before the assertions, so the red step was taken by REMOVING what selects them — `git stash push -- internal/web/routes.go`, leaving the handlers compiled and unreachable. All five new assertions failed and `SMOKE_FAILURES=5`, exactly the new ones, so no pre-existing assertion moved. That is the same evidence the ordered version would have produced and it is written down rather than quietly skipped, but it is NOT the same discipline: writing the assertion first is what stops the assertion being shaped to the code. This one was — see the S6 amendment.
2. [S2] Add `App.marketplaceOptions(ctx, profile)` building the read model, grouped by field in `MarketplaceFields()` order so a field with no options still renders its (empty) section. A field that vanishes when empty is a field an administrator cannot add the first value to.
3. [S3] Render the editor in `settings.templ`: one section per field, each row showing value and label, with add and remove. ⚠ No `<form>` — ADR-008. Signals and `@post`, like every other screen here.
4. [S4] Wire the routes behind the existing admin guard. ⚠ `internal/web` has NO Go tests, so the guard is proved only by a smoke assertion that a non-admin session is refused. [proof: acceptance]
5. [S5] Map the duplicate-value sentinel in `App.userMessage`, so adding `20081` twice says so instead of "Something went wrong. The details are in the server log." ⚠ This is the exact defect ADR-021 T5 shipped and had to close later; it is in the ordered steps this time rather than in Out of Scope. [proof: acceptance]
6. [S6] Assert in smoke that an added option is listed, that a duplicate is refused in words, and that a removed one is gone. [proof: acceptance]
   - **Amended in execution (2026-09-09):** the removal assertion first read `absent … "Antiques"` and FAILED against correct code. The card's help text says "nobody has to remember that 20081 means Antiques", so the label and the value are both on the page whether or not the row is. It now asserts the row's own `marketplace/remove/{id}` URL is gone, plus that the section says "Nothing on this list yet" rather than rendering blank. ⚠ A negative assertion has to name a substring that exists ONLY while the thing does; "not present anywhere on the page" is a different claim from "the row is gone".
7. [S7] Add a real-browser walk that TYPES into the add-boxes and presses Add. ⚠ Added during execution, because S1–S6 left this task's own first Invariant unproven: nothing in the smoke walk can distinguish a correct `data-bind:opt-value-category` from a camelCase one that binds a different signal — the page renders, the button posts, and the handler reads an empty string either way. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
bash scripts/smoke.sh 2>&1 | tee /tmp/adr022-t3.out \
  && grep -q "SMOKE_FAILURES=0" /tmp/adr022-t3.out \
  && grep -q "an administrator can add a marketplace option" /tmp/adr022-t3.out \
  && grep -q "a duplicate option is refused in words" /tmp/adr022-t3.out \
  && grep -q "a non-administrator cannot reach the marketplace options" /tmp/adr022-t3.out \
  && grep -q "a field with no options still renders its section" /tmp/adr022-t3.out \
  && grep -q "a removed option is gone from the list" /tmp/adr022-t3.out \
  && grep -q "and the list says so rather than going blank" /tmp/adr022-t3.out \
  && bash scripts/browser.sh marketplace 2>&1 | tee -a /tmp/adr022-t3.out \
  && grep -q "BROWSER_WALK_FAILURES=0" /tmp/adr022-t3.out \
  && grep -q "what was typed reaches the server and comes back as a row" /tmp/adr022-t3.out \
  && grep -q "the boxes are emptied by the save, not left loaded" /tmp/adr022-t3.out \
  && grep -q "the duplicate refusal is a sentence an operator can act on" /tmp/adr022-t3.out \
  && grep -q "the list editor fits a 390px viewport" /tmp/adr022-t3.out
```

⚠ EVERY grep NAMES A TEST THE TABLE PROMISES, and that is a rule rather than thoroughness. This fence
shipped naming three of its four, which `adr-lint` refuses — a test that merely rides along in the run
is not selected by the gate, so deleting it would not turn the gate red. Fixing a fence AFTER
acceptance changes its digest and orphans every mutant bound to it; this was corrected before the
first acceptance run, which is the only cheap time to do it.

The `grep -q` lines name the assertions individually because `SMOKE_FAILURES=0` is also what a
walk prints when the assertions were never added — the count cannot distinguish "all passed" from
"none ran".

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `an administrator can add a marketplace option` | `scripts/smoke.sh` | A posted option is stored and rendered in the list | — | S2, S3, S6 |
| `a duplicate option is refused in words` | `scripts/smoke.sh` | The uniqueness refusal reaches the operator as a sentence | — | S5, S6 |
| `a non-administrator cannot reach the marketplace options` | `scripts/smoke.sh` | The admin guard covers the new routes | — | S4 |
| `a field with no options still renders its section` | `scripts/smoke.sh` | An empty list is addable-to | — | S2 |
| `a removed option is gone from the list` | `scripts/smoke.sh` | Removal takes the row off the list, asserted on the row's own id | — | S3, S6 |
| `and the list says so rather than going blank` | `scripts/smoke.sh` | The last removal returns the section to its addable empty state, not to nothing | — | S2, S6 |
| `what was typed reaches the server and comes back as a row` | `scripts/browser/marketplace.js` | The spread-built `data-bind` resolves to the signal the handler reads — the Invariant nothing else can see | — | S3, S7 |
| `the boxes are emptied by the save, not left loaded` | `scripts/browser/marketplace.js` | The signal patch lands, so what was just submitted is not one press from being submitted again | — | S3, S7 |
| `the duplicate refusal is a sentence an operator can act on` | `scripts/browser/marketplace.js` | The sentinel's message reaches the rendered flash slot, not just the response body | — | S5, S7 |
| `the list editor fits a 390px viewport` | `scripts/browser/marketplace.js` | Three sections of add-boxes do not force a phone to scroll sideways | — | S3, S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The handlers compile and the view renders |
| 2 — something selects it | `internal/web/routes.go`; the smoke walk 404s if the route line is deleted |
| 3 — the caller can discover it | The screen is linked from Settings; the smoke walk asserts the link is on the settings page, not just that the URL answers |
| 4 — it is used | Nothing measures this yet |

## Mutation Log

- 2026-09-09 · 9f4f50e* · mutant killed · exit 1 · `internal/web/handlers_settings.go` · the two ends of the binding stop agreeing: the input still sends optValueCategory and the handler now reads a lowercased name, which is exactly what an attribute written camelCase would do silently · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · covers:the signal binding
- 2026-09-09 · 9f4f50e* · mutant killed · exit 2 · `internal/web/handlers_settings.go` · a field with no options stops getting a group, so its section disappears — the state every fresh installation is in, and the one where nobody can add the first value · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · covers:the rendered list
- 2026-09-09 · 9f4f50e* · mutant killed · exit 1 · `internal/web/app.go` · the duplicate sentinel stops being recognised, so adding the same value twice falls through to "Something went wrong. The details are in the server log." — the exact defect ADR-021 T5 shipped · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · covers:the refusal message
- 2026-09-09 · 9f4f50e* · mutant killed · exit 5 · `internal/web/routes.go` · the group holding the two new routes loses its admin guard, so a signed-in staff account can shape what every offer in the warehouse is allowed to say to a marketplace · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · covers:the route's admin guard

## Invariants

- No `<form>` element (ADR-008), and no `data-bind:` attribute written in camelCase — HTML lowercases attribute names and the signal silently differs.
- Every new route is admin-only and that is asserted, not assumed.
- `*_templ.go` is regenerated by `templ generate`, never hand-edited.

## Risks

- A screen with no Go tests. Mitigated by every claim here being a smoke assertion over the real binary — which is what caught the defect ADR-021 T5 shipped.
- Reorder is the fiddliest interaction; if it proves awkward without a form, ship add/remove and leave order as insertion order.

## Stop Condition

Stop if the list editor cannot be built without a `<form>` — that would put this task in conflict
with ADR-008, and the right move is a decision about that boundary rather than a quiet exception.

## Out of Scope

- The offer editor's dropdowns — T4.
- Importing a list from CSV (deferred: docs/adr/BACKLOG.md)

## Verification Log
- 2026-09-09 · 9f4f50e* · exit 0 · `set -o pipefail …` · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · ms:13734
- 2026-09-09 · 9f4f50e* · exit 0 · `set -o pipefail …` · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · ms:13827
- 2026-09-09 · 9f4f50e* · exit 0 · `set -o pipefail …` · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · ms:13724
- 2026-09-09 · 9f4f50e* · exit 0 · `set -o pipefail …` · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · ms:14001
- 2026-09-09 · 9f4f50e* · exit 0 · `set -o pipefail …` · acceptance-sha256:de6f3ef6feafeffd2f731eaf37ba8d255aa081f01f7edf8961f507b8abd38550 · ms:14026
