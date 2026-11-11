//go:build cgo

package xrayconfig

import (
	"context"
	"strings"
	"testing"
)

func TestTSinLabMakesTestInboundsThroughTheBestPingExitAndRemovesThem(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	if _, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", nil); err == nil {
		t.Fatal("the lab must refuse to start before the countries are applied")
	}
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE"},
		{Port: 1090, Name: "Best ping"},
	}
	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil); err != nil {
		t.Fatal(err)
	}
	tests, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tests) != 5 {
		t.Fatalf("got %d lab tests, want 5", len(tests))
	}
	for _, test := range tests {
		if !strings.HasPrefix(test.Link, "vless://") || !strings.Contains(test.Link, "@203.0.113.9:") {
			t.Fatalf("lab link %q", test.Link)
		}
	}
	config, err := repo.GetTargetRawConfig(ctx, "master")
	if err != nil {
		t.Fatal(err)
	}
	rules := listOfMaps(mapValue(config["routing"])["rules"])
	if !tsinLabRule(rules[0]) || stringValue(rules[0]["outboundTag"]) != TSinOutboundTag(1090) {
		t.Fatalf("the lab must leave through Best ping's exit first: %#v", rules[0])
	}
	// A second create keeps the same lab and links.
	again, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", nil)
	if err != nil || again[0].Link != tests[0].Link {
		t.Fatalf("a second create made a new lab: %v", err)
	}
	// The Cloudflare check never takes a lab inbound for a country.
	if err := repo.RemoveTSinLab(ctx, "master"); err != nil {
		t.Fatal(err)
	}
	config, _ = repo.GetTargetRawConfig(ctx, "master")
	for _, inbound := range listOfMaps(config["inbounds"]) {
		if strings.HasPrefix(stringValue(inbound["tag"]), TSinLabPrefix) {
			t.Fatalf("lab inbound %v survived removal", inbound["tag"])
		}
	}
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		if tsinLabRule(rule) {
			t.Fatal("the lab rule survived removal")
		}
	}

	// Applying the countries also removes a lab left behind.
	if _, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, TSinLabPrefix+"http"); err == nil {
		t.Fatal("applying the countries kept the lab")
	}
}
