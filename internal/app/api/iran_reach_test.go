package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
