package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake check-host with three Iranian test servers and one elsewhere: the
// first answers, one times out, one is still pending on the first read.
func TestCheckHostCountsIranianServersThatReachTheAddress(t *testing.T) {
	checkHostPollDelay = 10 * time.Millisecond
	t.Cleanup(func() { checkHostPollDelay = 5 * time.Second })
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/nodes/hosts":
			fmt.Fprint(w, `{"nodes":{"ir1.node":{"location":["ir","Iran","Tehran"]},"ir2.node":{"location":["ir","Iran","Shiraz"]},"ir3.node":{"location":["ir","Iran","Qom"]},"de1.node":{"location":["de","Germany","Frankfurt"]}}}`)
		case r.URL.Path == "/check-tcp":
			if got := r.URL.Query()["node"]; len(got) != 3 || strings.Contains(strings.Join(got, ","), "de1") {
				t.Errorf("nodes asked = %v, want only the three Iranian ones", got)
			}
			fmt.Fprint(w, `{"request_id":"abc"}`)
		case r.URL.Path == "/check-result/abc":
			reads++
			if reads == 1 {
				fmt.Fprint(w, `{"ir1.node":[{"time":0.08,"address":"1.2.3.4"}],"ir2.node":[{"error":"Connection timed out"}],"ir3.node":null}`)
				return
			}
			fmt.Fprint(w, `{"ir1.node":[{"time":0.08,"address":"1.2.3.4"}],"ir2.node":[{"error":"Connection timed out"}],"ir3.node":[{"time":0.1,"address":"1.2.3.4"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	open, total, err := checkHostTCPFromIran(context.Background(), srv.Client(), srv.URL, "1.2.3.4:2053")
	if err != nil || open != 2 || total != 3 {
		t.Fatalf("open=%d total=%d err=%v, want 2 of 3", open, total, err)
	}
}

func TestIranReachVerdicts(t *testing.T) {
	if !(iranReachResult{Open: 0, Total: 8}).blocked() {
		t.Fatal("0 of 8 is blocked")
	}
	if (iranReachResult{Open: 0, Total: 8, Error: "down"}).blocked() {
		t.Fatal("a failed check must not count as blocked")
	}
	if !(iranReachResult{Open: 5, Total: 8}).healthy() || (iranReachResult{Open: 2, Total: 8}).healthy() {
		t.Fatal("healthy means at least half reached it")
	}
}

func TestCheckHostReachedReadsTCPAndHTTPResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result []any
		want   bool
	}{
		{"tcp ok", []any{map[string]any{"time": 0.1, "address": "1.2.3.4"}}, true},
		{"tcp error", []any{map[string]any{"error": "Connection timed out"}}, false},
		{"http ok", []any{[]any{float64(1), 0.12, "Bad Request", "400", "104.21.0.1"}}, true},
		{"http failed", []any{[]any{float64(0), 10.0, "Connection timed out", nil, nil}}, false},
		{"http 520 from Cloudflare", []any{[]any{float64(0), 0.29, "<none>", "520", "188.114.96.3"}}, true},
		{"empty", []any{}, false},
		{"null", []any{nil}, false},
	} {
		if got := checkHostReached(tc.result); got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A fake check-host where four Iranian test servers download the control in
// full and one (ir5) is down: a new domain that answers but stalls is slow
// (throttled), an old one is fast, and ir5 is not counted against either.
func TestCloudflareDownloadsFromIranSeeAStalledNewDomain(t *testing.T) {
	checkHostPollDelay = 10 * time.Millisecond
	t.Cleanup(func() { checkHostPollDelay = 5 * time.Second })
	full := `[[1,0.5,"OK","200","104.17.24.14"]]`
	stalled := `[[0,25.0,"Connection timed out",null,null]]`
	down := `[[0,10.0,"No route to host",null,null]]`
	answers := map[string]string{
		"https://cdnjs.cloudflare.com" + cloudflareEdgeProbe: fmt.Sprintf(`{"ir1.node":%s,"ir2.node":%s,"ir3.node":%s,"ir4.node":%s,"ir5.node":%s}`, full, full, full, full, down),
		"https://new.example" + cloudflareEdgeProbe:          fmt.Sprintf(`{"ir1.node":%s,"ir2.node":%s,"ir3.node":%s,"ir4.node":%s,"ir5.node":%s}`, stalled, stalled, stalled, full, down),
		"https://old.example" + cloudflareEdgeProbe:          fmt.Sprintf(`{"ir1.node":%s,"ir2.node":%s,"ir3.node":%s,"ir4.node":%s,"ir5.node":%s}`, full, full, full, full, down),
	}
	var mu sync.Mutex
	ids := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/nodes/hosts":
			fmt.Fprint(w, `{"nodes":{"ir1.node":{"location":["ir"]},"ir2.node":{"location":["ir"]},"ir3.node":{"location":["ir"]},"ir4.node":{"location":["ir"]},"ir5.node":{"location":["ir"]}}}`)
		case r.URL.Path == "/check-http":
			target := r.URL.Query().Get("host")
			mu.Lock()
			id := fmt.Sprintf("id%d", len(ids))
			ids[id] = target
			mu.Unlock()
			fmt.Fprintf(w, `{"request_id":%q}`, id)
		case strings.HasPrefix(r.URL.Path, "/check-result/"):
			mu.Lock()
			target := ids[strings.TrimPrefix(r.URL.Path, "/check-result/")]
			mu.Unlock()
			fmt.Fprint(w, answers[target])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	results, err := checkCloudflareDownloadsFromIran(context.Background(), srv.Client(), srv.URL, []string{"new.example", "old.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got := results["new.example"]; got.Total != 4 || got.Complete != 1 || got.Reached != 1 {
		t.Fatalf("new.example = %+v, want 1 of 4 complete", got)
	}
	if got := results["old.example"]; got.Total != 4 || got.Complete != 4 || !iranFast(got.Complete, got.Total) {
		t.Fatalf("old.example = %+v, want 4 of 4 complete", got)
	}
	if !results["new.example"].throttled() || results["old.example"].throttled() {
		t.Fatal("1 of 4 is throttled, 4 of 4 is not")
	}
	if !iranSlow(1, 7) || iranSlow(2, 7) || iranFast(3, 7) || !iranFast(4, 7) {
		t.Fatal("slow is at most a quarter, fast at least half")
	}
}

func TestCheckHostDownloadComplete(t *testing.T) {
	if !checkHostDownloadComplete([]any{[]any{float64(1), 0.5, "OK", "200", "1.2.3.4"}}) {
		t.Fatal("ok with 200 is complete")
	}
	if checkHostDownloadComplete([]any{[]any{float64(1), 0.5, "Not Found", "404", "1.2.3.4"}}) {
		t.Fatal("a 404 is not a download")
	}
	if checkHostDownloadComplete([]any{[]any{float64(0), 25.0, "Connection timed out", nil, nil}}) {
		t.Fatal("a timeout is not complete")
	}
}
