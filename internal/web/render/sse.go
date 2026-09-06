// Package render opens datastar SSE streams with the headers and deadlines a
// long-lived stream needs.
//
// Nothing in this application calls datastar.NewSSE directly. Two settings have
// to be right before the first byte, both of them invisible when they are wrong:
// the response headers must be primed before any compression middleware sees the
// body, and the write deadline must be cleared or the connection dies partway
// through a session with no error anywhere. Wrapping both in one constructor is
// what stops each new handler having to remember them.
package render

import (
	"net/http"
	"time"

	"github.com/starfederation/datastar-go/datastar"
)

// PrimeSSE writes the headers a stream needs and flushes them.
//
// Order is the whole point. A compression middleware decides how to treat a
// response from the headers it sees when the body starts, and datastar's own
// framing is line-oriented — so a compressor that buffers across event
// boundaries produces a body the client silently fails to parse. The symptom is
// nasty precisely because the server looks healthy: the handler runs, the
// database write lands, and the browser simply applies no patches.
func PrimeSSE(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Nginx and several other proxies buffer a response by default, which turns a
	// stream into a single delivery at the end. This header is the documented way
	// to opt out and is ignored by proxies that do not buffer.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// NewSSE opens a datastar stream on w.
//
// It clears the write deadline first. http.Server.WriteTimeout applies to the
// whole response, and an SSE response is meant to last as long as the page is
// open, so any non-zero server write timeout kills a healthy stream at that mark
// — with no error in the handler and nothing in the log. The datastar SDK
// flushes through a ResponseController but never touches deadlines, so clearing
// it is the caller's job.
//
// ⚠ Read any request signals BEFORE calling this. Priming the response consumes
// the point at which the request body can still be read, and datastar.ReadSignals
// afterwards will not see it.
func NewSSE(w http.ResponseWriter, r *http.Request) *datastar.ServerSentEventGenerator {
	rc := http.NewResponseController(w)
	// A zero time means "no deadline". The error is deliberately ignored: not
	// every ResponseWriter supports deadlines (an httptest recorder does not),
	// and a stream that cannot set one is still a working stream.
	_ = rc.SetWriteDeadline(time.Time{})

	PrimeSSE(w)
	return datastar.NewSSE(w, r)
}

// Heartbeat is how often an idle stream sends something.
//
// An idle TCP connection through an intermediary is closed without either end
// being told, so a dashboard nobody touches would silently stop updating and
// look identical to one where nothing has changed. A periodic no-op keeps the
// path warm and gives the server a point at which to notice ctx.Done().
const Heartbeat = 25 * time.Second
