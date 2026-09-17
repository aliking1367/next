//go:build cgo

package xrayconfig

import (
	"context"
	"testing"
)

func TestAutoProvisionBestProtocolsCreatesAllProtocolsWithHosts(t *testing.T) {
	repo, db := testRepository(t)
	ctx := context.Background()

	result, err := repo.AutoProvisionBestProtocols(ctx)
	if err != nil {
		t.Fatalf("AutoProvisionBestProtocols: %v", err)
	}
	if len(result.Protocols) != 4 {
		t.Fatalf("expected 4 protocols, got %d", len(result.Protocols))
	}

	seenPorts := map[int]bool{}
	wantTags := map[string]string{
		"auto-vless-reality":  "vless",
		"auto-trojan-reality": "trojan",
		"auto-hysteria2":      "hysteria",
		"auto-shadowsocks":    "shadowsocks",
	}
	for _, protocol := range result.Protocols {
		if !protocol.Created {
			t.Errorf("expected %s to be newly created", protocol.Tag)
		}
		if wantProtocol, ok := wantTags[protocol.Tag]; !ok || wantProtocol != protocol.Protocol {
			t.Errorf("unexpected tag/protocol pair %q/%q", protocol.Tag, protocol.Protocol)
		}
		if protocol.Port < autoInboundMinPort || protocol.Port > autoInboundMaxPort {
			t.Errorf("port %d out of expected range", protocol.Port)
		}
		if seenPorts[protocol.Port] {
			t.Errorf("duplicate port %d assigned across protocols", protocol.Port)
		}
		seenPorts[protocol.Port] = true

		var hostCount int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, protocol.Tag).Scan(&hostCount); err != nil {
			t.Fatalf("query hosts for %s: %v", protocol.Tag, err)
		}
		if hostCount != 1 {
			t.Errorf("expected exactly one host row for %s, got %d", protocol.Tag, hostCount)
		}
	}

	// Re-running is idempotent: same tags/ports, nothing newly created, no
	// duplicate host rows.
	again, err := repo.AutoProvisionBestProtocols(ctx)
	if err != nil {
		t.Fatalf("AutoProvisionBestProtocols (second run): %v", err)
	}
	if len(again.Protocols) != 4 {
		t.Fatalf("expected 4 protocols on second run, got %d", len(again.Protocols))
	}
	for _, protocol := range again.Protocols {
		if protocol.Created {
			t.Errorf("expected %s to be reused, not recreated", protocol.Tag)
		}
		var hostCount int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, protocol.Tag).Scan(&hostCount); err != nil {
			t.Fatalf("query hosts for %s: %v", protocol.Tag, err)
		}
		if hostCount != 1 {
			t.Errorf("expected exactly one host row for %s after re-run, got %d", protocol.Tag, hostCount)
		}
	}
}
