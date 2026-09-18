package xrayconfig

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed default_catalog.json
var embeddedCatalogRaw []byte

// catalogSourceURL points at the project's own repository, so a fetch only
// ever pulls from a source the panel's own maintainers control — never an
// arbitrary or user-suppliable URL. It lets the auto-provision protocol
// choices (e.g. REALITY masquerade domains) be refreshed after release
// without shipping a new binary.
const catalogSourceURL = "https://raw.githubusercontent.com/aliking1367/next/master/protocol-catalog.json"

const catalogFetchTimeout = 4 * time.Second
const catalogCacheTTL = time.Hour

// RealityDestOption is one candidate REALITY masquerade target.
type RealityDestOption struct {
	Dest        string   `json:"dest"`
	ServerNames []string `json:"server_names"`
}

// Catalog is the auto-provisioning protocol recipe data: pools of choices to
// pick from so that not every installation ends up with an identical,
// fleet-wide-fingerprintable configuration.
type Catalog struct {
	Version     int                 `json:"version"`
	UpdatedAt   string              `json:"updated_at"`
	RealityPool []RealityDestOption `json:"reality_pool"`
}

func (c Catalog) valid() bool {
	if len(c.RealityPool) == 0 {
		return false
	}
	for _, option := range c.RealityPool {
		if strings.TrimSpace(option.Dest) == "" || len(option.ServerNames) == 0 {
			return false
		}
	}
	return true
}

func defaultCatalog() Catalog {
	var catalog Catalog
	if err := json.Unmarshal(embeddedCatalogRaw, &catalog); err != nil || !catalog.valid() {
		// The embedded file is validated by TestDefaultCatalogEmbedsValidData;
		// this is an unreachable-in-practice last resort, not user-facing config.
		return Catalog{RealityPool: []RealityDestOption{
			{Dest: "www.microsoft.com:443", ServerNames: []string{"www.microsoft.com"}},
		}}
	}
	return catalog
}

var (
	catalogCacheMu   sync.Mutex
	catalogCache     Catalog
	catalogCacheTime time.Time
)

// loadCatalog returns the freshest catalog available: a recently cached or
// newly fetched copy from the project repository, falling back to the
// binary's embedded defaults whenever fetching is disabled, the network is
// unavailable, or the fetched data doesn't validate. It never blocks
// provisioning on network access — any failure falls back immediately.
func (r Repository) loadCatalog(ctx context.Context) Catalog {
	if r.options.DisableCatalogFetch {
		return defaultCatalog()
	}
	catalogCacheMu.Lock()
	if !catalogCacheTime.IsZero() && time.Since(catalogCacheTime) < catalogCacheTTL {
		cached := catalogCache
		catalogCacheMu.Unlock()
		return cached
	}
	catalogCacheMu.Unlock()

	fetched, err := catalogFetcher(ctx)
	if err != nil || !fetched.valid() {
		return defaultCatalog()
	}

	catalogCacheMu.Lock()
	catalogCache = fetched
	catalogCacheTime = time.Now()
	catalogCacheMu.Unlock()
	return fetched
}

// catalogFetcher is a seam for tests to avoid real network access; production
// code always uses fetchCatalog.
var catalogFetcher = fetchCatalog

func fetchCatalog(ctx context.Context) (Catalog, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, catalogFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, catalogSourceURL, nil)
	if err != nil {
		return Catalog{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Catalog{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Catalog{}, fmt.Errorf("catalog fetch: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	if err := json.Unmarshal(body, &catalog); err != nil {
		return Catalog{}, err
	}
	if !catalog.valid() {
		return Catalog{}, fmt.Errorf("catalog fetch: invalid catalog data")
	}
	return catalog, nil
}

// pickRealityDest returns a random option from the pool, excluding any
// dest already in `exclude` when the pool is large enough to do so — so
// e.g. the VLESS and Trojan REALITY inbounds in one auto-provision run
// don't end up masquerading as the same site.
func pickRealityDest(pool []RealityDestOption, exclude map[string]bool) RealityDestOption {
	candidates := pool
	if exclude != nil && len(pool) > len(exclude) {
		filtered := make([]RealityDestOption, 0, len(pool))
		for _, option := range pool {
			if !exclude[option.Dest] {
				filtered = append(filtered, option)
			}
		}
		if len(filtered) > 0 {
			candidates = filtered
		}
	}
	index, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return candidates[0]
	}
	return candidates[index.Int64()]
}
