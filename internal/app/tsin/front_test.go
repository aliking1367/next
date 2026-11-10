package tsin

import (
	"strings"
	"testing"
)

func TestFrontScriptForwardsToTheCountriesServerOnly(t *testing.T) {
	script := FrontScript(" 155.117.5.18 ")
	if !strings.Contains(script, "DEST=155.117.5.18") || !strings.Contains(script, "next-front.service") {
		t.Fatal("the front script lacks its destination or its service")
	}
	if !strings.Contains(script, "Run this on the NEW front server") {
		t.Fatal("the script must refuse to run on the countries' server itself")
	}
	for _, bad := range []string{"", "example.com", "1.2.3.4; rm -rf /", "2001:db8::1"} {
		if FrontScript(bad) != "" {
			t.Errorf("%q made a script", bad)
		}
	}
}
