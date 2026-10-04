package xrayconfig

import (
	"strings"
	"testing"
)

func TestSelectableRecipesAreTheOnesAnAdminChoosesBetween(t *testing.T) {
	names := SelectableRecipes()
	want := []string{RecipeRealityVision, RecipeRealityXHTTP, RecipeHysteria2, RecipeCDNXHTTP}
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
