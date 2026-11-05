package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aliking1367/next/internal/app/tsin"
)

func stepByID(steps []tsinStep, id string) tsinStep {
	for _, step := range steps {
		if step.ID == id {
			return step
		}
	}
	return tsinStep{}
}

// useChecklistTSin points the checklist at a fake T.Sin answering status.
func useChecklistTSin(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	tsinChecklistClient = func() tsin.Client {
		return tsin.Client{HTTP: srv.Client(), BaseURL: func(string) string { return srv.URL }}
	}
	t.Cleanup(func() { tsinChecklistClient = tsin.NewClient })
}

func TestTSinChecklistWithoutATokenStartsAtTheToken(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	settings, _ := server.loadTSinSettings(context.Background())
	settings.Token = ""
	steps := server.tsinChecklist(context.Background(), settings, "")
	if step := stepByID(steps, "token"); step.State != "todo" || step.Reason != "no_token" {
		t.Fatalf("token = %+v", step)
	}
	if step := stepByID(steps, "datacenter"); step.State != "skip" {
		t.Fatalf("datacenter = %+v", step)
	}
}

func TestTSinChecklistRefusedTokenIsTheAccount(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	useChecklistTSin(t, http.StatusOK, `{"status":"error","error":"invalid token"}`)
	settings, _ := server.loadTSinSettings(context.Background())
	steps := server.tsinChecklist(context.Background(), settings, "")
	if step := stepByID(steps, "token"); step.State != "fail" || step.Reason != "token_refused" {
		t.Fatalf("token = %+v", step)
	}
}

func TestTSinChecklistADownDatacenterIsT_SinSide(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	useChecklistTSin(t, http.StatusServiceUnavailable, ``)
	settings, _ := server.loadTSinSettings(context.Background())
	steps := server.tsinChecklist(context.Background(), settings, "")
	if step := stepByID(steps, "datacenter"); step.State != "fail" || step.Reason != tsin.ProblemDatacenterDown {
		t.Fatalf("datacenter = %+v", step)
	}
}

// Before the node reports, T.Sin answers the panel "in use": the token is
// fine and what is left is running the node script.
func TestTSinChecklistWaitsForTheNodeScript(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	useChecklistTSin(t, http.StatusForbidden, `{"error": "Token is currently in use on another online server."}`)
	settings, _ := server.loadTSinSettings(context.Background())
	steps := server.tsinChecklist(context.Background(), settings, "")
	if step := stepByID(steps, "token"); step.State != "ok" || step.Reason != "in_use_node" {
		t.Fatalf("token = %+v", step)
	}
	if step := stepByID(steps, "node"); step.State != "todo" || step.Reason != "never" {
		t.Fatalf("node = %+v", step)
	}
}

func TestTSinChecklistFollowsTheNodeReport(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	key := server.tsinReportKey(context.Background())
	postTSinReport(server, key, map[string]any{
		"dc": "dc1", "handshake_age": 30,
		"account": nodeAccount(map[string]string{"1080": "Germany", "1093": "Italy"}),
		"ports":   map[string]bool{"1080": true, "1093": false}, "ports_at": time.Now().Unix(),
	})
	settings, _ := server.loadTSinSettings(context.Background())
	steps := server.tsinChecklist(context.Background(), settings, "")
	for id, want := range map[string]string{"token": "ok", "datacenter": "ok", "apply": "ok", "node": "ok", "countries": "warn"} {
		if step := stepByID(steps, id); step.State != want {
			t.Errorf("%s = %+v, want %s", id, step, want)
		}
	}
	if failing := stepByID(steps, "countries").Params["failing"]; failing != "Italy (1093)" {
		t.Fatalf("failing = %v", failing)
	}
}

func TestTSinGateVerdictNamesTheWrongSetting(t *testing.T) {
	cloudflare := http.Header{"Cf-Ray": []string{"abc"}}
	challenged := http.Header{"Cf-Ray": []string{"abc"}, "Cf-Mitigated": []string{"challenge"}}
	cases := []struct {
		code   int
		header http.Header
		want   string
	}{
		{101, cloudflare, ""},
		{404, http.Header{}, "not_proxied"},
		{403, challenged, "challenge"},
		{400, cloudflare, "ssl_flexible"},
		{526, cloudflare, "ssl_strict"},
		{522, cloudflare, "origin_down"},
		{520, cloudflare, "gate_answer"},
	}
	for _, c := range cases {
		if _, reason := tsinGateVerdict(c.code, c.header); reason != c.want {
			t.Errorf("%d: reason = %q, want %q", c.code, reason, c.want)
		}
	}
}

func TestPanelOriginStaysOnThePanelHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://next.example.com/api/core/tsin/checklist?origin=https://next.example.com", nil)
	if got := panelOrigin(request); got != "https://next.example.com" {
		t.Fatalf("origin = %q", got)
	}
	request = httptest.NewRequest(http.MethodGet, "https://next.example.com/api/core/tsin/checklist?origin=http://169.254.169.254", nil)
	if got := panelOrigin(request); got != "" {
		t.Fatalf("another host was accepted: %q", got)
	}
}
