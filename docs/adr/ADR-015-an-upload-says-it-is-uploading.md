# ADR-015: Make the photo upload say that it is uploading

**Status:** Accepted
**Date:** 2026-09-07
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-008, ADR-009, `internal/web/view/offer_detail.templ`
**Governs:** `internal/web/view/offer_detail.templ`, `internal/web/view/inbox.templ`, `internal/web/view/upload_contract_test.go`
**Enforced-by:** `internal/web/view/upload_contract_test.go::TestPhotoUploadShowsABusyState`
**Invalidates:** none — checked
**Served-path change:** Choosing a photograph now disables the control and swaps its label for a spinner and "Uploading…" until the server answers, so a phone on a slow connection shows that something is happening instead of looking inert.

## Context

Reported by M on 2026-09-07: *"upload photos need some kind of loading indicator
because with slow mobile i have no idea its doing something or not"*.

The upload is the one control in this application with **no busy state at all**.
Nine other places already carry the house pattern — `data-indicator:_busy`,
`data-attr:disabled="$_busy"`, and a `.spinner` span shown by `data-show` — and
the upload has none of it. Grepped 2026-09-07: `data-indicator` appears at nine
sites in `internal/web/view/*.templ`, and the `<form>` in `OfferPhotosCard` is
not one of them.

It is also the control that most needs one. A photograph off a phone camera is
several megabytes on a mobile uplink, so this is the longest request the
application makes, and it is fired by an `<input type="file">` whose only visible
affordance is a 104px dashed square reading "＋ Add". Nothing about that square
changes when a request is in flight. The failure mode M describes is the
predictable one: the user cannot tell a slow upload from a dead control, so they
tap again.

⚠ **The house pattern is currently correct by accident, and this record is where
that gets written down.** `data-indicator:_busy` puts the signal name in the
*attribute name*, and the HTML parser lowercases attribute names. `_busy` is
already lowercase, so datastar creates `_busy` and the consumers reading `$_busy`
find it. A camelCase name would not survive: datastar would create
`_uploadbusy` while every consumer read `$_uploadBusy`, and they are different
signals. That exact defect took a sibling project's admin page down in production
on 2026-08-31 — every control on it rendered `disabled` and stayed that way,
because `data-attr:disabled` on an undefined signal sets the attribute, and
`disabled` is a boolean attribute where presence alone disables.

## Existing Primitives Audit

- **`data-indicator`** (datastar v1.0.2) — **reused**. It maintains a signal that
  is true while a fetch is in flight and false otherwise, so there is no reason
  for a hand-rolled `$busy = true` before and `false` after — which is also
  wrong, because it cannot see a request that fails or is aborted.
- **The `.spinner` class** (`internal/web/assets/app.css`) — **reused** unchanged.
- **The house busy markup** — **reused** verbatim from the nine existing sites,
  including `style="display:none"` on the spinner span so it is hidden before
  hydration rather than flashing on every page load.
- **`upload_contract_test.go`** (ADR-008) — **reshaped**: it already exists to
  pin the half of the upload that only the client can see, which is exactly where
  this belongs.

## Decision

The file input carries `data-indicator:_uploading` and
`data-attr:disabled="$_uploading"`. Its label swaps contents on that signal:
`＋ Add` when idle, a `.spinner` and "Uploading…" when in flight. The busy text
is an `aria-live="polite"` region so it is announced rather than only seen.

The signal is **`_uploading`**, and both properties of that name are deliberate:

- **All lowercase**, so it survives the attribute-name lowercasing described
  above. This is now enforced rather than left to luck.
- **Underscore-prefixed**, so it stays in the browser. Every unprefixed signal is
  sent to the backend on *every* action, and a spinner flag has no business
  riding along with an export request.

It is **not** `_busy`. Signals in datastar are global and flattened, so reusing
`_busy` would spin the spinner on "Save details" and "Mark sold" at the same
time. (Those nine sites already share `_busy` with each other and therefore
already do this to one another; that is a pre-existing wart this record does not
widen and does not fix.)

Two invariants are enforced by test, because both are failure modes that leave
the markup looking right:

1. **An indicator's signal name must be all lowercase** — otherwise the signal
   datastar creates is not the signal the consumers read.
2. **Every indicator signal must have a consumer** — a `data-show` or `data-attr`
   that reads it. An indicator wired to nothing renders no feedback at all and
   nothing reports it. In the sibling project above, the most-used screen in the
   product carried three indicator attributes with zero consumers.

What would FAIL this decision: `TestPhotoUploadShowsABusyState` finding no
indicator on the upload input, no consumer for it, or no disabled binding; or the
two invariant tests finding an uppercase indicator name or an unconsumed signal
anywhere in the view package.

