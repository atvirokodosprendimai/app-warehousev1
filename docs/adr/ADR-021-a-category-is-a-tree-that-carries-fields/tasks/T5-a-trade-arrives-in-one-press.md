# Task ADR-021-T5: A trade's whole question set arrives in one press

**Depends-on:** T1, T2
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** `core.CategoryTemplate`, the two shipped templates, `Service.ApplyTemplate`, `Repo.CreateTree`, `POST /categories/template/{code}`, and the controls that apply one
**Consumes:** T1's `categories` / `category_fields` schema and its read-time inheritance, T2's taxonomy screen
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `every shipped template is valid`, `a template builds its whole tree in one press`, `a template's child inherits the root's questions`, `applying a template twice is refused and changes nothing`, `a failed template leaves nothing behind`, `the templates are offered on the screen somebody starts from`

## Goal

M: *"make 'tempplate' for cars and pc parts so in 1 click it will create all
fields reuquired and optional from @srotas1/"*.

ADR-021 gave an operator a tree they own and questions they write. What it did
not give them is a **start**: a scrap yard opening this application for the first
time faces an empty screen and about forty decisions before it can describe a
single part. The seven screenshots in `srotas1/` are that work already done by
somebody else — app.recar.lt's five-step intake form — and this task turns it
into one press.

⚠ **A TEMPLATE IS NOT A SCHEMA, AND THE DIFFERENCE IS THE WHOLE POINT.** What it
writes is ordinary categories and questions with nothing marking them as
generated: renameable, deletable, extendable, indistinguishable a minute later
from ones somebody typed. ADR-021 exists because a schema per trade shipped by us
is what this product must not have. A template is the opposite — it hands over
the typing, not the ownership.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/core/template.go` | add | The template descriptor, in `core` so the view can render a list of them without importing the taxonomy service |
| `internal/taxonomy/templates.go` | add | The two shipped templates, as data |
| `internal/taxonomy/templates_test.go` | add | The five proofs, including the one that shows the transaction is real |
| `internal/taxonomy/repo.go` | edit | `CreateTree`: every category and question in ONE transaction |
| `internal/taxonomy/service.go` | edit | `Store` gains `CreateTree`; `ApplyTemplate` composes ids, paths and positions |
| `internal/web/handlers_category.go` | edit | `PostCategoryTemplate`, and the template list on every exit of `taxonomyScreen` |
| `internal/web/routes.go` | edit | `POST /categories/template/{code}`, admin-only beside the rest of the taxonomy |
| `internal/web/view/model.go` | edit | `Taxonomy.Templates` |
| `internal/web/view/category.templ` | edit | The controls, and the empty state that points at them |
| `internal/web/view/category_contract_test.go` | edit | That they are offered, and say what they build |
| `scripts/smoke.sh` | edit | The whole path over the real binary, including the refusal |

## Ordered Steps

1. [S1] Write `TestEveryShippedTemplateIsValid` and confirm it is RED against an empty `Templates()`. ⚠ A template is data WE wrote that nothing reads until an operator presses the button — a choice with no options or a blank label compiles fine and is discovered by somebody watching their taxonomy half-appear. [proof: acceptance]
2. [S2] Add `core.CategoryTemplate` with `Questions()` and `Nodes()`, so the count under the button is derived from the template rather than typed beside it. [proof: acceptance]
3. [S3] Write the two templates from `srotas1/`. ⚠ **NOTHING THAT THE OFFER ALREADY CARRIES.** Price, SKU, location and description all appear on the source form and all are columns on `core.Offer`; a template that asked again would give the operator two boxes for one fact and two answers that disagree. This is M's own rule from the parent record — *"which we dont have already"*. [proof: acceptance]
4. [S4] Add `Repo.CreateTree`: every category and every question in ONE transaction. ⚠ A half-applied template LOOKS FINISHED — the operator cannot tell which of seven categories and twenty-three questions arrived, so they can neither trust it nor safely retry. [proof: acceptance]
5. [S5] Add `Service.ApplyTemplate`: compose ids, paths and positions, validate every node and question before the first write, refuse a duplicate root rather than merging into a tree the operator has since edited. ⚠ Position comes from the SLICE INDEX, never from the data: a hand-kept column of numbers is a second copy of the order that stops agreeing the first time somebody inserts a question in the middle. [proof: acceptance]
6. [S6] Add the route and handler, answering the duplicate-root refusal in words that name the code in the way. ⚠ `userMessage` does not map the taxonomy sentinels at all, so the generic path would hand the operator a wrapped SQLite `UNIQUE constraint failed`. [proof: acceptance]
7. [S7] Put the controls on the taxonomy screen, under the new-category form, using only class names the stylesheet already defines. ⚠ ADR-021's first two screens shipped with eleven invented class names and every gate passed, because nothing in this repository reads a stylesheet. [proof: acceptance]
8. [S8] Extend the smoke walk over the real binary: apply, assert the tree and the inheritance, apply again and assert the refusal is in words. [proof: acceptance]
9. [S9] ⚠ **UNPLANNED, AND FOUND BY THE BROWSER WALK.** `taxonomyScreen` has THREE exits — no selection, a stale bookmark, the full read — and S7 filled the template list in beside the LAST one. The buttons therefore rendered on every screen except the empty one a new installation opens on, which is the only screen a template is for. This is the same shape as the defect M reported in the offer editor the same day, and no Go test could see it: `internal/web` has none, and the view tests build the read model by hand. Set at construction, where no exit can miss it, and pinned by a smoke assertion on `GET /categories` with nothing in the tree. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/taxonomy/ -run '^TestEveryShippedTemplateIsValid$' -count=1 2>&1 | tee /tmp/adr021t5-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t5-new.out && \
go test ./internal/taxonomy/ ./internal/web/view/ -run '^(TestEveryShippedTemplateIsValid|TestATemplateBuildsItsWholeTreeInOnePress|TestATemplatesChildInheritsTheRootsQuestions|TestApplyingATemplateTwiceIsRefusedAndChangesNothing|TestAFailedTemplateLeavesNothingBehind|TestAStarterTemplateIsOfferedOnTheScreenSomebodyStartsFrom|TestATemplateSaysHowMuchItIsAboutToBuild)$' -count=1 2>&1 | tee /tmp/adr021t5-named.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t5-named.out && \
go test ./... -count=1 2>&1 | tee /tmp/adr021t5-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t5-reg.out && \
bash scripts/smoke.sh 2>&1 | tee /tmp/adr021t5-smoke.out && \
grep -q "SMOKE_FAILURES=0" /tmp/adr021t5-smoke.out
```

