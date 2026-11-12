package user

import "testing"

func TestHideHostsHidesOnlyTheGivenInbounds(t *testing.T) {
	admin := int64(3)
	hosts := []Host{
		{InboundTag: "emergency-vmess", EnabledAdmins: []int64{admin}},
		{InboundTag: "emergency-ss"},
		{InboundTag: "tsin-germany"},
	}
	got := hideHosts(hosts, emergencyInboundTags)
	if !got[0].IsDisabled || got[0].EnabledAdmins != nil || !got[1].IsDisabled {
		t.Fatalf("emergency hosts not hidden: %#v", got[:2])
	}
	if got[2].IsDisabled {
		t.Fatal("a country config was hidden")
	}
	if hosts[0].IsDisabled || hosts[0].EnabledAdmins == nil {
		t.Fatal("hideHosts changed the caller's hosts")
	}
	if same := hideHosts(hosts, nil); &same[0] != &hosts[0] {
		t.Fatal("with nothing to hide the hosts must pass through")
	}
	selected := selectConfigHosts(got, nil, &admin)
	if len(selected) != 1 || selected[0].host.InboundTag != "tsin-germany" {
		t.Fatalf("selected = %#v", selected)
	}
}

// The countries' tunnel configs follow the emergency tunnel's audience; the
// country's own, Cloudflare and direct configs never do.
func TestHideHostsHidesTheCountriesTunnelConfigs(t *testing.T) {
	hosts := []Host{
		{InboundTag: "tsin-germany-tunnel"},
		{InboundTag: "tsin-best-ping-tunnel"},
		{InboundTag: "tsin-germany"},
		{InboundTag: "tsin-germany-direct"},
		{InboundTag: "other-tunnel"},
	}
	got := hideHosts(hosts, emergencyInboundTags)
	for i, want := range []bool{true, true, false, false, false} {
		if got[i].IsDisabled != want {
			t.Fatalf("%s hidden=%v, want %v", got[i].InboundTag, got[i].IsDisabled, want)
		}
	}
}
