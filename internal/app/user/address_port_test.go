package user

import "testing"

// A tunnel provider commonly hands out one hostname and a port per
// destination, which no single host row can express: the row has one port and
// is offered for every node. So the port may travel with the address.
func TestSplitAddressPortTakesAPortFromTheAddress(t *testing.T) {
	cases := []struct {
		address string
		host    string
		port    int
	}{
		{"ar3.portimap.ir:8531", "ar3.portimap.ir", 8531},
		{"  tr.example.com:443  ", "tr.example.com", 443},
		{"203.0.113.10:8530", "203.0.113.10", 8530},
		// A bracketed IPv6 literal with a port.
		{"[2001:db8::1]:8530", "[2001:db8::1]", 8530},
	}
	for _, testCase := range cases {
		t.Run(testCase.address, func(t *testing.T) {
			host, port, ok := splitAddressPort(testCase.address)
			if !ok {
				t.Fatalf("splitAddressPort(%q) did not split", testCase.address)
			}
			if host != testCase.host || port != testCase.port {
				t.Fatalf("got %q:%d, want %q:%d", host, port, testCase.host, testCase.port)
			}
		})
	}
}

// A bare IPv6 literal is full of colons. Reading one as a host and a port would
// hand every IPv6 node a truncated address and a nonsense port -- a config that
// looks fine and reaches nothing.
func TestSplitAddressPortLeavesEverythingElseAlone(t *testing.T) {
	for _, address := range []string{
		"ar3.portimap.ir",
		"203.0.113.10",
		"2001:db8::1",
		"2001:19f0:5001:2904:5400:6ff:fec7:9e6f",
		"[2001:db8::1]",
		"",
		"   ",
		// Not a port.
		"example.com:",
		"example.com:http",
		"example.com:0",
		"example.com:70000",
		":8530",
	} {
		t.Run(address, func(t *testing.T) {
			if host, port, ok := splitAddressPort(address); ok {
				t.Fatalf("splitAddressPort(%q) split into %q:%d", address, host, port)
			}
		})
	}
}
