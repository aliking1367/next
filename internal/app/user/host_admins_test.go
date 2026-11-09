package user

import "testing"

// A disabled host still reaches the users of the admins it lists, and only
// them; an enabled host reaches everyone as before.
func TestSelectConfigHostsShowsAHiddenHostToTheChosenAdminsUsers(t *testing.T) {
	service := int64(1)
	hosts := []Host{
		{ID: 1, InboundTag: "tsin-germany", ServiceIDs: []int64{1}},
		{ID: 2, InboundTag: "emergency-vmess", ServiceIDs: []int64{1}, IsDisabled: true, EnabledAdmins: []int64{5}},
		{ID: 3, InboundTag: "emergency-ss", ServiceIDs: []int64{1}, IsDisabled: true},
	}
	tags := func(admin *int64) []string {
		var out []string
		for _, item := range selectConfigHosts(hosts, &service, admin) {
			out = append(out, item.host.InboundTag)
		}
		return out
	}
	five, six := int64(5), int64(6)
	if got := tags(&five); len(got) != 2 || got[1] != "emergency-vmess" {
		t.Fatalf("admin 5's user got %v", got)
	}
	if got := tags(&six); len(got) != 1 {
		t.Fatalf("admin 6's user got %v", got)
	}
	if got := tags(nil); len(got) != 1 {
		t.Fatalf("a user without an admin got %v", got)
	}
}
