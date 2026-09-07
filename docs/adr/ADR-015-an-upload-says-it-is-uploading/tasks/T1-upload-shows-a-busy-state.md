# Task ADR-015-T1: The upload shows a busy state, and the indicator is proved wired

**Depends-on:** none
**Covers:** none — no spec
**Estimated scope:** M (multi-file)
**Owner:** M
**Produces:** the `_uploading` indicator signal and its consumers in `OfferPhotosCard` and the submission upload
**Consumes:** none
**Data dependency:** hermetic
**Proof map:** v1
**Rests-on:** `the rendered markup of the upload label`, `the lowercase-survival rule for indicator attribute names`, `the has-a-consumer rule for indicator signals`, `templ generate keeping the committed generated files in step`

## Goal

Choosing a photograph disables the control and shows a spinner with "Uploading…"
until the server answers, and the indicator that drives it is proved to be wired
rather than merely present.

## Affected Files

| File | Change | Why |
|------|--------|-----|
| `internal/web/view/offer_detail.templ` | edit | Adds `data-indicator:_uploading`, `data-attr:disabled` and the busy/idle label swap to the offer upload |
| `internal/web/view/inbox.templ` | edit | The submission upload is the same control for a different aggregate and gets the same treatment |
| `internal/web/view/offer_detail_templ.go` | edit | Generated from the above by `templ generate`; committed, so it must be regenerated in the same change |
| `internal/web/view/inbox_templ.go` | edit | Generated, as above |
| `internal/web/view/upload_contract_test.go` | edit | Home of the client-contract assertions (ADR-008); the new test and both invariants go here |

<The markup is what SELECTS the behaviour — there is no handler, no route and no
registry, because `data-indicator` is a client-side concern the server never
reads. The line that makes it reachable is the attribute itself, which is exactly
what the tests assert, and the Mutation Log is what shows they can see it.>

## Ordered Steps

1. [S1] Write `TestPhotoUploadShowsABusyState` in `upload_contract_test.go` and confirm it is RED against the current markup, which carries no indicator at all.
2. [S2] Add `data-indicator:_uploading`, `data-attr:disabled="$_uploading"`, and the idle/busy label swap (spinner + `aria-live` text) to the offer upload input in `offer_detail.templ`.
3. [S3] Apply the same treatment to the submission upload in `inbox.templ`.
4. [S4] Regenerate the committed `*_templ.go` from their sources so the two cannot drift. [proof: acceptance]
5. [S5] Add `TestEveryIndicatorNameSurvivesHTMLLowercasing` and `TestEveryIndicatorSignalHasAConsumer`, which hold for every indicator in the package rather than only for this one.

## Acceptance

```bash
set -o pipefail
go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate && \
go test ./internal/web/view/ -run '^TestPhotoUploadShowsABusyState$' -count=1 2>&1 | tee /tmp/adr015-new.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr015-new.out && \
go test ./internal/web/view/ -run '^(TestPhotoUploadShowsABusyState|TestEveryIndicatorNameSurvivesHTMLLowercasing|TestEveryIndicatorSignalHasAConsumer|TestNoFormsOutsideFileUpload|TestOfferPhotoUploadSatisfiesDatastarsFormContract)$' -count=1 2>&1 | tee /tmp/adr015-reg.out && \
! grep -qE "no tests to run|^FAIL|^--- FAIL" /tmp/adr015-reg.out
```

The new unit runs alone first so it cannot be carried by its siblings, then the
whole `view` package runs as the regression half — which is also where both
falsifiability fixtures live, so the thing that proves this can fail is inside
the command rather than beside it. `go tool templ generate` leads because the
committed generated files are what the tests read.

## Tests

| Test name | File | Verifies | Covers | Steps |
|-----------|------|----------|--------|-------|
| `TestPhotoUploadShowsABusyState` | `internal/web/view/upload_contract_test.go` | Both uploads carry an indicator, a disabled binding on it, and a consumer that shows busy text | — | S1, S2, S3 |
| `TestEveryIndicatorNameSurvivesHTMLLowercasing` | `internal/web/view/upload_contract_test.go` | No indicator attribute name contains an uppercase letter, so the signal datastar creates is the signal consumers read | — | S5 |
| `TestEveryIndicatorSignalHasAConsumer` | `internal/web/view/upload_contract_test.go` | Every indicator signal is read by at least one `data-show` or `data-attr` | — | S5 |
| `TestNoFormsOutsideFileUpload` | `internal/web/view/upload_contract_test.go` | The change did not introduce a form outside the upload | — | — |
| `TestOfferPhotoUploadSatisfiesDatastarsFormContract` | `internal/web/view/upload_contract_test.go` | The change did not break ADR-008's form/enctype/name contract | — | — |

