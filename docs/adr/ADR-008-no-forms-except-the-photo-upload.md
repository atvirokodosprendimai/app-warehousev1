# ADR-008: Use no `<form>` elements except the photo upload

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-009, `internal/web/view/upload_contract_test.go`, `README.md`
**Governs:** `internal/web/view/*.templ`, `internal/web/view/upload_contract_test.go`
**Enforced-by:** `internal/web/view/upload_contract_test.go::TestNoFormsOutsideFileUpload`
**Invalidates:** none — checked
**Served-path change:** Every input in the application binds to a datastar signal and posts through a datastar action; the one exception is choosing a photograph, which is a real `<form>` because file bytes cannot ride in a signal.

## Context

Retrospective record — **and the one in this corpus written because the rule
itself caused a shipped defect.** It is worth stating the sequence plainly,
because the lesson is in the shape rather than in the fix.

The house style is "no `<form>` elements; inputs bind to signals". That rule is
correct and it has exactly one exception. The exception is the thing that got
forgotten, the upload markup was written form-free like everything else, and
photo upload did not work at all. Sixty-three green HTTP assertions did not see
it, because the smoke test built its own multipart body with `curl` — which
proves the *server* accepts an upload and says nothing about whether the *page*
can produce one.

**A test that constructs the request cannot test the code whose job is
constructing it.**

datastar v1.0.2 imposes three requirements on form-encoded markup. Read from the
pinned bundle, verbatim:

    else if (u === "form") {
        let T = l ? document.querySelector(l) : r.closest("form");
        if (!T) throw i("FetchFormNotFound", {action: D, selector: l});
        let A = T.getAttribute("enctype") === "multipart/form-data";
        A || (q["Content-Type"] = "application/x-www-form-urlencoded");
        let X = new URLSearchParams(F);
        if (ot(t)) A ? Y.body = F : Y.body = X;
    }

Each requirement alone is fatal, and each fails **differently**:

| Missing | What happens |
|---|---|
| the enclosing `<form>` | `FetchFormNotFound` is thrown; no request is sent; the control looks dead |
| `enctype="multipart/form-data"` | the body is sent urlencoded, which cannot carry bytes |
| `name` on the file input | `new FormData(form)` collects only *named* controls, so the file is simply absent |

All three shipped at once, and every server-side check stayed green.

## Existing Primitives Audit

- datastar signals and actions — **reused** for every other input. The rule is
  not new; this record writes down its exception and the guard on it.
- `templ` components — **reused**. The check reads rendered component output, so
  it tests what the browser receives rather than the source.
- There was no prior markup contract test; that is what the defect produced.

## Decision

Templates contain no `<form>` element except the two photo uploads (offer and
submission). Those two are real forms with `enctype="multipart/form-data"` and a
`name` on the file input, and they carry **no** `data-bind` on that input —
binding a file control to a signal is what produced the form-free version.

`upload_contract_test.go` renders the components and asserts the markup:

- `TestOfferPhotoUploadSatisfiesDatastarsFormContract` and its submission twin
  find every form-encoded action, walk to its nearest enclosing `<form>`, and
  assert the enctype and the named file input.
- `TestNoFormsOutsideFileUpload` asserts the ban itself, so the exception cannot
  quietly spread.
- The helper **fails loudly when it finds no form-encoded action at all**, rather
  than passing vacuously — a check that asserts nothing when its subject moves is
  the failure mode this whole record exists to remember.

What would FAIL this decision: any of those tests going red. The guards were
verified by breaking them — three mutations (remove the form, remove the enctype,
remove the name) each produced a distinct, correct failure message.

## Alternatives Considered

- **Use a form everywhere, as ordinary HTML.** Rejected: it fights datastar's
  model, in which the server owns page state and the browser reports events. Two
  submission mechanisms in one application is the ambiguity that caused this bug
  in the first place.
