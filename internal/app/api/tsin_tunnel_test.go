package api

import "testing"

func TestCleanTSinTunnelPorts(t *testing.T) {
	got, err := cleanTSinTunnelPorts([]tsinTunnelPort{
		{NodePort: 20101, Address: " Seller2.Example.IR ", Port: 41000},
		{NodePort: 20100, Address: "185.1.2.3"},
		{},
	}, 2087)
	if err != nil || len(got) != 2 || got[0].NodePort != 20100 || got[1].Address != "seller2.example.ir" || got[1].Port != 41000 {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, bad := range [][]tsinTunnelPort{
		{{NodePort: 2087}},
		{{NodePort: 20100}, {NodePort: 20100}},
		{{NodePort: 20100, Address: "https://x.ir/sub"}},
		{{NodePort: 0, Address: "x.ir"}},
		{{NodePort: 20100, Port: 70000}},
	} {
		if _, err := cleanTSinTunnelPorts(bad, 2087); err == nil {
			t.Fatalf("%+v was accepted", bad)
		}
	}
	// One tunnel port carries every country (the gate tells them apart by
	// path); only an absurd number is refused.
	many := map[string]int{}
	for i := 0; i < 19; i++ {
		many["country"+string(rune('a'+i))] = 20100
	}
	if _, err := cleanTSinTunnelCountries(many, 2087); err != nil {
		t.Fatalf("19 countries on one port were refused: %v", err)
	}
	for i := 0; i < 120; i++ {
		many["c"+string(rune(0x100+i))] = 20100
	}
	if _, err := cleanTSinTunnelCountries(many, 2087); err == nil {
		t.Fatal("an absurd number of countries on one port was accepted")
	}
}
