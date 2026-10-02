package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	adminapp "github.com/aliking1367/next/internal/app/admin"
	"github.com/aliking1367/next/internal/app/logging"
)

// When an integration the panel owner cannot see -- a reseller bot bought as a
// service, a script somebody else runs -- calls the API and the panel refuses,
// the owner is left with "it did not work" and no way to find out why. The
// panel knew the reason exactly and threw it away.
//
// So every refused API call is written to the panel log with who made it, what
// they asked for, and the reason the panel gave back. That is the whole
// diagnosis, available without reaching the other side.

// apiRejectionRecorder captures the status and the response body so the reason
// can be logged after the handler has answered. Only the first few hundred
// bytes are kept: the reason is a sentence, and a body can be a whole backup.
type apiRejectionRecorder struct {
	http.ResponseWriter
	status  int
	body    bytes.Buffer
	capture bool
}

const maxRejectionDetailCapture = 2048

func (w *apiRejectionRecorder) WriteHeader(status int) {
	w.status = status
	w.capture = status >= http.StatusBadRequest
	w.ResponseWriter.WriteHeader(status)
}

func (w *apiRejectionRecorder) Write(data []byte) (int, error) {
	if w.status == 0 {
		// A handler that writes without WriteHeader has answered 200.
		w.status = http.StatusOK
	}
	if w.capture && w.body.Len() < maxRejectionDetailCapture {
		remaining := maxRejectionDetailCapture - w.body.Len()
		if remaining > len(data) {
			remaining = len(data)
		}
		w.body.Write(data[:remaining])
	}
	return w.ResponseWriter.Write(data)
}

// Flush and Unwrap keep the streaming endpoints working. The log viewer and the
// metrics feed write progressively, and a wrapper that swallows Flush would
// leave them buffered until the connection closed.
func (w *apiRejectionRecorder) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *apiRejectionRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func withAPIRejectionLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldLogAPIRejection(r) {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &apiRejectionRecorder{ResponseWriter: w}
		holder := &apiActorHolder{}
		request := r.WithContext(context.WithValue(r.Context(), apiActorContextKey, holder))
		next.ServeHTTP(recorder, request)
		if recorder.status < http.StatusBadRequest {
			return
		}
		logging.Warnf(logging.ComponentAdmin,
			"api refused %s %s status=%d actor=%s reason=%q",
			r.Method, r.URL.Path, recorder.status,
			apiRejectionActor(r, holder), rejectionDetail(recorder.body.Bytes()))
	})
}

// shouldLogAPIRejection keeps the log about calls that were meant to change or
// read something and were refused. A websocket upgrade hijacks the connection,
// so wrapping its writer would break it, and the subscription routes are hit by
// every user's client -- their refusals are noise, not diagnosis.
func shouldLogAPIRejection(r *http.Request) bool {
	if websocketUpgradeRequested(r) {
		return false
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/api/sub"),
		strings.HasPrefix(path, "/api/v1/client/"),
		strings.HasPrefix(path, phpMyAdminEmbedPath),
		strings.HasPrefix(path, "/api/node/install-"):
		return false
	}
	return strings.HasPrefix(path, "/api/")
}

func websocketUpgradeRequested(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

// apiActorHolder carries the caller's identity back out to this middleware.
//
// It cannot be read from the request context: the auth middleware authenticates
// and then calls the handler with r.WithContext(...), a *derived* request, so
// the one this middleware holds never gains the principal. Reading the context
// here reported every authenticated call as unauthenticated, which is worse
// than logging nothing -- it sent the reader looking for an auth problem that
// was not there. A pointer placed in the context before the chain runs is
// filled in by whichever guard authenticates, and survives the derivation.
type apiActorHolder struct {
	mu   sync.Mutex
	name string
}

func (h *apiActorHolder) set(name string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.name = name
	h.mu.Unlock()
}

func (h *apiActorHolder) get() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.name
}

// recordAPIActor is called by the auth guards once they know who is calling.
func recordAPIActor(r *http.Request, principal adminPrincipal) {
	holder, _ := r.Context().Value(apiActorContextKey).(*apiActorHolder)
	if holder == nil || principal.Username == "" {
		return
	}
	if principal.Context.Source == adminapp.AuthSourceSession {
		holder.set(principal.Username + " (dashboard)")
		return
	}
	holder.set(principal.Username + " (api key)")
}

// apiRejectionActor names the caller without revealing the credential it used.
// An API key is identified by its owner, never by the key itself: this line
// goes to a log file that is read, copied and pasted into chats.
func apiRejectionActor(r *http.Request, holder *apiActorHolder) string {
	if name := holder.get(); name != "" {
		return name
	}
	// Refused before authentication succeeded, so the caller is not known.
	if bearerToken(r) != "" {
		return "unauthenticated (bearer token rejected)"
	}
	return "unauthenticated"
}

// rejectionDetail pulls the sentence the panel sent back. Error responses are
// {"detail": "..."}; anything else is reported by length so the line stays one
// line.
func rejectionDetail(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}
	var payload struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(trimmed, &payload); err == nil && len(payload.Detail) > 0 {
		var text string
		if err := json.Unmarshal(payload.Detail, &text); err == nil {
			return collapseRejectionWhitespace(text)
		}
		return collapseRejectionWhitespace(string(payload.Detail))
	}
	return collapseRejectionWhitespace(string(trimmed))
}

func collapseRejectionWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
