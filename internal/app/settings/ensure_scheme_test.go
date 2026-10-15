package settings

import "testing"

// Saving the same settings twice has to leave them as they were. The old
// version only recognised a scheme written with both slashes, so a stored
// value that had lost one grew another scheme on every save until the field
// held "https://https:/https:/host" and nothing resolved.
func TestEnsureSchemeIsIdempotent(t *testing.T) {
	for _, value := range []string{
		"https://*.fandak.ru:2053",
		"http://cdn.example.net",
		"https://panel.example.com:2053",
	} {
		t.Run(value, func(t *testing.T) {
			once := ensureScheme(value)
			if once != value {
				t.Fatalf("ensureScheme(%q) = %q, want it unchanged", value, once)
			}
			if twice := ensureScheme(once); twice != once {
				t.Fatalf("a second pass changed it: %q -> %q", once, twice)
			}
			if thrice := ensureScheme(ensureScheme(once)); thrice != once {
				t.Fatalf("a third pass changed it: %q", thrice)
			}
		})
	}
}

func TestEnsureSchemeRepairsAccumulatedSchemes(t *testing.T) {
	cases := map[string]string{
		// Exactly what the live panel ended up storing.
		"https://https:/https:/https:/*.fandak.ru:2053": "https://*.fandak.ru:2053",
		"https://https://cdn.example.net":               "https://cdn.example.net",
		"https:/cdn.example.net":                        "https://cdn.example.net",
		// The scheme the admin wrote is kept, not promoted.
		"http://http:/cdn.example.net": "http://cdn.example.net",
		// A bare origin still gets one.
		"cdn.example.net":  "https://cdn.example.net",
		"*.fandak.ru:2053": "https://*.fandak.ru:2053",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got := ensureScheme(input)
			if got != want {
				t.Fatalf("ensureScheme(%q) = %q, want %q", input, got, want)
			}
			if again := ensureScheme(got); again != want {
				t.Fatalf("repairing twice changed it: %q", again)
			}
		})
	}
}

func TestEnsureSchemeLeavesNothingFromNothing(t *testing.T) {
	for _, value := range []string{"", "   ", "https://", "https:/", "//"} {
		if got := ensureScheme(value); got != "" {
			t.Fatalf("ensureScheme(%q) = %q, want empty", value, got)
		}
	}
}

// The repair has to reach the field the admin actually edits.
func TestNormalizeBackupPrefixesRepairsAndDeduplicates(t *testing.T) {
	got := normalizeBackupPrefixes([]string{
		"https://https:/https:/*.fandak.ru:2053",
		"https://*.fandak.ru:2053/",
		"  ",
		"cdn.example.net",
	})
	want := []string{"https://*.fandak.ru:2053", "https://cdn.example.net"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
