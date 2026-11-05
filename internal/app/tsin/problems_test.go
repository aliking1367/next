package tsin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClassifyNamesWhereTheProblemIs(t *testing.T) {
	cases := map[string]string{
		"T.Sin refused the token: Token is currently in use on another online server. Stop it first to switch IPs.": ProblemInUse,
		`HTTP 403: {"error": "Token is currently in use on another online server."}`:                                ProblemInUse,
		"T.Sin dc2 server error (HTTP 503)": ProblemDatacenterDown,
		"HTTP 503: ":                        ProblemDatacenterDown,
		"T.Sin dc1 unreachable: dial tcp 1.2.3.4:8080: connect: connection refused": ProblemDatacenterDown,
		"T.Sin dc1 unreachable: dial tcp: i/o timeout":                              ProblemUnreachable,
		"HTTP 000: ":                             ProblemUnreachable,
		"T.Sin refused the token: invalid token": ProblemTokenRefused,
		"something new":                          ProblemUnknown,
		"":                                       "",
	}
	for text, want := range cases {
		if got := ClassifyText(text); got != want {
			t.Errorf("ClassifyText(%q) = %q, want %q", text, got, want)
		}
		if want != "" && ExplainFA(want) == "" {
			t.Errorf("no Persian explanation for %q", want)
		}
	}
}

// A datacenter whose service is down answers 503, sometimes with a JSON error
// page; that is the datacenter's failure, not a refusal of the token.
func TestStatusCallsA503ADatacenterProblem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"maintenance"}`))
	}))
	defer srv.Close()
	client := Client{HTTP: srv.Client(), BaseURL: func(string) string { return srv.URL }}
	_, err := client.Status(context.Background(), "dc2", "token-123456")
	if Classify(err) != ProblemDatacenterDown || !strings.Contains(err.Error(), "maintenance") {
		t.Fatalf("err = %v (%s)", err, Classify(err))
	}
}

func TestParseStatusReadsANodeReport(t *testing.T) {
	status, err := ParseStatus([]byte(`{"status":"success","client_name":"seller","is_active":1,"active_proxies":{"1080":{"country":"Germany"}}}`))
	if err != nil || status.ClientName != "seller" || !status.Active || len(status.Proxies) != 1 {
		t.Fatalf("status = %+v err = %v", status, err)
	}
	if _, err := ParseStatus([]byte(`{"error":"in use"}`)); err == nil {
		t.Fatal("a refusal must not parse as an account")
	}
}

func TestNodeScriptReportsOnlyWithAValidKey(t *testing.T) {
	key := strings.Repeat("ab", 32)
	script := NodeScriptReporting("dc1", key)
	if !strings.Contains(script, "REPORT_KEY='"+key+"'") || !strings.Contains(script, "REPORT_URL='"+ReportURLPlaceholder+"/internal/tsin/report'") {
		t.Fatal("the report settings are missing from the script")
	}
	if !strings.Contains(script, "next-tsin-report") {
		t.Fatal("the reporter is not installed")
	}
	if !strings.Contains(NodeScriptReporting("dc1", "'; rm -rf / #"), "REPORT_KEY=''") {
		t.Fatal("an invalid key reached the script")
	}
}
