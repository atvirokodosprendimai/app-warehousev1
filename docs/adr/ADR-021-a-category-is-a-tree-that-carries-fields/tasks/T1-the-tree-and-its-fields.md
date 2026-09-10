# Task ADR-021-T1: The tree, its fields, and the values that hang off an offer

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** L (new package, new migration, new domain types)
**Owner:** M
**Produces:** `core.Category`, `core.CategoryField`, `core.FieldKind`, `core.FieldValue`, `Offer.CategoryID`, `Offer.Fields`, the taxonomy ports on `core.ports`, `migrations/00009_categories.sql`, and the `internal/taxonomy` package
**Consumes:** `internal/store`'s two handles, and `internal/location`'s materialised-path discipline as a PATTERN — no code is shared
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `fields inherit from every ancestor root-first`, `a subtree move rewrites descendant paths by offset`, `a duplicate root is refused`, `a value is keyed by field id`, `deleting a field takes its values with it`

## Goal

The operator's own tree of part types exists in the database, any node can carry
fields, and an offer filed under a node resolves every field from the root down.
Nothing renders it — that is T2 and T3 — and no export sees it yet, which is T4.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `migrations/00009_categories.sql` | create | `categories`, `category_fields`, `offer_field_values`, and `offers.category_id` |
| `internal/core/taxonomy.go` | create | `Category`, `CategoryField`, `FieldKind`, `FieldValue`, and the pure resolution of an ancestor chain into an ordered field list |
| `internal/core/taxonomy_test.go` | create | the kind and resolution assertions below |
| `internal/core/offer.go` | edit | `CategoryID string` and `Fields []FieldValue`, each doc-commented against ADR-016's `Categories` |
| `internal/core/ports.go` | edit | `TaxonomyReader` / `TaxonomyWriter` / `TaxonomyStore`, shaped like `LocationReader` and friends |
| `internal/taxonomy/repo.go` | create | the adjacency + materialised-path store, mirroring `internal/location/repo.go` |
| `internal/taxonomy/service.go` | create | create / rename / move / delete, and the ancestor walk that resolves fields |
| `internal/taxonomy/repo_test.go` | create | the inheritance, move and uniqueness assertions below |
| `internal/offer/repo.go` | edit | load `CategoryID` and, where an offer is read whole, its resolved `Fields` |

<`internal/core/taxonomy.go` holds the TYPES and the one pure function;
`internal/taxonomy` holds the SQL. That is exactly how `core.Location` and
`internal/location` already divide, and copying the division is what makes the
resolution testable without a database.>

## Ordered Steps

1. [S1] Write `TestFieldsInheritFromEveryAncestor` against a package that does not exist and confirm it fails. ⚠ The red here is a COMPILE failure, not a wrong value — record it as such rather than pretending an assertion ran.
2. [S2] Write `migrations/00009_categories.sql`, up and down. `UNIQUE(path)` is the constraint that catches a duplicate root; `UNIQUE(parent_id, code)` is kept for the readable error but is INERT at the root and the migration says so in a comment. [proof: acceptance]
3. [S3] Add the domain types and `FieldKind`'s five values, with a parser that REFUSES an unknown kind on the way in. A renderer tolerating an unknown kind (parent ADR, Risks) is a READ concern; a write that stores one is a bug.
4. [S4] Add `Offer.CategoryID` and `Offer.Fields`, each doc-commented to point at ADR-016's `Offer.Categories` so the collision is visible at the only place somebody reads it. [proof: acceptance]
5. [S5] Implement the repo: create, rename, `Ancestors`, `Children`, `FieldsFor`, and `MoveSubtree`. ⚠ `MoveSubtree` rewrites descendant paths with `substr(path,1,n)` where `n` comes from `utf8.RuneCountInString` — never a concatenated `LIKE`, whose `_` is a wildcard that silently matches a sibling subtree. [proof: acceptance]
6. [S6] Implement value persistence keyed by FIELD ID, with `ON DELETE CASCADE` from `category_fields`, so deleting a question deletes the answers that no longer have one. [proof: acceptance]
7. [S7] Load `CategoryID` and the resolved `Fields` where `internal/offer` reads an offer whole, leaving the list read paths untouched — a listing does not render custom fields and should not pay an ancestor walk per row. [proof: acceptance]
8. [S8] Run the down migration and back up again, because this corpus has never exercised one and a new table is the cheapest place to start. [proof: acceptance]