- **Upload via base64 in a signal.** Rejected: it inflates the payload by a third,
  and every unprefixed signal is sent on *every* action, so a pending image would
  ride along with unrelated requests.
- **A browser test (Playwright or similar).** Not rejected on merit — it is the
  right tool and it is genuinely absent. Rejected *for now* as disproportionate
  to one repository with one developer; the markup assertion buys most of the
  value at a fraction of the cost. The gap is recorded in `docs/adr/BACKLOG.md`.
- **Trust the rule and the code review.** Rejected by evidence: that is exactly
  what was in place when all three requirements were violated at once.

## Component / Boundary Impact

`internal/web/view` owns the markup and the contract test. No other package can
violate this rule, because no other package emits HTML. One reason to change: how
this application sends data from the browser.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `<form enctype="multipart/form-data">` around the photo inputs | the single exception | `internal/web/view` | datastar client, `POST /offers/{id}/photos` |
| `name="photos"` on the file input | required by `FormData` | `internal/web/view` | the upload handler's `r.MultipartForm` |
| `TestNoFormsOutsideFileUpload` | new guard on the ban | `internal/web/view` | anyone editing a template |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `internal/web/view/offer_detail.templ`, its
submission equivalent, and `upload_contract_test.go`. No tasks directory: this is
a retrospective record.

## Consequences

- **Positive:** the client-side contract is now asserted in Go, on every run, with
  the reason quoted from the bundle beside the assertion.
- **Positive:** the ban and its exception are both checked, so the exception
  cannot spread by copy-paste.
- **Negative:** the test reads rendered HTML with string search, so it is coupled
  to the markup's shape and will need updating when the components change. That
  is the cost of reaching a layer Go otherwise cannot see.
- **Negative:** it pins datastar v1.0.2's behaviour. A datastar upgrade must
  re-read the bundle rather than trusting this quote.
- **Neutral:** it still cannot see layout, focus order, contrast or whether
  datastar hydrates at all. Those need a browser.

## Out of Scope

- Any browser-driven end-to-end test (permanent: fact: it exists — two Playwright walks drive the real binary against a fresh database and run on every push; citation: file `scripts/browser.sh:53`)
- Drag-and-drop or paste-to-upload (deferred: docs/adr/BACKLOG.md)
- Client-side image resizing before upload (permanent: boundary: the server already refuses what a marketplace cannot render, and doing it twice adds a second place to be wrong)
- Asserting datastar's behaviour beyond the form branch (permanent: fact: the three requirements are read verbatim from the pinned bundle and quoted beside the assertion; citation: file `internal/web/view/upload_contract_test.go:19`)
- Tracking datastar client releases automatically (permanent: fact: the Go SDK is pinned and upgrades are deliberate; citation: version `github.com/starfederation/datastar-go@v1.2.2`)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A datastar upgrade changes the form branch | Med | High | The bundle text is quoted in the test; an upgrade must re-read it. Not mechanically detected |
| The markup is restructured and the string search silently matches nothing | Med | High | The helper fails when it finds no form-encoded action at all, rather than passing |
| Someone adds a `<form>` for convenience | Med | Med | `TestNoFormsOutsideFileUpload` refuses it |
| A second file upload is added without the enctype | Med | High | The helper walks *every* form-encoded action, not a named one |

## Rollback

None needed — this is a markup rule with a test, not a mechanism. Removing the
tests would restore the state in which the defect shipped.

## Follow-ups

- [x] A browser-driven check is the real remedy for the class of defect this record describes. **Built 2026-09-07**: `scripts/browser/` holds two Playwright walks and `scripts/browser.sh` runs them against the real binary with a fresh database each; the `browser` job in `.github/workflows/checks.yml` runs them on every push and keeps the screenshots as an artifact. ⚠ A fresh database PER WALK is load-bearing rather than hygiene — each walk bootstraps the first administrator, and ADR-006 closes that route for ever once an account exists.
