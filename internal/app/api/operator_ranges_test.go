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

func TestMobileRangesClassify(t *testing.T) {
	var st operatorRangesState
	if got := st.classify("5.208.224.9"); got != "" {
		t.Fatalf("before the ranges load the network must be unknown, got %q", got)
	}
	st.set(operatorRangesData{
		MCI:       []string{"5.208.224.0/20", "2a03:ef42::/32", "bad"},
		Iran:      []string{"5.208.0.0/12", "2.176.0.0-2.191.255.255", "2a03:ef40::/29", "nope"},
		FetchedAt: time.Now(),
	})
	for address, want := range map[string]string{
		"5.208.224.9":        operatorMCI,
		"5.208.239.255":      operatorMCI,
		"::ffff:5.208.224.9": operatorMCI,
		"2a03:ef42:1::5":     operatorMCI,
		"5.209.1.1":          operatorIran,
		"2.180.3.4":          operatorIran,
		"2a03:ef44::1":       operatorIran,
		"8.8.8.8":            operatorForeign,
		"2001:41d0::1":       operatorForeign,
		"not an address":     "",
		"":                   "",
	} {
		if got := st.classify(address); got != want {
			t.Fatalf("classify(%q) = %q, want %q", address, got, want)
		}
	}
	if summary := st.summary(); summary["mci_ranges"] != 2 || summary["iran_ranges"] != 3 {
		t.Fatalf("summary = %v", summary)
	}
}

func TestFetchMobileRangesFromRIPE(t *testing.T) {
	var mci, iran []string
	for i := 0; i < 60; i++ {
		mci = append(mci, fmt.Sprintf(`{"prefix":"5.%d.0.0/16"}`, i))
	}
	for i := 0; i < 210; i++ {
		iran = append(iran, fmt.Sprintf(`"10.%d.0.0/16"`, i))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/announced-prefixes/") && r.URL.Query().Get("resource") == "AS197207":
			fmt.Fprintf(w, `{"data":{"prefixes":[%s]}}`, strings.Join(mci, ","))
		case strings.HasPrefix(r.URL.Path, "/country-resource-list/") && r.URL.Query().Get("resource") == "IR":
			fmt.Fprintf(w, `{"data":{"resources":{"ipv4":[%s],"ipv6":["2a03:ef40::/29"]}}}`, strings.Join(iran, ","))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	data, err := fetchOperatorRanges(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.MCI) != 60 || len(data.Iran) != 211 {
		t.Fatalf("got %d MCI and %d Iran ranges", len(data.MCI), len(data.Iran))
	}

	// A near-empty answer is an outage, not the truth.
	thin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/announced-prefixes/") {
			fmt.Fprint(w, `{"data":{"prefixes":[{"prefix":"5.1.0.0/16"}]}}`)
			return
		}
		fmt.Fprint(w, `{"data":{"resources":{"ipv4":["10.0.0.0/8"],"ipv6":[]}}}`)
	}))
	defer thin.Close()
	if _, err := fetchOperatorRanges(context.Background(), thin.Client(), thin.URL); err == nil {
		t.Fatal("a near-empty answer must be refused")
	}
}

func TestNormalizeEmergencyAudience(t *testing.T) {
	for value, want := range map[string]string{"mci": "mci", " MCI ": "mci", "all": "all", "": "all", "irancell": "all"} {
		if got := normalizeEmergencyAudience(value); got != want {
			t.Fatalf("normalizeEmergencyAudience(%q) = %q, want %q", value, got, want)
		}
	}
}
