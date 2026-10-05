package xrayconfig

import (
	"strings"
	"testing"
)

func TestSelectableRecipesAreTheOnesAnAdminChoosesBetween(t *testing.T) {
	names := SelectableRecipes()
	want := []string{
		RecipeRealityVision, RecipeRealityXHTTP, RecipeHysteria2,
		RecipeCDNXHTTP, RecipeCDNHTTPUpgrade, RecipeDirectTCP,
	}
	if len(names) != len(want) {
		t.Fatalf("SelectableRecipes() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("SelectableRecipes() = %v, want %v", names, want)
		}
	}
	// These two are gated on inputs of their own, so offering them as a
	// checkbox would let an admin tick a box that does nothing.
	for _, name := range names {
		if name == RecipeAmneziaWG || name == RecipeFastlyWS {
			t.Fatalf("%s should not be selectable on its own", name)
		}
	}
}

func TestNormalizeRecipeSelectionAcceptsWhatTheDashboardSends(t *testing.T) {
	selected, err := normalizeRecipeSelection([]string{" CDN-XHTTP ", RecipeRealityVision, ""})
	if err != nil {
		t.Fatal(err)
	}
	if !selected[RecipeCDNXHTTP] || !selected[RecipeRealityVision] {
		t.Fatalf("selection = %v", selected)
	}
	if selected[RecipeHysteria2] {
		t.Fatal("hysteria2 was not asked for")
	}
}

// Empty means "everything", which is what every caller before this change got.
func TestNormalizeRecipeSelectionEmptyMeansAll(t *testing.T) {
	selected, err := normalizeRecipeSelection(nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected != nil {
		t.Fatalf("expected nil (meaning all), got %v", selected)
	}
}

// A typo must be refused by name. Silently building nothing looks exactly like
// the feature being broken.
func TestNormalizeRecipeSelectionRefusesUnknownNames(t *testing.T) {
	_, err := normalizeRecipeSelection([]string{"reality-vision", "cdn-xhtp"})
	if err == nil {
		t.Fatal("expected an error for a misspelled recipe")
	}
	if !strings.Contains(err.Error(), "cdn-xhtp") {
		t.Fatalf("the error should name the offender: %v", err)
	}
}

func TestNormalizeRecipeSelectionRefusesABlankSelection(t *testing.T) {
	if _, err := normalizeRecipeSelection([]string{"", "   "}); err == nil {
		t.Fatal("a selection of nothing but blanks should be refused")
	}
}

// An empty selection builds the default set, which must leave out the recipe
// that ships without TLS. A fallback with a real downside is one an admin
// chooses, not one an upgrade turns on for them.
func TestDefaultRecipesExcludeTheOptInOnes(t *testing.T) {
	for _, name := range DefaultRecipes() {
		if name == RecipeDirectTCP {
			t.Fatal("direct-tcp must not be in the default set")
		}
	}
	found := false
	for _, name := range DefaultRecipes() {
		if name == RecipeCDNHTTPUpgrade {
			found = true
		}
	}
	if !found {
		t.Fatal("cdn-httpupgrade is free to add and should be built by default")
	}
}

// The dashboard renders its checkboxes from SelectableRecipes, so every id it
// can send has to be one the panel accepts. v1.23.0 shipped a dashboard that
// sent "hysteria2" while the recipe was "hysteria2-obfs", and every narrowed
// selection keeping Hysteria2 was refused.
func TestEverySelectableRecipeIsAccepted(t *testing.T) {
	for _, name := range SelectableRecipes() {
		selected, err := normalizeRecipeSelection([]string{name})
		if err != nil {
			t.Fatalf("SelectableRecipes offers %q but the panel refuses it: %v", name, err)
		}
		if !selected[name] {
			t.Fatalf("%q was accepted but not selected", name)
		}
	}
}

func TestFingerprintAndALPNFallBackRatherThanWriteNonsense(t *testing.T) {
	// Xray refuses a fingerprint it does not know, and the config would reach
	// users looking normal and never connecting.
	if got := fingerprintOrDefault("not-a-browser"); got != "chrome" {
		t.Fatalf("fingerprintOrDefault = %q, want chrome", got)
	}
	if got := fingerprintOrDefault(" FireFox "); got != "firefox" {
		t.Fatalf("fingerprintOrDefault = %q, want firefox", got)
	}
	if got := alpnOrDefault("", "h2"); got != "h2" {
		t.Fatalf("alpnOrDefault = %q, want the fallback", got)
	}
	if got := alpnOrDefault("h3,h2,http/1.1", "h2"); got != "h3,h2,http/1.1" {
		t.Fatalf("alpnOrDefault = %q", got)
	}
	if got := alpnOrDefault("nonsense", "h2"); got != "h2" {
		t.Fatalf("alpnOrDefault = %q, want the fallback", got)
	}
}
