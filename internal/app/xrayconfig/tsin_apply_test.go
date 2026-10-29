//go:build cgo

package xrayconfig

import (
	"context"
	"strings"
	"testing"
)

func TestApplyTSinCreatesOneInboundPerCountryAndRoutesIt(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Address: "10.0.0.1"},
		{Port: 1095, Name: "Turkey", Code: "TR", Address: "10.0.0.1"},
	}

	first, err := repo.ApplyTSin(ctx, "master", "tcp", countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(first) != 2 || !first[0].Created || first[0].Port == first[1].Port {
		t.Fatalf("first apply = %#v", first)
	}
	// Applying again keeps the same inbounds and ports: users' links must not move.
	second, err := repo.ApplyTSin(ctx, "master", "tcp", countries, nil)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if second[0].Created || second[0].Port != first[0].Port || second[1].Port != first[1].Port {
		t.Fatalf("re-apply changed the inbounds: %#v vs %#v", first, second)
	}

	config, err := repo.MasterRawConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rules := interfaceSlice(mapValue(config["routing"])["rules"])
	if len(rules) < 2 || stringValue(mapValue(rules[0])["outboundTag"]) != "tsin-out-1082" {
		t.Fatalf("rules = %#v", rules)
	}
	if len(listOfMaps(config["outbounds"])) < 2 {
		t.Fatalf("outbounds = %#v", config["outbounds"])
	}

	var remark string
	if err := db.QueryRow(`SELECT remark FROM hosts WHERE inbound_tag = 'tsin-germany'`).Scan(&remark); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(remark, "\U0001F1E9\U0001F1EA") || !strings.Contains(remark, "Germany") {
		t.Fatalf("remark = %q", remark)
	}

	// Dropping a country retires its inbound and its rule, and leaves the other.
	if _, err := repo.ApplyTSin(ctx, "master", "tcp", countries[:1], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, "tsin-turkey"); err == nil {
		t.Fatal("tsin-turkey was not retired")
	}
	if err := repo.RemoveTSin(ctx); err != nil {
		t.Fatal(err)
	}
	config, _ = repo.MasterRawConfig(ctx)
	for _, outbound := range listOfMaps(config["outbounds"]) {
		if strings.HasPrefix(stringValue(outbound["tag"]), TSinOutboundPrefix) {
			t.Fatalf("outbound left behind: %#v", outbound)
		}
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany"); err == nil {
		t.Fatal("tsin-germany was not removed")
	}
}

func TestApplyTSinCanMoveToANodeAndLeavesNothingBehind(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{{Port: 1082, Name: "Germany", Code: "DE"}}

	if _, err := repo.ApplyTSin(ctx, "master", "tcp", countries, nil); err != nil {
		t.Fatalf("apply on master: %v", err)
	}
	moved, err := repo.ApplyTSin(ctx, "node:7", "tcp", countries, nil)
	if err != nil {
		t.Fatalf("apply on node: %v", err)
	}
	if len(moved) != 1 || !moved[0].Created {
		t.Fatalf("the inbound was not rebuilt on the node: %#v", moved)
	}

	direct, err := repo.directTargetsForInbound(ctx, "tsin-germany")
	if err != nil || len(direct) != 1 || direct[0] != "node:7" {
		t.Fatalf("targets = %v, err = %v", direct, err)
	}
	master, err := repo.MasterRawConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hasTSinRouting(master) {
		t.Fatalf("master still routes T.Sin after the move: %#v", master["outbounds"])
	}
	node, err := repo.GetTargetRawConfig(ctx, "node:7")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTSinRouting(node) {
		t.Fatalf("the node has no T.Sin routing: %#v", node["outbounds"])
	}

	if err := repo.RemoveTSin(ctx); err != nil {
		t.Fatal(err)
	}
	node, _ = repo.GetTargetRawConfig(ctx, "node:7")
	if hasTSinRouting(node) {
		t.Fatal("removal left routing on the node")
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany"); err == nil {
		t.Fatal("the inbound survived removal")
	}
}

func TestApplyTSinSwitchesTransportByRebuilding(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{{Port: 1082, Name: "Germany", Code: "DE"}}

	if _, err := repo.ApplyTSin(ctx, "master", "tcp", countries, nil); err != nil {
		t.Fatal(err)
	}
	switched, err := repo.ApplyTSin(ctx, "master", "xhttp", countries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !switched[0].Created || switched[0].Transport != "xhttp" {
		t.Fatalf("the inbound was not rebuilt on XHTTP: %#v", switched)
	}
	inbound, err := repo.GetInbound(ctx, "tsin-germany")
	if err != nil {
		t.Fatal(err)
	}
	stream := mapValue(inbound["streamSettings"])
	if stringValue(stream["network"]) != "xhttp" || stringValue(stream["security"]) != "reality" {
		t.Fatalf("stream = %#v", stream)
	}
	again, err := repo.ApplyTSin(ctx, "master", "xhttp", countries, nil)
	if err != nil || again[0].Created || again[0].Port != switched[0].Port {
		t.Fatalf("re-applying the same transport moved the inbound: %#v %v", again, err)
	}
}
