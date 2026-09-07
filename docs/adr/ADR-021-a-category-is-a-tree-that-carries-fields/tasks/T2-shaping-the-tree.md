# Task ADR-021-T2: An operator can shape the tree and the questions it asks

**Depends-on:** T1
**Covers:** none — no spec
**Estimated scope:** M (multi-file, one new screen)
**Owner:** M
**Produces:** the `/categories` screens and their handlers — create, rename, move, delete a node; add, edit, reorder, delete a field on a node
**Consumes:** the `TaxonomyStore` ports T1 produces
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `a node can be created under any node`, `a field is attached to a node`, `inherited fields are shown as inherited and not editable here`, `deleting a field says how many values it will take`, `the taxonomy is admin-only`

## Goal

An operator can build "Car parts › Engine › Turbocharger" and attach the
questions each level asks, without us shipping a schema per trade.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/routes.go` | edit | the `/categories` routes, inside the ADMIN group |
| `internal/web/handlers_category.go` | create | the handlers, shaped like `handlers_place.go` |
| `internal/web/view/category.templ` | create | the tree screen and the field editor, shaped like `place.templ` |
| `internal/web/view/layout.templ` | edit | a navigation entry, so the screen is reachable without typing a URL |
| `internal/web/view/category_contract_test.go` | create | the assertions below |
| `scripts/smoke.sh` | edit | walks: create a root, a child, a field on each, and read the child back |

<`/warehouse` is the same screen for the other tree, so this one copies its
shape deliberately — an operator who has moved a shelf should not have to learn
a second idiom to move a category.>

## Ordered Steps

1. [S1] Write the tests below against markup that does not exist and confirm they are RED.
2. [S2] Add the routes to the ADMIN group. ⚠ Shaping the vocabulary is an administrative act, not an intake one: a cataloguer files an offer under a node, and changing what nodes EXIST changes what every offer can say. This is a decision, not an oversight, and the smoke walk pins it the way it already pins `staff cannot open settings` — with a real 403 from a real staff session, because `internal/web` carries no Go tests and a route's protection is not visible from its markup. [proof: acceptance]
3. [S3] Render the tree with its nodes and, on a selected node, its OWN fields and its INHERITED ones — the inherited set shown with the ancestor it comes from and not editable here, because editing it there would mean editing it for every sibling subtree without saying so. [proof: acceptance]
4. [S4] Create, rename, move and delete a node, each posting a datastar signal. ⚠ No `<form>` (ADR-008), and `data-bind:` attribute names are kebab-case — HTML lowercases attribute names, so `data-bind:fieldLabel` binds `fieldlabel`, a DIFFERENT signal, and nothing reports it.
5. [S5] Add, edit, reorder and delete a field, with the five kinds, the optional unit on `number`, and the option list on `choice`. [proof: acceptance]
6. [S6] Make deletion of a field state how many stored values it will take with it, because the cascade is deliberate (parent ADR, Consequences) and a deliberate loss still has to be visible before it happens. [proof: acceptance]
7. [S7] Regenerate the committed `*_templ.go`. ⚠ Through `templ generate` — never by editing a `*_templ.go`. [proof: acceptance]
8. [S8] Extend the smoke walk. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate && \
go test ./internal/web/view/ -run '^TestACategoryTreeCanBeShaped$' -count=1 2>&1 | tee /tmp/adr021t2-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t2-new.out && \
go test ./internal/web/... -run '^(TestACategoryTreeCanBeShaped|TestInheritedFieldsAreShownAsInherited|TestDeletingAFieldSaysWhatItTakes|TestNoFormsOutsideFileUpload|TestEveryIndicatorSignalHasAConsumer)$' -count=1 2>&1 | tee /tmp/adr021t2-named.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t2-named.out && \
go test ./... -count=1 2>&1 | tee /tmp/adr021t2-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t2-reg.out && \
bash scripts/smoke.sh 2>&1 | tee /tmp/adr021t2-smoke.out && \
grep -q "SMOKE_FAILURES=0" /tmp/adr021t2-smoke.out
```

`templ generate` leads because the committed generated files are what the view
tests read. The smoke run is last because it is the only segment that proves the
ROUTES answer — a rendered control proves the markup, and only an HTTP request
proves the handler behind it exists and refuses a non-admin.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestACategoryTreeCanBeShaped` | `internal/web/view/category_contract_test.go` | The screen renders a node's children, and carries controls that post a create, a rename, a move and a delete | — | S1, S3, S4 |
| `TestInheritedFieldsAreShownAsInherited` | `internal/web/view/category_contract_test.go` | A child node's screen shows its ancestor's fields, labelled with the ancestor, and offers no edit control for them | — | S3 |
| `TestDeletingAFieldSaysWhatItTakes` | `internal/web/view/category_contract_test.go` | The delete control renders the count of values the cascade will remove | — | S6 |
| the smoke walk's `staff cannot open categories` | `scripts/smoke.sh` | A signed-in non-admin gets 403 from `/categories`, so shaping the vocabulary needs the same standing as changing a setting. ⚠ Not a Go test: `internal/web` has none, and this is where the auth boundary is already proved | — | S2 |
| `TestNoFormsOutsideFileUpload` | `internal/web/view/upload_contract_test.go` | The new screen introduced no `<form>` (ADR-008) | — | S4, S5 |
| `TestEveryIndicatorSignalHasAConsumer` | `internal/web/view/upload_contract_test.go` | Every busy indicator this screen adds is wired to something | — | S4, S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestACategoryTreeCanBeShaped` renders the screen and finds the tree and its controls |
| 2 — something selects it | The routes answer; the smoke walk creates a root and a child and reads the child back |
| 3 — the caller can discover it | S4's navigation entry, asserted by rendering the shell — ⚠ **this rung is the one ADR-020 got wrong once**, by claiming a control was on every page while two handlers passed it. The assertion here renders the chrome and looks for the link rather than trusting that the entry was added |
| 4 — it is used | The smoke walk attaches a field to each of two levels, which is what T3 then resolves |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.

## Invariants

- No `<form>` outside the photo upload (ADR-008).
- Every `data-bind:` is kebab-case.
- An inherited field is never edited from a descendant's screen.
- Shaping the tree requires admin standing.

## Risks

- **A tree nobody prunes becomes a tree nobody can navigate** — the parent ADR names this as the cost of the whole record. This task adds no depth limit and no warning, deliberately: a limit picked here would be a number with no reason behind it.
- **Deleting a node with children.** The schema's `ON DELETE RESTRICT` (T1) refuses it, so the screen must say "move or delete the children first" rather than offering a control that returns an error. A refusal the interface did not predict reads as a bug.
- **Reordering fields is a `position` rewrite over several rows.** It is one transaction on the writer handle, which is capped at one connection (ADR-001), so it cannot interleave with another reorder.

## Stop Condition

Stop and ask if shaping the tree turns out to want a field defined once and
REUSED across unrelated branches — a "VIN" shared by two roots. That is a
different model (a field library with attachments) and the parent ADR chose
inheritance instead; adding it quietly would make the tree's meaning ambiguous.

## Out of Scope

- Rendering fields on an offer — T3 owns it.
- The `export` flag — T4 owns it, including its control on this screen.
- Importing a ready-made taxonomy from a marketplace — ADR-021 refuses it permanently.
- A field whose options depend on another field's answer — ADR-021 defers it to `BACKLOG.md`.

## Verification Log

To be completed by `adr-verify` during execution.
