package xrayconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultCatalogEmbedsValidData(t *testing.T) {
	catalog := defaultCatalog()
	if !catalog.valid() {
		t.Fatalf("embedded default catalog is not valid: %#v", catalog)
	}
	if len(catalog.RealityPool) < 2 {
		t.Errorf("expected at least 2 REALITY options so vless and trojan can differ, got %d", len(catalog.RealityPool))
	}
	for _, option := range catalog.RealityPool {
		if option.Dest == "" {
			t.Errorf("option with empty dest: %#v", option)
		}
		if err := validateHostPortTarget(option.Dest); err != nil {
			t.Errorf("dest %q is not a valid host:port target: %v", option.Dest, err)
		}
		for _, name := range option.ServerNames {
			if err := validateServerNameValue(name); err != nil {
				t.Errorf("serverName %q is not valid: %v", name, err)
			}
		}
	}
}

// The repository copy is what every installed panel fetches at runtime, so a
// malformed edit there would silently degrade every deployment back to the
// embedded defaults. Keep it parseable and valid.
func TestRepositoryCatalogFileMatchesEmbeddedSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "protocol-catalog.json"))
	if err != nil {
		t.Fatalf("read protocol-catalog.json: %v", err)
	}
	var catalog Catalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("protocol-catalog.json is not valid JSON for the catalog schema: %v", err)
	}
	if !catalog.valid() {
		t.Fatalf("protocol-catalog.json failed validation: %#v", catalog)
	}
	for _, option := range catalog.RealityPool {
		if err := validateHostPortTarget(option.Dest); err != nil {
			t.Errorf("dest %q is not a valid host:port target: %v", option.Dest, err)
		}
		for _, name := range option.ServerNames {
			if err := validateServerNameValue(name); err != nil {
				t.Errorf("serverName %q is not valid: %v", name, err)
			}
		}
	}
}

func TestLoadCatalogFallsBackToEmbeddedWhenFetchFails(t *testing.T) {
	original := catalogFetcher
	catalogFetcher = func(context.Context) (Catalog, error) {
		return Catalog{}, errors.New("offline")
	}
	catalogCacheMu.Lock()
	catalogCacheTime = time.Time{}
	catalogCacheMu.Unlock()
	t.Cleanup(func() {
		catalogFetcher = original
		catalogCacheMu.Lock()
		catalogCacheTime = time.Time{}
		catalogCacheMu.Unlock()
	})

	catalog := NewRepository(nil, "sqlite", Options{}).loadCatalog(context.Background())
	if !catalog.valid() {
		t.Fatalf("expected a valid fallback catalog, got %#v", catalog)
	}
}

func TestLoadCatalogRejectsInvalidFetchedData(t *testing.T) {
	original := catalogFetcher
	catalogFetcher = func(context.Context) (Catalog, error) {
		// Structurally decodable but semantically useless: an entry with no
		// serverNames would produce an inbound Xray rejects.
		return Catalog{RealityPool: []RealityDestOption{{Dest: "example.com:443"}}}, nil
	}
	catalogCacheMu.Lock()
	catalogCacheTime = time.Time{}
	catalogCacheMu.Unlock()
	t.Cleanup(func() {
		catalogFetcher = original
		catalogCacheMu.Lock()
		catalogCacheTime = time.Time{}
		catalogCacheMu.Unlock()
	})

	catalog := NewRepository(nil, "sqlite", Options{}).loadCatalog(context.Background())
	if len(catalog.RealityPool) == 1 && catalog.RealityPool[0].Dest == "example.com:443" {
		t.Fatal("invalid fetched catalog was used instead of the embedded fallback")
	}
	if !catalog.valid() {
		t.Fatalf("expected a valid fallback catalog, got %#v", catalog)
	}
}

func TestPickRealityDestAvoidsAlreadyUsedTargets(t *testing.T) {
	pool := []RealityDestOption{
		{Dest: "a.example:443", ServerNames: []string{"a.example"}},
		{Dest: "b.example:443", ServerNames: []string{"b.example"}},
	}
	used := map[string]bool{"a.example:443": true}
	for i := 0; i < 20; i++ {
		if got := pickRealityDest(pool, used); got.Dest != "b.example:443" {
			t.Fatalf("expected the unused option, got %q", got.Dest)
		}
	}
}

func TestPickRealityDestFallsBackWhenEverythingIsUsed(t *testing.T) {
	pool := []RealityDestOption{{Dest: "a.example:443", ServerNames: []string{"a.example"}}}
	used := map[string]bool{"a.example:443": true}
	if got := pickRealityDest(pool, used); got.Dest != "a.example:443" {
		t.Fatalf("expected the only option even though it is used, got %q", got.Dest)
	}
}

func TestPickRealityDestSpreadsAcrossThePool(t *testing.T) {
	pool := defaultCatalog().RealityPool
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		seen[pickRealityDest(pool, nil).Dest] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected picks to vary across installations, only ever saw %v", seen)
	}
}
