//go:build cgo

package xrayconfig

import (
	"context"
	"testing"
)

func TestTSinConnectAddressKeepsTheDomainAsSNIAndSurvivesASwitch(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	gate := TSinGate{Host: "g1.example.com", TunnelHost: "tun.example.ir"}
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", TunnelPort: 20100, Direct: true},
	}
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	row := func(tag string) (address, sni string) {
		t.Helper()
		var s *string
		if err := repo.db.QueryRowContext(ctx, `SELECT address, sni FROM hosts WHERE inbound_tag = ?`, tag).Scan(&address, &s); err != nil {
			t.Fatal(err)
		}
		if s != nil {
			sni = *s
		}
		return
	}
	cf := TSinInboundTag("Germany")

	if n, err := repo.SetTSinConnectAddress(ctx, "g1.example.com", "render.com"); err != nil || n != 1 {
		t.Fatalf("set: %d %v", n, err)
	}
	if address, sni := row(cf); address != "render.com" || sni != "g1.example.com" {
		t.Fatalf("after set: %q %q", address, sni)
	}
	// The direct and tunnel configs are not Cloudflare's and keep theirs.
	if address, _ := row(TSinTunnelInboundTag("Germany")); address != "tun.example.ir" {
		t.Fatalf("tunnel config moved to %q", address)
	}

	// Switching domain keeps the connect address and moves SNI and Host.
	if n, err := repo.SetTSinGateHost(ctx, "g1.example.com", "g2.example.com"); err != nil || n != 1 {
		t.Fatalf("switch: %d %v", n, err)
	}
	if address, sni := row(cf); address != "render.com" || sni != "g2.example.com" {
		t.Fatalf("after switch: %q %q", address, sni)
	}

	// Applying the countries again keeps it too.
	gate.Host, gate.ConnectAddress = "g2.example.com", "render.com"
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	if address, sni := row(cf); address != "render.com" || sni != "g2.example.com" {
		t.Fatalf("after apply: %q %q", address, sni)
	}

	// Empty goes back to the domain itself.
	if _, err := repo.SetTSinConnectAddress(ctx, "g2.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if address, _ := row(cf); address != "g2.example.com" {
		t.Fatalf("after reset: %q", address)
	}
	// And a switch without a connect address moves the address as before.
	if _, err := repo.SetTSinGateHost(ctx, "g2.example.com", "g3.example.com"); err != nil {
		t.Fatal(err)
	}
	if address, sni := row(cf); address != "g3.example.com" || sni != "g3.example.com" {
		t.Fatalf("after plain switch: %q %q", address, sni)
	}
}
