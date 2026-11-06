package api

import (
	"testing"
	"time"
)

func TestNodeLoadWatchAlertsOnlyOnSustainedLoad(t *testing.T) {
	watch := &nodeLoadWatch{}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	step := func(cpu, memory float64) string {
		now = now.Add(time.Minute)
		return watch.observe(1, nodeLoadSample{Name: "France", CPU: cpu, Memory: memory}, now)
	}

	// A short spike is not an alert.
	for i := 0; i < nodeLoadHighChecks-1; i++ {
		if got := step(95, 30); got != "" {
			t.Fatalf("minute %d of a spike sent %q", i+1, got)
		}
	}
	if got := step(20, 30); got != "" {
		t.Fatalf("calm after a spike sent %q", got)
	}

	// Five busy minutes in a row are, once; memory counts as much as CPU.
	var verdicts []string
	for i := 0; i < nodeLoadHighChecks+3; i++ {
		if got := step(40, 90); got != "" {
			verdicts = append(verdicts, got)
		}
	}
	if len(verdicts) != 1 || verdicts[0] != "busy" {
		t.Fatalf("verdicts = %v, want one busy", verdicts)
	}

	// Between the thresholds it stays alerted; two calm minutes end it.
	if got := step(75, 30); got != "" {
		t.Fatalf("in-between sent %q", got)
	}
	if got := step(30, 30); got != "" {
		t.Fatalf("first calm minute sent %q", got)
	}
	if got := step(30, 30); got != "calm" {
		t.Fatalf("second calm minute sent %q, want calm", got)
	}
}

func TestNodeLoadWatchRemindsWhileStillBusy(t *testing.T) {
	watch := &nodeLoadWatch{}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	sent := map[string]int{}
	for i := 0; i < int(nodeLoadRemind/time.Minute)+nodeLoadHighChecks+1; i++ {
		now = now.Add(time.Minute)
		if got := watch.observe(1, nodeLoadSample{CPU: 99}, now); got != "" {
			sent[got]++
		}
	}
	if sent["busy"] != 1 || sent["still_busy"] != 1 {
		t.Fatalf("sent = %v, want one alert and one reminder after %s", sent, nodeLoadRemind)
	}
}