The smoke run is inside the fence rather than beside it because the two things
this task can get wrong that no unit test sees — the route being reachable at
all, and the buttons rendering on the empty screen — are both only visible over
the real binary.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestEveryShippedTemplateIsValid` | `internal/taxonomy/templates_test.go` | Every shipped template passes the same `Validate` the service walks, asks no question twice on one category, and carries a unit only on a number | — | S1, S3 |
| `TestATemplateBuildsItsWholeTreeInOnePress` | `internal/taxonomy/templates_test.go` | One call produces exactly the advertised number of categories, every child inside the root by both path and parent id | — | S5 |
| `TestATemplatesChildInheritsTheRootsQuestions` | `internal/taxonomy/templates_test.go` | A graphics card is asked the root's five questions, its own three, and none of a sibling's — which is the reason the PC template has children at all | — | S3, S5 |
| `TestApplyingATemplateTwiceIsRefusedAndChangesNothing` | `internal/taxonomy/templates_test.go` | The second press is refused with a sentinel the handler can recognise, and the tree is the size it was | — | S5, S6 |
| `TestAFailedTemplateLeavesNothingBehind` | `internal/taxonomy/templates_test.go` | A template failing PART-WAY leaves no categories. ⚠ The duplicate-root case cannot prove this — it fails on the first statement, where a rollback and no transaction at all are indistinguishable — so this one fails on a second question sharing a code, after the category is already inserted | — | S4 |
| `TestAStarterTemplateIsOfferedOnTheScreenSomebodyStartsFrom` | `internal/web/view/category_contract_test.go` | Both templates render with their apply controls on a taxonomy with nothing in it | — | S7 |
| `TestATemplateSaysHowMuchItIsAboutToBuild` | `internal/web/view/category_contract_test.go` | The counts under each button are computed from the template, and the one-node template says "1 category" rather than "1 categories" | — | S2, S7 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestATemplateBuildsItsWholeTreeInOnePress` applies one and reads the tree back out of the database |
| 2 — something selects it | `Template(code)` resolves the code the route carries, case-insensitively, and the handler refuses an unknown one in words |
| 3 — the caller can discover it | The controls are in the New category card AND the empty state points at them — asserted by `TestAStarterTemplateIsOfferedOnTheScreenSomebodyStartsFrom` on an empty `Taxonomy`, and over HTTP by the smoke walk's `GET /categories` before anything exists. ⚠ This rung is where S9's defect lived, and it was NOT caught by the rung as first written: the view test builds the read model by hand and so cannot see a handler that never fills it in. The smoke assertion is the half that can |
| 4 — it is used | The smoke walk applies a template over the real binary and then reads the questions off a child category's screen |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.
- 2026-09-07 · d46fed3* · mutant killed · exit 1 · `internal/taxonomy/templates.go` · a shipped template offers a choice with no options, so the operator gets a question in their taxonomy that literally nobody can answer — and finds out by opening an offer · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · covers:every shipped template is valid
- 2026-09-07 · d46fed3* · mutant killed · exit 1 · `internal/taxonomy/service.go` · a template silently drops every subcategory, so the PC template creates a bare root and the operator is asked five generic questions instead of the twenty-three the button promised — and the screen shows no sign anything is missing · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · covers:a template builds its whole tree in one press
- 2026-09-07 · d46fed3* · mutant killed · exit 1 · `internal/taxonomy/service.go` · every subcategory is created as a ROOT, so a graphics card inherits none of the questions that identify a component and the tree the operator sees is eight unrelated trades side by side · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · covers:a template's child inherits the root's questions
- 2026-09-07 · d46fed3* · mutant killed · exit 1 · `internal/taxonomy/service.go` · the duplicate-root refusal stops firing, so pressing the button twice builds a second identical tree at a different address and the operator files half their stock under each · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · covers:applying a template twice is refused and changes nothing
- 2026-09-07 · d46fed3* · mutant killed · exit 1 · `internal/taxonomy/repo.go` · the categories are committed before the questions are written, so a template that fails part-way leaves a tree of empty categories that looks finished — the operator cannot tell what arrived, and applying it again is refused because the root now exists · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · covers:a failed template leaves nothing behind
- 2026-09-07 · d46fed3* · mutant killed · exit 3 · `internal/web/handlers_category.go` · the templates vanish from every taxonomy screen, so the one press M asked for is unreachable and the feature exists only in the tests — the exact shape of the defect this task S9 already had to fix once · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · covers:the templates are offered on the screen somebody starts from

