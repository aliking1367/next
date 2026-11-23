package api

import "testing"

func TestNormalizeTSinDirectAddressList(t *testing.T) {
	got, err := normalizeTSinDirectAddress(" 167.233.180.133, 167.233.184.49\n167.233.180.133 ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "167.233.180.133,167.233.184.49" {
		t.Fatalf("got %q", got)
	}
	if got, err := normalizeTSinDirectAddress("Front.Example.COM"); err != nil || got != "front.example.com" {
		t.Fatalf("single host: %q %v", got, err)
	}
	if got, err := normalizeTSinDirectAddress(""); err != nil || got != "" {
		t.Fatalf("empty: %q %v", got, err)
	}
	if _, err := normalizeTSinDirectAddress("1.2.3.4, bad_host!"); err == nil {
		t.Fatal("a bad entry must be refused")
	}
	if firstTSinAddress("a.example.com,b.example.com") != "a.example.com" {
		t.Fatal("firstTSinAddress")
	}
}

func TestTSinChangedTunnelTargetsOnlyMovesChangedRows(t *testing.T) {
	saved := []tsinTunnelPort{
		{NodePort: 20200, Address: "109.94.164.145", Port: 20200},
		{NodePort: 20201, Address: "95.38.195.122", Port: 20201},
	}
	ports := []tsinTunnelPort{
		{NodePort: 20200, Address: "109.94.164.145", Port: 20200},
		{NodePort: 20201, Address: "95.38.195.122,109.94.164.145", Port: 20201},
	}
	countries := map[string]int{"germany": 20201, "latvia": 20200, "spain": 20201}
	got := tsinChangedTunnelTargets(saved, ports, countries, []string{"Germany", "Latvia", "Spain", "Japan"}, "tunnel.example.ir")
	if len(got) != 2 {
		t.Fatalf("want Germany and Spain only, got %#v", got)
	}
	if got["Germany"].Address != "95.38.195.122,109.94.164.145" || got["Germany"].Port != 20201 {
		t.Fatalf("Germany: %#v", got["Germany"])
	}
	if _, moved := got["Latvia"]; moved {
		t.Fatal("Latvia's row did not change, its config must stay as it is")
	}
	if none := tsinChangedTunnelTargets(saved, saved, countries, []string{"Germany"}, ""); len(none) != 0 {
		t.Fatalf("nothing changed, got %#v", none)
	}
}