## Acceptance

```bash
set -o pipefail
go test ./internal/taxonomy/ -run '^TestFieldsInheritFromEveryAncestor$' -count=1 2>&1 | tee /tmp/adr021t1-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t1-new.out && \
go test ./internal/taxonomy/ ./internal/core/ -run '^(TestFieldsInheritFromEveryAncestor|TestMovingASubtreeRewritesEveryDescendantPath|TestASecondRootWithTheSameCodeIsRefused|TestAValueSurvivesRenamingItsField|TestDeletingAFieldDeletesItsValues|TestAnUnknownFieldKindIsRefused)$' -count=1 2>&1 | tee /tmp/adr021t1-named.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t1-named.out && \
go test ./... -count=1 2>&1 | tee /tmp/adr021t1-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr021t1-reg.out && \
bash scripts/smoke.sh 2>&1 | tee /tmp/adr021t1-smoke.out && \
grep -q "SMOKE_FAILURES=0" /tmp/adr021t1-smoke.out
```

The smoke run is in the fence even though this task renders nothing, because it
is the only segment that runs the new migration against a database built from
scratch by the real migration set (ADR-013) rather than by a test fixture.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestFieldsInheritFromEveryAncestor` | `internal/taxonomy/repo_test.go` | A node three deep resolves its own fields AND both ancestors', root first and in `position` order | — | S1, S5 |
| `TestMovingASubtreeRewritesEveryDescendantPath` | `internal/taxonomy/repo_test.go` | A move rewrites descendants by rune offset and leaves a SIBLING whose path shares a prefix under the `_` wildcard untouched | — | S5 |
| `TestASecondRootWithTheSameCodeIsRefused` | `internal/taxonomy/repo_test.go` | `UNIQUE(path)` catches what `UNIQUE(parent_id, code)` cannot, because every NULL parent is distinct | — | S2 |
| `TestAValueSurvivesRenamingItsField` | `internal/taxonomy/repo_test.go` | Renaming a field's label or code leaves the value readable, because the row is keyed by field id | — | S6 |
| `TestDeletingAFieldDeletesItsValues` | `internal/taxonomy/repo_test.go` | The cascade is real, so no answer outlives its question | — | S6 |
| `TestAnUnknownFieldKindIsRefused` | `internal/core/taxonomy_test.go` | A kind outside the five is refused on write | — | S3 |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | The migration runs and the tables answer a query in every test above |
| 2 — something selects it | `FieldsFor` returns the resolved list for a node, and `Ancestors` the chain it walked |
| 3 — the caller can discover it | The ports are on `core.ports` beside `LocationReader`, which is where the web layer already looks for a tree. ⚠ **No screen reaches this in T1, and the task claims none.** T2 and T3 are where a human can find it; asserting discoverability here would be asserting a thing this task deliberately does not build |
| 4 — it is used | `internal/offer` loads `CategoryID` and `Fields` when it reads an offer whole, so the values are on the domain object every later task reads |

## Mutation Log

To be completed by `adr-verify` during execution; each entry binds to the
acceptance digest of the run that killed it.
- 2026-09-07 · 922b941* · mutant killed · exit 1 · `internal/taxonomy/repo.go` · inheritance resolves LEAF-first, so an operator filing a turbocharger meets the turbo questions before the general car ones — the order the parent ADR chose deliberately, reversed, with every field still present so nothing looks missing · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · covers:fields inherit from every ancestor root-first
- 2026-09-07 · 922b941* · mutant killed · exit 1 · `internal/taxonomy/repo.go` · the descendant rewrite matches the old path with LIKE instead of substr, so a node whose code contains an underscore re-addresses an unrelated sibling subtree — "_" is a LIKE wildcard, "A_B" is a legal code, and every path it silently rewrites points at a node that is not there · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · covers:a subtree move rewrites descendant paths by offset
- 2026-09-07 · 922b941* · mutant killed · exit 1 · `migrations/00009_categories.sql` · the path index is dropped and only UNIQUE(parent_id, code) is left, which is INERT at the root because SQL treats every NULL parent as distinct — so two roots called CAR both insert, two different trees answer to one address, and the pasted path that used to resolve to exactly one node now resolves to either · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · covers:a duplicate root is refused
- 2026-09-07 · 922b941* · mutant killed · exit 1 · `internal/taxonomy/repo.go` · answers are looked up by the field CODE rather than its id, so renaming a question silently orphans every answer already given to it — the offer still renders, the question is still asked, and what somebody typed is simply gone with no error anywhere · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · covers:a value is keyed by field id
- 2026-09-07 · 922b941* · mutant killed · exit 1 · `migrations/00009_categories.sql` · answers no longer follow their question out of the database, so deleting a field is refused by a bare FOREIGN KEY error the operator cannot act on — and were it not refused, answers would outlive the question that gives them meaning, which is a value nothing can interpret · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · covers:deleting a field takes its values with it

## Invariants

- A category is optional on an offer; nothing here makes one required.
- Inheritance is resolved at READ time. No field is ever copied down, so adding a field to a parent reaches the offers already filed beneath it.
- A value is keyed by field id, never by code.
- Tests run the real migrations (ADR-013). No hand-copied schema fixture.

## Risks

- **`MoveSubtree` is the one destructive path here**, and the sibling-prefix case is the one that fails silently. It carries its own named test for that reason, and the test asserts the untouched sibling rather than only the moved subtree — a move that rewrote everything would otherwise pass.
- **`Offer.Fields` is a read-model on a domain type.** That is ADR-016's precedent (`Offer.Categories`) rather than a new idea, but it means an offer loaded by a LIST path has an empty `Fields` that is indistinguishable from a category with no fields. S7 confines the load to the whole-offer read deliberately; T3 and T4 are the only consumers and both read whole offers.
- **The down migration has never been run in this repository.** S8 runs it. If it fails, that is a finding about the corpus rather than about this task, and it goes to `BACKLOG.md` under the gap that already names it.

## Stop Condition

Stop and ask if resolving fields turns out to need anything from `offer_categories`
(ADR-016). It should not — they are different things wearing one word — and a
dependency appearing between them means the parent ADR's central distinction is
wrong rather than merely subtle.

## Out of Scope

- Any screen at all — T2 and T3 own rendering.
- The `export` flag on a field and anything in `internal/export` — T4 owns it.
- Filtering the offers listing by a field's value — ADR-021 defers it to `BACKLOG.md`.

## Verification Log

To be completed by `adr-verify` during execution.
- 2026-09-07 · 922b941* · exit 0 · `set -o pipefail …` · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · ms:12837
- 2026-09-07 · 922b941* · exit 0 · `set -o pipefail …` · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · ms:13594
- 2026-09-07 · 922b941* · exit 0 · `set -o pipefail …` · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · ms:13848
- 2026-09-07 · 922b941* · exit 0 · `set -o pipefail …` · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · ms:13457
- 2026-09-07 · 922b941* · exit 0 · `set -o pipefail …` · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · ms:13699
- 2026-09-07 · 922b941* · exit 0 · `set -o pipefail …` · acceptance-sha256:757c4afb164313e8abb6675cdbf641ee0e893afef181fde8d8e53d81cfbfc190 · ms:13862
