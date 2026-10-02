package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

func TestRejectionDetailReadsThePanelsOwnSentence(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "error response",
			body: `{"detail":"Service does not have any active hosts"}`,
			want: "Service does not have any active hosts",
		},
		{
			name: "newlines collapse onto one line",
			body: "{\"detail\":\"first line\\nsecond line\"}",
			want: "first line second line",
		},
		{
			name: "structured detail",
			body: `{"detail":{"field":"service_id"}}`,
			want: `{"field":"service_id"}`,
		},
		{
			name: "not a detail payload",
			body: `plain text failure`,
			want: "plain text failure",
		},
		{name: "empty", body: "", want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := rejectionDetail([]byte(testCase.body)); got != testCase.want {
				t.Fatalf("rejectionDetail() = %q, want %q", got, testCase.want)
			}
		})
	}
}

// The log line is read, copied and pasted around, so it must name the caller
// without carrying the credential they used.
func TestAPIRejectionActorNeverCarriesTheCredential(t *testing.T) {
	const secret = "super-secret-api-key"
	holder := &apiActorHolder{}
	request := httptest.NewRequest(http.MethodPost, "/api/user", nil)
	request.Header.Set("Authorization", "Bearer "+secret)

	actor := apiRejectionActor(request, holder)
	if strings.Contains(actor, secret) {
		t.Fatalf("the actor carried the token: %q", actor)
	}
	if actor == "" {
		t.Fatal("an unauthenticated caller should still be described")
	}
}

func TestShouldLogAPIRejectionSkipsNoiseAndWebsockets(t *testing.T) {
	cases := []struct {
		path    string
		upgrade bool
		want    bool
	}{
		{path: "/api/user", want: true},
		{path: "/api/core/cdn-automation", want: true},
		// Every user's client hits these; their refusals are noise.
		{path: "/api/sub/abc123", want: false},
		{path: "/api/v1/client/subscribe/abc", want: false},
		// Wrapping a hijacked connection would break it.
		{path: "/api/core/logs", upgrade: true, want: false},
		// Not the API at all.
		{path: "/dashboard", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			if testCase.upgrade {
				request.Header.Set("Upgrade", "websocket")
			}
			if got := shouldLogAPIRejection(request); got != testCase.want {
				t.Fatalf("shouldLogAPIRejection(%q) = %v, want %v", testCase.path, got, testCase.want)
			}
		})
	}
}

// The wrapper sits in front of every API response, so a successful one must
// come through byte for byte, headers and status included.
func TestAPIRejectionLogDoesNotDisturbSuccessfulResponses(t *testing.T) {
	handler := withAPIRejectionLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"username":"alice"}`))
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/user", nil))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", recorder.Code)
	}
	if got := recorder.Body.String(); got != `{"username":"alice"}` {
		t.Fatalf("body = %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestAPIRejectionLogPassesRefusalsThroughUnchanged(t *testing.T) {
	handler := withAPIRejectionLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusBadRequest, "Service does not have any active hosts")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/user", nil))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "active hosts") {
		t.Fatalf("the caller must still receive the reason: %q", recorder.Body.String())
	}
}

// A body larger than the capture limit must still reach the caller whole; only
// what is kept for the log line is bounded.
func TestAPIRejectionLogDoesNotTruncateTheResponse(t *testing.T) {
	large := strings.Repeat("x", maxRejectionDetailCapture*3)
	handler := withAPIRejectionLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(large))
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/user", nil))

	if recorder.Body.Len() != len(large) {
		t.Fatalf("body length = %d, want %d", recorder.Body.Len(), len(large))
	}
}

// A handler that writes without calling WriteHeader has answered 200, and must
// not be logged as a refusal.
func TestAPIRejectionLogTreatsImplicitStatusAsSuccess(t *testing.T) {
	recorder := &apiRejectionRecorder{ResponseWriter: httptest.NewRecorder()}
	if _, err := recorder.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if recorder.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.status)
	}
	if recorder.capture {
		t.Fatal("a successful response should not be captured")
	}
}

// The bug this guards against: the auth middleware calls the handler with
// r.WithContext(...), so the request this middleware holds never gains the
// principal. Reading the context here named every authenticated caller
// "unauthenticated", which sent the reader hunting an auth problem that was not
// there.
func TestAPIRejectionLogNamesTheCallerAcrossContextDerivation(t *testing.T) {
	var logged string
	handler := withAPIRejectionLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stand in for requireAdmin: authenticate, record, then hand the
		// handler a derived request.
		principal := adminPrincipal{ID: 7, Username: "ddbot", Role: "full_access"}
		recordAPIActor(r, principal)
		derived := r.WithContext(context.WithValue(r.Context(), adminContextKey, principal))
		holder, _ := derived.Context().Value(apiActorContextKey).(*apiActorHolder)
		logged = apiRejectionActor(derived, holder)
		writeError(w, http.StatusBadGateway, "something broke")
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/user", nil)
	request.Header.Set("Authorization", "Bearer some-api-key")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if logged != "ddbot (api key)" {
		t.Fatalf("actor = %q, want %q", logged, "ddbot (api key)")
	}
}

func TestAPIRejectionActorFallsBackWhenAuthNeverSucceeded(t *testing.T) {
	holder := &apiActorHolder{}
	request := httptest.NewRequest(http.MethodPost, "/api/user", nil)
	if got := apiRejectionActor(request, holder); got != "unauthenticated" {
		t.Fatalf("actor = %q", got)
	}
	request.Header.Set("Authorization", "Bearer rejected-key")
	if got := apiRejectionActor(request, holder); got != "unauthenticated (bearer token rejected)" {
		t.Fatalf("actor = %q", got)
	}
}

func TestRecordAPIActorDistinguishesDashboardFromAPIKey(t *testing.T) {
	holder := &apiActorHolder{}
	request := httptest.NewRequest(http.MethodGet, "/api/user", nil).
		WithContext(context.WithValue(context.Background(), apiActorContextKey, holder))

	session := adminPrincipal{Username: "pouria"}
	session.Context.Source = adminapp.AuthSourceSession
	recordAPIActor(request, session)
	if got := holder.get(); got != "pouria (dashboard)" {
		t.Fatalf("actor = %q", got)
	}
}
