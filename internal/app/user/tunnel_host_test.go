package user

import "testing"

func tunnelLocations() []ConfigLocation {
	return []ConfigLocation{
		{Name: "🇹🇷 Turkey", Address: "direct.example.com", TunnelAddress: "tr.tunnel.example.ir"},
		{Name: "🇩🇪 Germany", Address: "de.example.com", TunnelAddress: "de.tunnel.example.ir"},
		// Bought no tunnel yet.
		{Name: "🇫🇷 Paris", Address: "fr.example.com"},
	}
}

// One host row, one config per node that has a tunnel. A tunnel is bought per
// node, so the panel cannot derive it -- but once it is on the node, adding a
// node adds its config without anyone editing hosts.
func TestTunnelHostIsOfferedOncePerNodeThatHasOne(t *testing.T) {
	host := Host{Address: "{TUNNEL}", Remark: "{LOCATION} · Tunnel"}
	variants := hostLocationVariants(tunnelLocations(), host, map[string]string{})

	if len(variants) != 2 {
		t.Fatalf("expected one variant per node with a tunnel, got %d", len(variants))
	}
	if got := variants[0].variables["TUNNEL"]; got != "tr.tunnel.example.ir" {
		t.Fatalf("first variant TUNNEL = %q", got)
	}
	if got := variants[1].variables["TUNNEL"]; got != "de.tunnel.example.ir" {
		t.Fatalf("second variant TUNNEL = %q", got)
	}
	// The remark names the location itself, so the panel must not prefix it
	// again.
	for _, variant := range variants {
		if variant.prefix != "" {
			t.Fatalf("remark already names the location, got prefix %q", variant.prefix)
		}
	}
}

// A node with no tunnel is left out rather than handed a config pointing at an
// empty address: a user cannot tell that apart from one that is merely blocked.
func TestTunnelHostSkipsNodesWithoutATunnel(t *testing.T) {
	host := Host{Address: "{TUNNEL}"}
	for _, variant := range hostLocationVariants(tunnelLocations(), host, map[string]string{}) {
		if variant.variables["TUNNEL"] == "" {
			t.Fatal("a node without a tunnel produced a config")
		}
	}
	onlyWithout := []ConfigLocation{{Name: "🇫🇷 Paris", Address: "fr.example.com"}}
	if got := hostLocationVariants(onlyWithout, host, map[string]string{}); len(got) != 0 {
		t.Fatalf("expected no configs at all, got %d", len(got))
	}
}

// With one node the old code returned a single unexpanded variant, which would
// have handed the user the word "{TUNNEL}" as a hostname.
func TestTunnelHostExpandsEvenForASingleNode(t *testing.T) {
	single := []ConfigLocation{
		{Name: "🇹🇷 Turkey", Address: "direct.example.com", TunnelAddress: "tr.tunnel.example.ir"},
	}
	variants := hostLocationVariants(single, Host{Address: "{TUNNEL}"}, map[string]string{})
	if len(variants) != 1 {
		t.Fatalf("got %d variants", len(variants))
	}
	if got := variants[0].variables["TUNNEL"]; got != "tr.tunnel.example.ir" {
		t.Fatalf("TUNNEL = %q, want the node's tunnel", got)
	}
}

// The direct path must keep working exactly as before.
func TestServerIPHostIsUnaffectedByTunnels(t *testing.T) {
	host := Host{Address: "{SERVER_IP}", Remark: "{LOCATION} · Direct"}
	variants := hostLocationVariants(tunnelLocations(), host, map[string]string{})
	if len(variants) != 3 {
		t.Fatalf("every node should get a direct config, got %d", len(variants))
	}
	if got := variants[2].variables["SERVER_IP"]; got != "fr.example.com" {
		t.Fatalf("third variant SERVER_IP = %q", got)
	}
}

// A host with a fixed address is still offered once, as it always was.
func TestFixedHostIsStillOfferedOnce(t *testing.T) {
	host := Host{Address: "fixed.example.com"}
	if got := hostLocationVariants(tunnelLocations(), host, map[string]string{}); len(got) != 1 {
		t.Fatalf("got %d variants, want 1", len(got))
	}
}
