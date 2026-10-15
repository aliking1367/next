package user

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bundled subscription pages are hand-edited HTML with template tags in
// them, and they are read from disk at runtime rather than compiled in. A typo
// in one would otherwise reach a user's page before anyone noticed, so every
// one of them is rendered here.
func TestEveryBundledSubscriptionPageRenders(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "templates", "subscription")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("bundled templates are not available here: %v", err)
	}
	rendered := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			page, err := RenderSubscriptionPagePreview(string(content))
			if err != nil {
				t.Fatalf("%s does not render: %v", entry.Name(), err)
			}
			if strings.TrimSpace(page) == "" {
				t.Fatalf("%s rendered empty", entry.Name())
			}
		})
		rendered++
	}
	if rendered == 0 {
		t.Fatal("no bundled template was rendered, so this test proves nothing")
	}
}

// Handing a user one link has to hand them the spare as well: no client fails
// over between subscription addresses, so a spare the user never saw is a
// spare they will not have when the first one stops opening.
func TestBundledPagesShowTheBackupAddresses(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "templates", "subscription")
	for _, name := range []string{"pearl.html", "midnight.html", "ember.html"} {
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Skipf("template not available: %v", err)
			}
			page, err := RenderSubscriptionPagePreview(string(content))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"https://backup.example.net/sub/demo",
				"https://7f3a9c1e.example.com/sub/demo",
			} {
				if !strings.Contains(page, want) {
					t.Fatalf("%s does not show the backup address %q", name, want)
				}
			}
		})
	}
}
