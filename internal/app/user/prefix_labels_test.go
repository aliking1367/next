package user

import "testing"

// The label is part of the key each link is stored under, so two origins
// sharing one would not merely look alike: one would overwrite the other and
// that origin's links would never reach the user.
func TestPrefixLabelsNameOriginsByHost(t *testing.T) {
	// The case that was broken: separate hostnames on the same port were every
	// one of them labelled "2053".
	got := prefixLabels([]string{
		"https://e4758aa39a97bdca.fandak.ru:2053",
		"https://backup.example.net:2053",
	})
	want := []string{"e4758aa39a97bdca.fandak.ru", "backup.example.net"}
	assertLabels(t, got, want)
}

// The older arrangement -- one host reached on several ports -- still has to
// produce distinct labels, so the port is added where the host repeats.
func TestPrefixLabelsAddThePortWhenTheHostRepeats(t *testing.T) {
	got := prefixLabels([]string{
		"https://panel.example.com:2053",
		"https://panel.example.com:8443",
		"https://other.example.com:2053",
	})
	want := []string{"panel.example.com:2053", "panel.example.com:8443", "other.example.com"}
	assertLabels(t, got, want)
}

func TestPrefixLabelsStayUniqueWhenEvenThePortRepeats(t *testing.T) {
	got := prefixLabels([]string{
		"https://panel.example.com:2053",
		"https://panel.example.com:2053",
	})
	if got[0] == got[1] {
		t.Fatalf("labels collided: %v", got)
	}
}

func TestPrefixLabelsFallBackToTheWholePrefix(t *testing.T) {
	got := prefixLabels([]string{"not a url"})
	if len(got) != 1 || got[0] == "" {
		t.Fatalf("got %v, want the prefix itself", got)
	}
}

func TestPrefixLabelsHandleAnEmptyList(t *testing.T) {
	if got := prefixLabels(nil); len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}

func assertLabels(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
