package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// slogFormatter adapts chi's own request logger to this application's slog
// logger.
//
// It is chi's built-in middleware — middleware.RequestLogger — with a formatter
// supplied, rather than the bare middleware.Logger. The bare one writes through
// the STDLIB log package in a coloured human-readable format, which would mean
// request lines arriving on a different stream, in a different shape, with no
// level control and no way to correlate them with the request_id that every
// other log line in this application already carries. Swapping to it is a
// one-line change if the coloured output is ever preferred.
//
// ⚠ SUCCESSES ARE LOGGED AT INFO, DELIBERATELY. A sibling project mounted a
// request logger that emitted 2xx at DEBUG, and since the default level is Info
// a normal run showed nothing at all — the middleware was mounted, working, and
// completely invisible, which is indistinguishable from not having one. Seeing
// ordinary traffic is the entire reason this exists, so ordinary traffic is
// logged at the level a default run prints.
type slogFormatter struct{ log *slog.Logger }

// NewLogEntry starts a log entry for one request.
func (f slogFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	return &slogEntry{log: f.log, r: r}
}

// slogEntry is one request's pending log line.
type slogEntry struct {
	log *slog.Logger
	r   *http.Request
}

// Write emits the line once the response is finished.
//
// ⚠ "Finished" is doing real work in that sentence: a long-lived response logs
// only when it ENDS. An SSE stream lasts as long as the tab is open, so its line
// appears on disconnect rather than on connect — which is why GetStream logs its
// own opening separately. Without that, the single longest-lived request on
// every page would be the one piece of traffic you could not see arriving.
func (e *slogEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ any) {
	attrs := []any{
		"method", e.r.Method,
		"path", e.r.URL.Path,
		"status", status,
		"bytes", bytes,
		"elapsed", elapsed.Round(time.Microsecond).String(),
		"ip", e.r.RemoteAddr,
		"request_id", middleware.GetReqID(e.r.Context()),
	}

	// Severity follows the status, so a failing run is greppable by level rather
	// than by reading every line.
	switch {
	case status >= 500:
		e.log.Error("request failed", attrs...)
	case status >= 400:
		e.log.Warn("request rejected", attrs...)
	default:
		e.log.Info("request", attrs...)
	}
}

// Panic records a panic before chi's Recoverer turns it into a 500.
//
// The stack is attached here rather than left to Recoverer's stdout dump, so it
// lands in the same structured stream as everything else and carries the
// request_id that identifies which request produced it.
func (e *slogEntry) Panic(v any, stack []byte) {
	e.log.Error("request panicked",
		"method", e.r.Method,
		"path", e.r.URL.Path,
		"panic", v,
		"request_id", middleware.GetReqID(e.r.Context()),
		"stack", string(stack),
	)
}
