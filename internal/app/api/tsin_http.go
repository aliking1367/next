package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The HTTP configs (xrayconfig/tsin_http.go) are kept in their own columns,
// like the Vision ones, so a read failure leaves every other T.Sin setting as
// it was.

// loadTSinHTTP reads the countries with an HTTP config and the site those
// configs name; any failure reads as none and the default site.
func (s *Server) loadTSinHTTP(ctx context.Context) ([]string, string) {
	var raw, host string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(http_countries, ''), COALESCE(http_host, '') FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&raw, &host); err != nil {
		return []string{}, xrayconfig.DefaultTSinHTTPHost
	}
	countries := []string{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &countries)
	}
	if strings.TrimSpace(host) == "" {
		host = xrayconfig.DefaultTSinHTTPHost
	}
	return countries, host
}

func (s *Server) saveTSinHTTP(ctx context.Context, countries []string, host string) error {
	if countries == nil {
		countries = []string{}
	}
	encoded, err := json.Marshal(countries)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tsin_settings SET http_countries = ?, http_host = ?`, string(encoded), host)
	return err
}
