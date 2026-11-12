package xrayconfig

import "testing"

// Random inbound ports stay out of Linux's default range for outgoing
// connections (32768-60999), where a busy server's own connections could hold
// the port and make Xray fail to bind it.
func TestRandomInboundPortsStayBelowTheEphemeralRange(t *testing.T) {
	if autoInboundMaxPort >= 32768 {
		t.Fatalf("autoInboundMaxPort = %d reaches the ephemeral range", autoInboundMaxPort)
	}
	for i := 0; i < 500; i++ {
		port, err := pickProvisionPort(nil, map[int]bool{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if port < autoInboundMinPort || port > autoInboundMaxPort {
			t.Fatalf("picked port %d outside %d-%d", port, autoInboundMinPort, autoInboundMaxPort)
		}
	}
}