## Invariants

- A template writes ordinary data. Nothing afterwards records that a category or a question came from one, and nothing may start.
- Nothing a template asks duplicates a column already on `core.Offer`.
- A template is all-or-nothing.
- The counts an operator reads before pressing are derived from the template, never typed beside it.

## Risks

| Risk | Mitigation |
|------|-----------|
| The shipped field sets are our opinion of two trades, and a yard's may differ | Everything is editable the moment it lands, and the button says how much it is about to write before it writes it |
| A template grows a third and fourth trade and this file becomes a catalogue nobody curates | `TestEveryShippedTemplateIsValid` walks every one of them, so an unmaintained addition fails the suite rather than an operator |
| `Position` on a child category is stored and does not order the tree, which sorts by path | Recorded here rather than removed: it is the column the tree would order by if it ever stopped ordering by path |

## Stop Condition

The acceptance fence passes, and an operator opening a taxonomy with nothing in
it can press one control and be asked a trade's questions on the next offer they
file.

## Out of Scope

- Translating the templates (deferred: `docs/adr/BACKLOG.md` — the labels are English because they become CSV headers on international marketplaces; M chose this over Lithuanian on 2026-09-07)
- Mapping taxonomy sentinels in `userMessage` (permanent: fact: done the same day, in the backlog sweep — all six are mapped, so a duplicate category code says so on the hand-typed path too rather than "Something went wrong"; citation: file `internal/web/app.go:357`)
- Editing a template, or re-applying one over an existing tree (permanent: boundary: a template's output is the operator's data from the moment it lands, so the thing to edit is the tree, not the template that seeded it)

## Verification Log

To be completed during execution.
- 2026-09-07 · d46fed3* · exit 0 · `set -o pipefail …` · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · ms:13858
- 2026-09-07 · d46fed3* · exit 0 · `set -o pipefail …` · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · ms:13894
- 2026-09-07 · d46fed3* · exit 0 · `set -o pipefail …` · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · ms:13569
- 2026-09-07 · d46fed3* · exit 0 · `set -o pipefail …` · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · ms:16322
- 2026-09-07 · d46fed3* · exit 0 · `set -o pipefail …` · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · ms:13464
- 2026-09-07 · d46fed3* · exit 0 · `set -o pipefail …` · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · ms:13203
- 2026-09-07 · d46fed3* · exit 0 · `set -o pipefail …` · acceptance-sha256:d58a82014d567b15ff25003f70b93792098f62e2aabdb4aa105a7d245e72fd62 · ms:13461
