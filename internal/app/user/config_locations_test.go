package user

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func multiLocationLinks(t *testing.T, locations []ConfigLocation, hosts []Host) []string {
	t.Helper()
	serviceID := int64(1)
	response, err := BuildConfigLinks(
		ConfigLinkUser{
			ID: 7, Username: "ali", Status: "active", ServiceID: &serviceID,
			CredentialKey: "05bfddf81eb418fa1edbce7cd286eee1",
			Proxies:       []StoredProxy{{Type: "vless", Settings: map[string]any{"id": "05bfddf8-1eb4-18fa-1edb-ce7cd286eee1"}}},
			ServerIP:      "198.51.100.1",
			Locations:     locations,
		},
		map[string]ResolvedInbound{"VLESS": {"tag": "VLESS", "protocol": "vless", "port": int64(443), "network": "tcp", "tls": "none"}},
		[]string{"VLESS"},
		hosts,
		map[string][]byte{},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	return response.Links
}

func linkRemarkAndHost(t *testing.T, link string) (string, string) {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Fragment, parsed.Host
}

func TestMultiLocationRepeatsServerIPHostsPerNode(t *testing.T) {
	locations := []ConfigLocation{
		{Name: "🇩🇪 Germany", Address: "203.0.113.10"},
		{Name: "🇳🇱 Netherlands", Address: "2001:db8::1"},
	}
	hosts := []Host{
		{ID: 1, InboundTag: "VLESS", Remark: "Vision", Address: "{SERVER_IP}", Security: "inbound_default", ServiceIDs: []int64{1}},
		{ID: 2, InboundTag: "VLESS", Remark: "CDN", Address: "cdn.example.com", Security: "inbound_default", ServiceIDs: []int64{1}},
	}
	links := multiLocationLinks(t, locations, hosts)
	if len(links) != 3 {
		t.Fatalf("want one link per node plus the fixed-address host, got %d: %v", len(links), links)
	}
	remark, host := linkRemarkAndHost(t, links[0])
	if remark != "🇩🇪 Germany · Vision" || host != "203.0.113.10:443" {
		t.Fatalf("first location = %q @ %q", remark, host)
	}
	remark, host = linkRemarkAndHost(t, links[1])
	if remark != "🇳🇱 Netherlands · Vision" || host != "[2001:db8::1]:443" {
		t.Fatalf("second location = %q @ %q", remark, host)
	}
	// A host with its own fixed address is not repeated.
	remark, host = linkRemarkAndHost(t, links[2])
	if remark != "CDN" || host != "cdn.example.com:443" {
		t.Fatalf("fixed host = %q @ %q", remark, host)
	}
}

func TestMultiLocationHonoursLocationPlaceholderAndSingleNode(t *testing.T) {
	locations := []ConfigLocation{{Name: "🇩🇪 Germany", Address: "203.0.113.10"}, {Name: "🇫🇮 Finland", Address: "203.0.113.20"}}
	hosts := []Host{{ID: 1, InboundTag: "VLESS", Remark: "{LOCATION} | fast", Address: "{SERVER_IP}", Security: "inbound_default", ServiceIDs: []int64{1}}}
	links := multiLocationLinks(t, locations, hosts)
	if remark, _ := linkRemarkAndHost(t, links[1]); remark != "🇫🇮 Finland | fast" {
		t.Fatalf("placeholder remark = %q", remark)
	}

	// One node: exactly the classic single link, no prefix.
	links = multiLocationLinks(t, locations[:1], []Host{{ID: 1, InboundTag: "VLESS", Remark: "Vision", Address: "{SERVER_IP}", Security: "inbound_default", ServiceIDs: []int64{1}}})
	if len(links) != 1 {
		t.Fatalf("single node must keep one link, got %v", links)
	}
	if remark, host := linkRemarkAndHost(t, links[0]); remark != "Vision" || host != "198.51.100.1:443" {
		t.Fatalf("single node link = %q @ %q", remark, host)
	}
}

func TestLocationLabelAddsCountryFlags(t *testing.T) {
	for name, want := range map[string]string{
		"Germany":          "🇩🇪 Germany",
		"Alman 2":          "🇩🇪 Alman 2",
		"سرور آلمان":       "🇩🇪 سرور آلمان",
		"hetzner-helsinki": "🇫🇮 hetzner-helsinki",
		"NL Amsterdam":     "🇳🇱 NL Amsterdam",
		"🇺🇸 USA":           "🇺🇸 USA",
		"king":             "king",
		"checkout-node":    "checkout-node",
		"russian-bridge":   "russian-bridge",
		"":                 "203.0.113.1",
	} {
		if got := locationLabel(name, "203.0.113.1"); got != want {
			t.Errorf("locationLabel(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestConfigLocationsListsSharedConfigNodes(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "locations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE nodes (id INTEGER PRIMARY KEY, name TEXT, address TEXT, status TEXT, xray_config_mode TEXT);
INSERT INTO nodes VALUES
 (1, 'Germany', '203.0.113.10', 'connected', 'default'),
 (2, 'Finland', '203.0.113.20', 'error', NULL),
 (3, 'custom box', '203.0.113.30', 'connected', 'custom'),
 (4, 'old', '203.0.113.40', 'deleted', 'default'),
 (5, 'off', '203.0.113.50', 'disabled', 'default'),
 (6, 'no address', '  ', 'connected', 'default');`); err != nil {
		t.Fatal(err)
	}
	locations := Repository{db: db}.configLocations(context.Background())
	var got []string
	for _, location := range locations {
		got = append(got, location.Name+"@"+location.Address)
	}
	if strings.Join(got, ",") != "🇩🇪 Germany@203.0.113.10,🇫🇮 Finland@203.0.113.20" {
		t.Fatalf("locations = %v", got)
	}
}
