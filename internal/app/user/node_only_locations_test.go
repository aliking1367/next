package user

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestNodeOnlyInboundsAreOfferedAtTheNodeThatRunsThem(t *testing.T) {
	exclusive := map[string]bool{"tsin-germany": true}
	locations := []ConfigLocation{
		{Name: "🇩🇪 germany", Address: "191.0.2.26", skipTags: exclusive},
		{Name: "🇫🇷 France", Address: "e1.example.com", onlyTags: map[string]bool{"tsin-germany": true}},
	}
	base := map[string]string{"SERVER_IP": "191.0.2.26", "server_ip": "191.0.2.26"}

	country := hostLocationVariants(locations, Host{InboundTag: "tsin-germany", Address: "{SERVER_IP}"}, base)
	if len(country) != 1 || country[0].variables["SERVER_IP"] != "e1.example.com" {
		t.Fatalf("the country config does not point at the node that runs it: %#v", country)
	}

	shared := hostLocationVariants(locations, Host{InboundTag: "auto-reality", Address: "{SERVER_IP}"}, base)
	if len(shared) != 1 || shared[0].variables["SERVER_IP"] != "191.0.2.26" {
		t.Fatalf("a shared inbound was offered on a node without it: %#v", shared)
	}
}

func TestConfigLocationsAddsCustomNodesOnlyForTheirOwnInbounds(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "locations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE nodes (id INTEGER PRIMARY KEY, name TEXT, address TEXT, public_address TEXT, tunnel_address TEXT, status TEXT, xray_config_mode TEXT, xray_config TEXT);
CREATE TABLE xray_config (id INTEGER PRIMARY KEY, data TEXT);
INSERT INTO xray_config VALUES (1, '{"inbounds":[{"tag":"auto-reality"}]}');
INSERT INTO nodes VALUES
 (1, 'germany', '191.0.2.26', NULL, NULL, 'connected', 'default', NULL),
 (2, 'France', '155.0.2.18', 'e1.example.com', NULL, 'connected', 'custom', '{"inbounds":[{"tag":"auto-reality"},{"tag":"tsin-germany"}]}'),
 (3, 'copy only', '203.0.113.9', NULL, NULL, 'connected', 'custom', '{"inbounds":[{"tag":"auto-reality"}]}');`); err != nil {
		t.Fatal(err)
	}
	locations := Repository{db: db}.configLocations(context.Background())
	if len(locations) != 2 {
		t.Fatalf("want the shared node and the node with its own inbound, got %#v", locations)
	}
	if !locations[0].serves("auto-reality") || locations[0].serves("tsin-germany") {
		t.Fatalf("shared node serves the wrong inbounds: %#v", locations[0])
	}
	if locations[1].Address != "e1.example.com" || !locations[1].serves("tsin-germany") || locations[1].serves("auto-reality") {
		t.Fatalf("custom node serves the wrong inbounds: %#v", locations[1])
	}
}
