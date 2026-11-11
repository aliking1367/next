package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
)

// The operator ranges tell which mobile operator a subscription request came
// from, so the emergency tunnel can be shown only to Hamrah Aval (MCI) users,
// whose network the tunnel is bought for. MCI's ranges are the prefixes its
// network (AS197207) announces; Iran's are the country's registered ranges,
// to tell an Iranian network that is not MCI from a request made through a
// VPN. Both come from RIPE's public data once a day and are cached in the
// database, so a restart does not depend on RIPE answering.

const (
	operatorMCI     = "mci"
	operatorIran    = "ir"
	operatorForeign = "foreign"

	mciASN                = "AS197207"
	operatorRangesRefresh = 24 * time.Hour
	ripeStatBase          = "https://stat.ripe.net/data"
)

type operatorRangesData struct {
	MCI       []string  `json:"mci"`
	Iran      []string  `json:"ir"`
	FetchedAt time.Time `json:"fetched_at"`
}

type addrRange struct{ from, to netip.Addr }

type operatorRangesState struct {
	mu        sync.RWMutex
	mci       []netip.Prefix
	iran      []addrRange
	fetchedAt time.Time
}

// parseOperatorRanges turns RIPE's entries (CIDR prefixes, or "a-b" ranges for
// Iran's IPv4) into what a lookup needs. Entries it cannot read are skipped.
func parseMobileRanges(data operatorRangesData) ([]netip.Prefix, []addrRange) {
	mci := make([]netip.Prefix, 0, len(data.MCI))
	for _, item := range data.MCI {
		if prefix, err := netip.ParsePrefix(strings.TrimSpace(item)); err == nil {
			mci = append(mci, prefix.Masked())
		}
	}
	iran := make([]addrRange, 0, len(data.Iran))
	for _, item := range data.Iran {
		item = strings.TrimSpace(item)
		if from, to, ok := strings.Cut(item, "-"); ok {
			a, errA := netip.ParseAddr(strings.TrimSpace(from))
			b, errB := netip.ParseAddr(strings.TrimSpace(to))
			if errA == nil && errB == nil {
				iran = append(iran, addrRange{a, b})
			}
			continue
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			prefix = prefix.Masked()
			iran = append(iran, addrRange{prefix.Addr(), lastAddr(prefix)})
		}
	}
	return mci, iran
}

// lastAddr is the highest address in a prefix.
func lastAddr(prefix netip.Prefix) netip.Addr {
	bytes := prefix.Addr().AsSlice()
	for bit := prefix.Bits(); bit < len(bytes)*8; bit++ {
		bytes[bit/8] |= 0x80 >> (bit % 8)
	}
	addr, _ := netip.AddrFromSlice(bytes)
	return addr
}

func (st *operatorRangesState) set(data operatorRangesData) {
	mci, iran := parseMobileRanges(data)
	st.mu.Lock()
	st.mci, st.iran, st.fetchedAt = mci, iran, data.FetchedAt
	st.mu.Unlock()
}

// classify names the network an address belongs to: "mci", "ir" (Iranian,
// not MCI), "foreign", or "" while the ranges are not known yet.
func (st *operatorRangesState) classify(address string) string {
	addr, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	st.mu.RLock()
	defer st.mu.RUnlock()
	if len(st.mci) == 0 || len(st.iran) == 0 {
		return ""
	}
	for _, prefix := range st.mci {
		if prefix.Contains(addr) {
			return operatorMCI
		}
	}
	for _, item := range st.iran {
		if item.from.BitLen() == addr.BitLen() && addr.Compare(item.from) >= 0 && addr.Compare(item.to) <= 0 {
			return operatorIran
		}
	}
	return operatorForeign
}

func (st *operatorRangesState) summary() map[string]any {
	st.mu.RLock()
	defer st.mu.RUnlock()
	view := map[string]any{"mci_ranges": len(st.mci), "iran_ranges": len(st.iran)}
	if !st.fetchedAt.IsZero() {
		view["fetched_at"] = st.fetchedAt.UTC().Format(time.RFC3339)
	}
	return view
}

// fetchOperatorRanges reads MCI's announced prefixes and Iran's ranges from
// RIPE. It fails rather than return a partial list.
func fetchOperatorRanges(ctx context.Context, client *http.Client, base string) (operatorRangesData, error) {
	getJSON := func(path string, out any) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("RIPE answered HTTP %d", response.StatusCode)
		}
		return json.NewDecoder(response.Body).Decode(out)
	}
	var announced struct {
		Data struct {
			Prefixes []struct {
				Prefix string `json:"prefix"`
			} `json:"prefixes"`
		} `json:"data"`
	}
	if err := getJSON("/announced-prefixes/data.json?resource="+mciASN, &announced); err != nil {
		return operatorRangesData{}, err
	}
	var country struct {
		Data struct {
			Resources struct {
				IPv4 []string `json:"ipv4"`
				IPv6 []string `json:"ipv6"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := getJSON("/country-resource-list/data.json?resource=IR", &country); err != nil {
		return operatorRangesData{}, err
	}
	data := operatorRangesData{FetchedAt: time.Now().UTC()}
	for _, item := range announced.Data.Prefixes {
		data.MCI = append(data.MCI, item.Prefix)
	}
	data.Iran = append(append(data.Iran, country.Data.Resources.IPv4...), country.Data.Resources.IPv6...)
	// Real data has hundreds of each; a near-empty answer is an outage, and
	// using it would hide the tunnel from every MCI user.
	if len(data.MCI) < 50 || len(data.Iran) < 200 {
		return operatorRangesData{}, fmt.Errorf("RIPE returned too few ranges (MCI %d, Iran %d)", len(data.MCI), len(data.Iran))
	}
	return data, nil
}

// runOperatorRangesRefresher loads the cached ranges, then refreshes them from
// RIPE when they are a day old. A failed refresh keeps the ranges it has.
func (s *Server) runOperatorRangesRefresher(ctx context.Context) {
	s.loadCachedOperatorRanges(ctx)
	for {
		s.mobileRanges.mu.RLock()
		age := time.Since(s.mobileRanges.fetchedAt)
		s.mobileRanges.mu.RUnlock()
		wait := operatorRangesRefresh - age
		if wait <= 0 {
			if err := s.refreshOperatorRanges(ctx); err != nil {
				logging.Warnf(logging.ComponentNode, "operator ranges: %v", err)
				wait = time.Hour
			} else {
				wait = operatorRangesRefresh
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (s *Server) loadCachedOperatorRanges(ctx context.Context) {
	var raw *string
	if err := s.db.QueryRowContext(ctx, `SELECT operator_ranges FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&raw); err != nil || raw == nil {
		return
	}
	var data operatorRangesData
	if json.Unmarshal([]byte(*raw), &data) == nil {
		s.mobileRanges.set(data)
	}
}

func (s *Server) refreshOperatorRanges(ctx context.Context) error {
	fetchCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	data, err := fetchOperatorRanges(fetchCtx, http.DefaultClient, ripeStatBase)
	if err != nil {
		return err
	}
	s.mobileRanges.set(data)
	encoded, _ := json.Marshal(data)
	// Only cached when tsin_settings has its row; without T.Sin the ranges
	// are not used anyway.
	_, err = s.db.ExecContext(ctx, `UPDATE tsin_settings SET operator_ranges = ?`, string(encoded))
	return err
}