## Reachability

| Rung | How this task shows it |
|------|------------------------|
| 1 — exists | `TestPhotoUploadShowsABusyState` renders the component and finds the attributes |
| 2 — something selects it | The attribute IS the selection — there is no call site. The Mutation Log entry deletes the indicator and the fence goes red, which is what shows the named test can see this rather than merely naming it |
| 3 — the caller can discover it | n/a: no declared interface. The consumer is datastar reading the DOM |
| 4 — it is used | Nothing measures this yet. Whether a person on a slow phone actually sees it needs a browser, which this repository does not have |

## Mutation Log

- 2026-09-07 · ba52758* · mutant killed · exit 1 · `internal/web/view/offer_detail.templ` · deleting the indicator leaves the spinner and the disabled binding reading a signal nothing creates — markup that looks like feedback and produces none · acceptance-sha256:697c99afe240ef86783d5c9229c442677aac829877565c68645b2d5213be5159 · covers:the rendered markup of the upload label
- 2026-09-07 · ba52758* · mutant killed · exit 1 · `internal/web/view/carts.templ` · an uppercase letter in the attribute NAME makes datastar create $_busy while every consumer still reads $_busy — the shape that shipped as a dead page in a sibling project · acceptance-sha256:697c99afe240ef86783d5c9229c442677aac829877565c68645b2d5213be5159 · covers:the lowercase-survival rule for indicator attribute names
- 2026-09-07 · ba52758* · mutant killed · exit 1 · `internal/web/view/carts.templ` · renaming the indicator to a signal nothing reads leaves an attribute that looks like feedback and produces none — three such attributes sat on a sibling project most-used screen · acceptance-sha256:697c99afe240ef86783d5c9229c442677aac829877565c68645b2d5213be5159 · covers:the has-a-consumer rule for indicator signals

## Invariants

- The upload keeps ADR-008's form contract: an enclosing `<form>` with `enctype="multipart/form-data"` and a `name` on the file input.
- No `<form>` appears anywhere outside the two uploads.
- The indicator signal stays underscore-prefixed, so it is never sent to the backend.
- The indicator signal stays all lowercase, so it survives HTML attribute-name lowercasing.

## Risks

- The tests assert rendered markup, not behaviour: they cannot see whether datastar actually hydrates. Mitigated only by the fact that the same pattern is in use at nine other sites in M's hands; the real remedy is the browser check in `docs/adr/BACKLOG.md`.
- `templ generate` must be run or the committed generated files drift from their sources. The acceptance fence runs it, so the drift cannot survive a passing run.
- ⚠ `templ generate keeping the committed generated files in step` is declared in **Rests-on:** and carries NO killed mutant, deliberately. The fence performs that step itself, so it cannot fail for that reason inside its own run: a codegen that silently no-opped would leave the tests reading stale generated files and passing against them, and nothing here would notice. That is stated rather than papered over with a mutant that would really be proving something else. The other three declared mechanisms each carry a killed mutant bound to the current acceptance digest.

## Stop Condition

Stop and ask if the busy state cannot be expressed without adding client-side
JavaScript, or if disabling the input during flight turns out to cancel the
in-flight request in any browser — either would mean the decision in ADR-015 is
wrong rather than the implementation.

## Out of Scope

- Byte-level upload progress — ADR-015 rejects it with a reason.
- Giving the nine existing `_busy` sites their own signals (deferred: docs/adr/BACKLOG.md)

## Verification Log

- 2026-09-07 · ba52758* · exit 2 · `set -o pipefail …` · acceptance-sha256:7c6f55063f5dea6b8f84262b3b2a3059287e0da087bfa0293be72ab48e6ce8d0 · ms:153
  ```
  --- last 1 line(s) of stderr
  go: no such tool "templ"
  ```
- 2026-09-07 · ba52758* · exit 0 · `set -o pipefail …` · acceptance-sha256:697c99afe240ef86783d5c9229c442677aac829877565c68645b2d5213be5159 · ms:2670
- 2026-09-07 · ba52758* · exit 0 · `set -o pipefail …` · acceptance-sha256:697c99afe240ef86783d5c9229c442677aac829877565c68645b2d5213be5159 · ms:2837
- 2026-09-07 · ba52758* · exit 0 · `set -o pipefail …` · acceptance-sha256:697c99afe240ef86783d5c9229c442677aac829877565c68645b2d5213be5159 · ms:2971
- 2026-09-07 · ba52758* · exit 0 · `set -o pipefail …` · acceptance-sha256:697c99afe240ef86783d5c9229c442677aac829877565c68645b2d5213be5159 · ms:2593
