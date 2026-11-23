//go:build cgo

package xrayconfig

import (
	"context"
	"testing"
)

func tsinRuleInbounds(config map[string]any, outbound string) map[string]bool {
	found := map[string]bool{}
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		if stringValue(rule["outboundTag"]) != outbound {
			continue
		}
		for _, tag := range interfaceSlice(rule["inboundTag"]) {
			found[stringValue(tag)] = true
		}
	}
	return found
}

func TestApplyTSinMovesTheDirectConfigsToTheSecondServerAsTheyAre(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Address: "10.0.0.1", Vision: true, HTTP: true, Direct: true},
		{Port: 1095, Name: "Turkey", Code: "TR", Address: "10.0.0.1"},
	}
	gate := TSinGate{Host: "g1.example.com", DirectAddress: "167.233.180.133"}
	if _, err := repo.ApplyTSin(ctx, "node:7", "path", gate, countries, nil); err != nil {
		t.Fatalf("apply on one server: %v", err)
	}
	before := map[string]map[string]any{}
	for _, tag := range []string{"tsin-germany-vision", "tsin-germany-http", "tsin-germany-direct"} {
		inbound, err := repo.GetInbound(ctx, tag)
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		before[tag] = inbound
	}

	gate.DirectTarget = "node:8"
	applied, err := repo.ApplyTSin(ctx, "node:7", "path", gate, countries, nil)
	if err != nil {
		t.Fatalf("apply with a second server: %v", err)
	}
	for _, entry := range applied {
		if entry.Created {
			t.Fatalf("%s was made again instead of moved: %#v", entry.Tag, entry)
		}
	}
	for tag, old := range before {
		targets, err := repo.directTargetsForInbound(ctx, tag)
		if err != nil || len(targets) != 1 || targets[0] != "node:8" {
			t.Fatalf("%s runs on %v (%v), want node:8 only", tag, targets, err)
		}
		now, err := repo.GetInbound(ctx, tag)
		if err != nil {
			t.Fatal(err)
		}
		oldPort, _ := parseConfigPort(old["port"])
		newPort, _ := parseConfigPort(now["port"])
		oldStream, newStream := mapValue(old["streamSettings"]), mapValue(now["streamSettings"])
		if oldPort != newPort || stringValue(mapValue(oldStream["realitySettings"])["privateKey"]) != stringValue(mapValue(newStream["realitySettings"])["privateKey"]) {
			t.Fatalf("%s changed on the move: port %d -> %d", tag, oldPort, newPort)
		}
	}
	if targets, _ := repo.directTargetsForInbound(ctx, "tsin-germany"); len(targets) != 1 || targets[0] != "node:7" {
		t.Fatalf("the country's own config moved too: %v", targets)
	}

	mainConfig, _ := repo.GetTargetRawConfig(ctx, "node:7")
	mainRule := tsinRuleInbounds(mainConfig, "tsin-out-1082")
	if !mainRule["tsin-germany"] || mainRule["tsin-germany-vision"] || mainRule["tsin-germany-http"] || mainRule["tsin-germany-direct"] {
		t.Fatalf("countries' server routes %v", mainRule)
	}
	directConfig, _ := repo.GetTargetRawConfig(ctx, "node:8")
	directRule := tsinRuleInbounds(directConfig, "tsin-out-1082")
	if directRule["tsin-germany"] || !directRule["tsin-germany-vision"] || !directRule["tsin-germany-http"] || !directRule["tsin-germany-direct"] {
		t.Fatalf("direct server routes %v", directRule)
	}
	if len(tsinRuleInbounds(directConfig, "tsin-out-1095")) > 0 {
		t.Fatal("the direct server routes a country with no direct config")
	}
	keptOwn := false
	for _, inbound := range listOfMaps(directConfig["inbounds"]) {
		if stringValue(inbound["tag"]) == "custom-ss" {
			keptOwn = true
		}
	}
	if !keptOwn {
		t.Fatal("the direct server lost its own inbound")
	}

	// Taking the second server away brings them back, still as they were.
	gate.DirectTarget = ""
	if _, err := repo.ApplyTSin(ctx, "node:7", "path", gate, countries, nil); err != nil {
		t.Fatalf("apply back on one server: %v", err)
	}
	for tag, old := range before {
		targets, _ := repo.directTargetsForInbound(ctx, tag)
		now, _ := repo.GetInbound(ctx, tag)
		oldPort, _ := parseConfigPort(old["port"])
		newPort, _ := parseConfigPort(now["port"])
		if len(targets) != 1 || targets[0] != "node:7" || oldPort != newPort {
			t.Fatalf("%s back on %v port %d, want node:7 port %d", tag, targets, newPort, oldPort)
		}
	}
	directConfig, _ = repo.GetTargetRawConfig(ctx, "node:8")
	if hasTSinRouting(directConfig) {
		t.Fatal("the second server kept T.Sin routing")
	}
}
