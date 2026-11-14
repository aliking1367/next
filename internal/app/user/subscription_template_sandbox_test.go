package user

import (
	"strings"
	"testing"
)

// A subscription page template is written by an admin and rendered for every
// visitor; it must not be able to read files on the server.
func TestSubscriptionTemplatesCannotReadServerFiles(t *testing.T) {
	for _, content := range []string{
		`{% ssi "/etc/passwd" %}`,
		`{% include "/etc/passwd" %}`,
		`{% extends "/etc/passwd" %}`,
		`{% import "/etc/passwd" x %}`,
	} {
		out, err := renderSubscriptionPageTemplate(content, UserDetail{}, nil, "#", "#", "t")
		if err == nil && strings.Contains(out, "root:") {
			t.Fatalf("%s read a file", content)
		}
		if err == nil {
			t.Fatalf("%s was accepted", content)
		}
	}
	out, err := renderSubscriptionPageTemplate(`<p>{{ "<b>x</b>" }}</p>`, UserDetail{}, nil, "#", "#", "t")
	if err != nil || strings.Contains(out, "<b>") {
		t.Fatalf("ordinary template broke or did not escape: %q %v", out, err)
	}
}
