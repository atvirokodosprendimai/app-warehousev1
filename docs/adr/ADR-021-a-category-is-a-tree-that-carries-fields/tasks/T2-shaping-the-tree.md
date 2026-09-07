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
| `internal/web/view/model.go` | edit | the `Taxonomy` read model this screen renders |
| `internal/web/view/upload_contract_test.go` | edit | the new screen joins the no-forms map — ⚠ a screen absent from that map is not checked, and the omission is silent |
| `internal/web/app.go`, `cmd/warehouse/main.go` | edit | the taxonomy repo and service reach the handlers |

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
go test ./internal/web/... -run '^(TestACategoryTreeCanBeShaped|TestInheritedFieldsAreShownAsInherited|TestDeletingAFieldSaysWhatItTakes|TestEveryFieldKindCanBeChosen|TestTheExportTickIsOnTheQuestion|TestTheTaxonomyScreenIsReachableWithoutTypingAURL|TestNoFormsOutsideFileUpload|TestEveryIndicatorSignalHasAConsumer)$' -count=1 2>&1 | tee /tmp/adr021t2-named.out && \
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
| `TestEveryFieldKindCanBeChosen` | `internal/web/view/category_contract_test.go` | All five kinds are reachable from the picker, and the unit / options / order / required / export controls exist | — | S5 |
| `TestTheExportTickIsOnTheQuestion` | `internal/web/view/category_contract_test.go` | A question can be marked for export where it is defined, and an exported one is visible as such in the list without opening it | — | S5 |
| `TestTheTaxonomyScreenIsReachableWithoutTypingAURL` | `internal/web/view/category_contract_test.go` | The shell renders a Categories entry for an administrator and none for anybody else. ⚠ Written because the Reachability row below CLAIMED this before anything asserted it | — | S2 |
| the smoke walk's `staff cannot open categories` | `scripts/smoke.sh` | A signed-in non-admin gets 403 from `/categories`, so shaping the vocabulary needs the same standing as changing a setting. ⚠ Not a Go test: `internal/web` has none, and this is where the auth boundary is already proved | — | S2 |
| `TestNoFormsOutsideFileUpload` | `internal/web/view/upload_contract_test.go` | The new screen introduced no `<form>` (ADR-008) | — | S4, S5 |
| `TestEveryIndicatorSignalHasAConsumer` | `internal/web/view/upload_contract_test.go` | Every busy indicator this screen adds is wired to something | — | S4, S5 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestACategoryTreeCanBeShaped` renders the screen and finds the tree and its controls |
| 2 — something selects it | The routes answer; the smoke walk creates a root and a child and reads the child back |
| 3 — the caller can discover it | `TestTheTaxonomyScreenIsReachableWithoutTypingAURL` renders the shell and finds the Categories entry for an admin, and none for anybody else. ⚠ **THIS ROW CLAIMED THAT BEFORE IT WAS TRUE.** It said the entry was "asserted by rendering the shell" while nothing asserted anything — the identical shape to ADR-020's rung 3, which said the New offer control was on every page while two handlers passed it, and which M found by doing the job. Caught here by re-reading the row against the tests that existed, and closed by writing the test rather than softening the claim |
| 4 — it is used | The smoke walk attaches a field to each of two levels, which is what T3 then resolves |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.
- 2026-09-07 · b35d8d8* · mutant killed · exit 4 · `internal/web/handlers_category.go` · the "Inside" picker is ignored and every category is created as a ROOT, so the tree is permanently flat — no node ever has an ancestor, and inheritance, which is the whole record, can never happen even though the screen appears to offer it · acceptance-sha256:25df7ec0a1230aa6981ced01a543da11df88e828bf7c6ff666cbe43b88baf601 · covers:a node can be created under any node
- 2026-09-07 · b35d8d8* · mutant killed · exit 1 · `internal/web/handlers_category.go` · the inherited and own lists are swapped: a question this category defines is shown under "Inherited" and cannot be edited or deleted, while the ancestor questions appear as this level own — so the one screen that decides what an operator may safely change tells them the opposite of the truth · acceptance-sha256:25df7ec0a1230aa6981ced01a543da11df88e828bf7c6ff666cbe43b88baf601 · covers:inherited fields are shown as inherited and not editable here
- 2026-09-07 · b35d8d8* · mutant killed · exit 4 · `internal/web/handlers_category.go` · the "Inside" picker is ignored and every category is created as a ROOT, so the tree is permanently flat — no node ever has an ancestor, and inheritance, which is the whole record, can never happen even though the screen appears to offer it · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · covers:a node can be created under any node
- 2026-09-07 · b35d8d8* · mutant killed · exit 1 · `internal/web/handlers_category.go` · the inherited and own lists are swapped: a question this category defines is shown under "Inherited" and cannot be edited or deleted, while the ancestor questions appear as this level own — so the one screen that decides what an operator may safely change tells them the opposite of the truth · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · covers:inherited fields are shown as inherited and not editable here
- 2026-09-07 · b35d8d8* · mutant survived · exit 0 · `internal/web/handlers_category.go` · a question is created with no category, so it hangs off nothing: the foreign key refuses it and adding any question to any category fails outright — the screen offers the control and the write never lands · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · covers:a field is attached to a node
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-07 · b35d8d8* · mutant killed · exit 1 · `internal/web/view/category.templ` · the delete control stops saying how many stored answers it will destroy, so an operator tidying up a question nobody uses cannot tell it apart from one carrying three hundred answers — the cascade is deliberate, and this is the only thing that made the loss visible before it happened · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · covers:deleting a field says how many values it will take
- 2026-09-07 · b35d8d8* · mutant survived · exit 0 · `internal/web/handlers_category.go` · probe · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · covers:a field is attached to a node
  ```
  the fence passed with the mechanism broken; it may not materialize, compile, load, or assert on the changed path
  ```
- 2026-09-07 · b35d8d8* · mutant killed · exit 1 · `internal/taxonomy/service.go` · a question is attached to no category at all, so the foreign key refuses the insert and adding ANY question to ANY category fails — the screen offers the control, the operator fills it in, and nothing is ever created · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · covers:a field is attached to a node
- 2026-09-07 · b35d8d8* · mutant killed · exit 3 · `internal/web/routes.go` · the taxonomy leaves the admin group, so any signed-in staff member can rewrite the vocabulary every offer in the warehouse is described in — renaming a category re-addresses a whole subtree, and deleting a question destroys every answer given to it · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · covers:the taxonomy is admin-only

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
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:25df7ec0a1230aa6981ced01a543da11df88e828bf7c6ff666cbe43b88baf601 · ms:15006
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:25df7ec0a1230aa6981ced01a543da11df88e828bf7c6ff666cbe43b88baf601 · ms:14548
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:25df7ec0a1230aa6981ced01a543da11df88e828bf7c6ff666cbe43b88baf601 · ms:14844
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · ms:14515
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · ms:14261
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · ms:14292
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · ms:13826
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · ms:14059
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · ms:15867
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · ms:14446
- 2026-09-07 · b35d8d8* · exit 0 · `set -o pipefail …` · acceptance-sha256:4ca901b6b848f9f8444cc9980c6ff7d6078660921aff732950fb998e5c67b087 · ms:14094
