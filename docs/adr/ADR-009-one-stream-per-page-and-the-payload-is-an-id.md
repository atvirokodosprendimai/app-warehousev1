# ADR-009: Open one SSE stream per page, and put only an id on the wire

**Status:** Accepted
**Date:** 2026-09-06
**Owner:** M
**Spec:** None — no spec stage
**Cross-references:** ADR-008, `internal/web/render/sse.go`
**Governs:** `internal/web/render/*.go`
**Enforced-by:** None — no mechanical check reaches this. The rule is carried by the `internal/web/render` package comment and by review; the reasons are given under Risks.
**Invalidates:** none — checked
**Served-path change:** A submission arriving in the inbox makes an unread-count banner appear at the top of every open page, on a phone and on a desktop, with no reload and no polling.

## Context

Retrospective record. The requirement was that the administrator's interface be
realtime: *"once a message arrives it will see a block at the page top, on mobile
and desktop, that it has N new unread messages"*.

Two things about SSE in this stack are invisible when they are wrong, and both
were found the expensive way:

1. **Response headers must be primed before any compression middleware sees the
   body.** A compressor that buffers across event boundaries produces a body the
   client silently fails to parse. The symptom is nasty precisely because the
   server looks healthy — the handler runs, the database write lands, and the
   browser applies no patches.
2. **The write deadline must be cleared.** `http.Server.WriteTimeout` applies to
   the whole response, and an SSE response is meant to last as long as the page is
   open, so any non-zero server write timeout kills a healthy stream at that mark
   with no error in the handler and nothing in the log.

## Existing Primitives Audit

- `starfederation/datastar-go` — **reused** for the SSE framing and patch
  protocol. It flushes through a `ResponseController` but never touches
  deadlines, so clearing the deadline is the caller's job.
- `net/http`'s `ResponseController` — **reused** to clear the deadline.
- `internal/web/render` is **new** and exists only to make the two settings above
  impossible to forget.

## Decision

**Nothing calls `datastar.NewSSE` directly.** `render.NewSSE` clears the write
deadline, primes the headers (`text/event-stream`, `no-cache`, `keep-alive`,
`X-Accel-Buffering: no`) and flushes, then opens the stream. Wrapping both
settings in one constructor is what stops each new handler having to remember
them.

**One connection per page.** Every page opens `/stream`; the query string says
what that page needs *beyond* what all pages need (`?offer=<id>`). A widget does
not get its own stream — browsers cap concurrent connections per origin, and a
page with four widgets would spend that budget on itself.

**The wire payload is always an id, never rendered state.** A subscriber
receiving an id re-reads the current row and re-renders it. That makes two events
racing for the same id idempotent: both produce the truth as it is now, rather
than one overwriting the other with a stale snapshot. It also means a patch can
never disclose a field the recipient's page would not otherwise show, because the
recipient renders it themselves.

An idle stream sends a heartbeat every 25 seconds, because an idle TCP connection
through an intermediary is closed without either end being told — a dashboard
nobody touches would silently stop updating and look identical to one where
nothing has changed.

There is **no mechanical check** on any of this, which is stated in
`Enforced-by:` rather than papered over with a check that cannot fail. What would
falsify the decision is a stream that dies at the server's write timeout or a
patch the browser ignores; both are visible only in a browser, and this
repository has no browser test.

## Alternatives Considered

- **Polling.** Rejected: an inbox banner that appears within a poll interval is
  not what was asked for, and a poll that is fast enough to feel realtime costs
  more than a held connection.
- **WebSockets.** Rejected: the traffic is one-directional (server to browser) and
  the browser already reports events over ordinary requests. A duplex protocol
  buys nothing and adds a second framing to get wrong.
- **One stream per widget.** Rejected on the per-origin connection cap; a page
  with several live regions would exhaust it and the last widget would silently
  never connect.
- **Send rendered HTML on the wire instead of an id.** Rejected: two events for
  one id can then land out of order and the loser overwrites current state with
  stale bytes. Sending an id makes ordering irrelevant.
- **Set `WriteTimeout` and live with reconnects.** Rejected: the reconnect is
  invisible and the gap is not — an event that arrives during it is simply lost.

## Component / Boundary Impact

`internal/web/render` owns stream construction and the heartbeat interval. Every
handler that streams uses it and none constructs a generator itself. One reason to
change: how this application pushes to a browser.

## Wiring & Contract Changes

| Surface | Change | Producer | Consumer(s) |
|---------|--------|----------|-------------|
| `GET /stream[?offer=<id>]` | new; one per page | `internal/web` | the datastar client on every page |
| `render.NewSSE(w, r)` | new; the only way to open a stream | `internal/web/render` | every streaming handler |
| `render.PrimeSSE(w)` | new; header ordering | `internal/web/render` | `NewSSE` |
| `render.Heartbeat` | new; 25s idle keepalive | `internal/web/render` | every streaming handler |

## Inter-task Contracts

None.

## Implementation

Already implemented, on `main`, in `internal/web/render/sse.go`. No tasks
directory: this is a retrospective record.

## Consequences

- **Positive:** the two invisible settings are set in one place and cannot be
  forgotten by a new handler.
- **Positive:** an id-only payload makes concurrent updates idempotent and keeps
  authorisation decisions on the rendering side.
- **Negative:** every subscriber re-reads on every event, so a broadcast is N
  reads. At this scale that is cheaper than the correctness it buys.
- **Negative:** none of it is mechanically checked. This record says so in
  `Enforced-by:` rather than naming a check that cannot fail.
- **Neutral:** signals must be read from the request *before* `NewSSE` is called,
  because priming the response consumes the point at which the body can still be
  read. That is documented on the function.

## Out of Scope

- A browser test that a patch is actually applied (deferred: docs/adr/BACKLOG.md)
- Reconnection with event replay (permanent: boundary: an id-only payload means a reconnecting client re-reads current state anyway, which is what replay would be for)
- Streaming to anonymous visitors (permanent: boundary: the only unauthenticated route is the photo capability — ADR-007)
- Per-widget streams (permanent: fact: browsers cap concurrent connections per origin, so a page's widgets would compete for that budget; citation: url https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events)

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| A `WriteTimeout` is added to the server and kills every stream | Med | High | `render.NewSSE` clears the deadline per response; the package comment says why. No test — it needs a real server and a real clock |
| Compression middleware is reordered ahead of the priming | Low | High | `PrimeSSE` flushes headers before the body; ordering is in `cmd/warehouse` and is not asserted anywhere |
| A handler calls `datastar.NewSSE` directly | Med | High | The package comment forbids it; nothing enforces it |
| An intermediary closes an idle stream | Med | Med | 25-second heartbeat |

## Rollback

Reverting to polling would remove `internal/web/render` and change the client
markup. Nothing persistent depends on the streaming design, so it is a pure code
change.

## Follow-ups

- [ ] A check that no handler calls `datastar.NewSSE` directly would be cheap — a source grep in a test — and would close the third risk. Not built.
