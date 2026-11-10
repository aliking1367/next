package xrayconfig

import (
	"context"
	"testing"
)

func TestSetTSinGateHostMovesOnlyTheGateConfigs(t *testing.T) {
	repo, db := provisionTestRepository(t)
	for _, statement := range []string{
		`DELETE FROM hosts`,
		`INSERT INTO hosts (remark, address, sni, host, inbound_tag) VALUES ('de', 'g1.example.com', 'g1.example.com', 'g1.example.com', 'tsin-germany')`,
		`INSERT INTO hosts (remark, address, sni, host, inbound_tag) VALUES ('nl', 'G1.example.com', 'g1.example.com', 'g1.example.com', 'tsin-the-netherlands')`,
		`INSERT INTO hosts (remark, address, sni, host, inbound_tag) VALUES ('de direct', '203.0.113.7', 'www.lenovo.com', NULL, 'tsin-germany-direct')`,
		`INSERT INTO hosts (remark, address, sni, host, inbound_tag) VALUES ('other', 'g1.example.com', 'g1.example.com', 'g1.example.com', 'auto-cdn-xhttp')`,
		`INSERT INTO hosts (remark, address, sni, host, inbound_tag) VALUES ('sos', 'tun.example.ir', NULL, NULL, 'emergency-vmess')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	moved, err := repo.SetTSinGateHost(context.Background(), "g1.example.com", "G2.Example.com.")
	if err != nil {
		t.Fatal(err)
	}
	if moved != 2 {
		t.Fatalf("moved %d rows, want the two country configs", moved)
	}
	rows, err := db.Query(`SELECT inbound_tag, address, COALESCE(sni, ''), COALESCE(host, '') FROM hosts ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string][3]string{
		"tsin-germany":         {"g2.example.com", "g2.example.com", "g2.example.com"},
		"tsin-the-netherlands": {"g2.example.com", "g2.example.com", "g2.example.com"},
		"tsin-germany-direct":  {"203.0.113.7", "www.lenovo.com", ""},
		"auto-cdn-xhttp":       {"g1.example.com", "g1.example.com", "g1.example.com"},
		"emergency-vmess":      {"tun.example.ir", "", ""},
	}
	for rows.Next() {
		var tag, address, sni, host string
		if err := rows.Scan(&tag, &address, &sni, &host); err != nil {
			t.Fatal(err)
		}
		if got := [3]string{address, sni, host}; got != want[tag] {
			t.Fatalf("%s = %v, want %v", tag, got, want[tag])
		}
	}
	if _, err := repo.SetTSinGateHost(context.Background(), "g1.example.com", "https://bad/path"); err == nil {
		t.Fatal("a URL instead of a domain must be refused")
	}
}