## Alternatives Considered

- **A real progress bar showing bytes uploaded.** Rejected, and this is the
  alternative that most deserves the explanation, because it is what M actually
  wants on a slow link. datastar's fetch exposes no upload-progress hook, so it
  would mean hand-rolled `XMLHttpRequest` with `upload.onprogress` and manual
  signal writes — client-side state and a second request path, against the whole
  premise of ADR-008 and ADR-009. A busy state is what is achievable without
  leaving the model; it answers "is it doing something", not "how far".
- **Reuse `_busy`.** Rejected: signals are global, so every other spinner on the
  page would spin during an upload.
- **A hand-rolled `$uploading` set true on change and false on the response.**
  Rejected: it cannot observe a request that fails, times out or is aborted, so
  the spinner would run for ever on exactly the slow connection this is for.
- **Disable the control only, with no spinner.** Rejected on evidence from the
  sibling project: a control that dims and stays dim reads as *broken*, not as
  busy, so the person taps it again anyway.
- **Put the busy state outside the patched card.** Considered, because the
  response replaces `#offer-photos` wholesale. Rejected as unnecessary here: the
  card is replaced at the moment the request completes, which is the moment the
  indicator would go false regardless. The outside-the-target rule applies to a
  status bar that must survive the response, not to an in-flight spinner.

## Component / Boundary Impact

`internal/web/view` owns the markup and both invariant tests. No handler changes
and no new route: `data-indicator` is entirely a client-side concern that the
server neither sets nor reads. One reason to change: how this application shows
that it is working.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `_uploading` signal | new, browser-only (underscore-prefixed) | `data-indicator` on the upload input | the label's spinner and text, the input's disabled binding |
| `OfferPhotosCard` upload label | busy/idle contents | `internal/web/view` | the operator |
| Submission upload label | same treatment | `internal/web/view` | the submitter |

## Inter-task Contracts

None — one task.

## Implementation

One task; see `tasks/README.md`.

## Consequences

- **Positive:** the longest request the application makes now says so, on the
  device where that matters most.
- **Positive:** the control is disabled while in flight, so a second tap cannot
  start a duplicate upload.
- **Positive:** the lowercase-name and has-a-consumer rules are now checked for
  *every* indicator in the package, not just this one — closing a trap that has
  already caused a production incident elsewhere.
- **Negative:** it is a busy state, not progress. On a genuinely slow uplink the
  user learns that something is happening and not how long it will take.
- **Negative:** the invariant tests read rendered HTML with string matching, so
  they are coupled to markup shape and will need updating when it changes. That
  is the cost of reaching a layer Go otherwise cannot see (ADR-008).
- **Neutral:** nothing about the request, the handler or the stored data changes.

## Out of Scope

- Byte-level upload progress (permanent: fact: datastar v1.0.2's fetch exposes no upload-progress hook, so this needs hand-rolled XHR outside the model ADR-008 establishes; citation: version `github.com/starfederation/datastar-go@v1.2.2`)
- Giving the nine existing `_busy` sites their own signals so they stop spinning together (deferred: docs/adr/BACKLOG.md)
- Client-side image resizing before upload (permanent: boundary: the server already refuses what a marketplace cannot render, and doing it twice adds a second place to be wrong)
- Retrying a failed upload automatically (permanent: boundary: the operator chose the file and is the one who should decide to try again; a silent retry on a metered mobile connection is a cost they did not agree to)
- Verifying any of this in a real browser (deferred: docs/adr/BACKLOG.md)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| An indicator name is later written camelCase and silently creates a different signal | **Med** | High | `TestEveryIndicatorNameSurvivesHTMLLowercasing` refuses it — this is the production incident being designed out |
| An indicator is added with nothing consuming it | Med | Med | `TestEveryIndicatorSignalHasAConsumer` refuses it |
| `$_uploading` is undefined at first paint and `data-attr:disabled` renders `disabled` | Low | High | `data-indicator` on the same element creates the signal at hydration; the nine existing sites have run this way in M's hands without a dead control. Not browser-verified here — see Out of Scope |
| The spinner flashes on every page load before hydration | Low | Low | `style="display:none"` on the span, matching the nine existing sites |
| The card is replaced mid-flight and the signal sticks true | Low | Med | The card is replaced *by* the response, i.e. after the request completes; datastar clears the signal on completion rather than on element removal |

## Rollback

Remove the four attributes and the two spans. No schema, no route, no handler and
no stored data is involved, so rollback is deleting markup.

## Follow-ups

None — nothing was left open when this record was written.
