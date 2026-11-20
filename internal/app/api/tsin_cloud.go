package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The Cloudflare XHTTP configs (xrayconfig/tsin_cloud.go) keep their settings
// in their own columns, apart from the others, so a read failure leaves every
// other T.Sin setting as it was.

type tsinCloudSettings struct {
	Domain    string   `json:"cloud_domain"`
	Port      int      `json:"cloud_port"`
	Connect   string   `json:"cloud_connect"`
	Countries []string `json:"cloud_countries"`
}

// loadTSinCloud reads the Cloudflare XHTTP settings; any failure reads as
// none.
func (s *Server) loadTSinCloud(ctx context.Context) tsinCloudSettings {
	settings := tsinCloudSettings{Port: xrayconfig.DefaultTSinCloudPort, Countries: []string{}}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(cloud_domain, ''), COALESCE(cloud_port, 0), COALESCE(cloud_connect, ''), COALESCE(cloud_countries, '') FROM tsin_settings ORDER BY id LIMIT 1`).
		Scan(&settings.Domain, &settings.Port, &settings.Connect, &raw); err != nil {
		return tsinCloudSettings{Port: xrayconfig.DefaultTSinCloudPort, Countries: []string{}}
	}
	if settings.Port <= 0 {
		settings.Port = xrayconfig.DefaultTSinCloudPort
	}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &settings.Countries)
	}
	if settings.Countries == nil {
		settings.Countries = []string{}
	}
	return settings
}

func (s *Server) saveTSinCloud(ctx context.Context, settings tsinCloudSettings) error {
	if settings.Countries == nil {
		settings.Countries = []string{}
	}
	encoded, err := json.Marshal(settings.Countries)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tsin_settings SET cloud_domain = ?, cloud_port = ?, cloud_connect = ?, cloud_countries = ?`,
		settings.Domain, settings.Port, settings.Connect, string(encoded))
	return err
}
