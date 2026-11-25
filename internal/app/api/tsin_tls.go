package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The TLS configs (xrayconfig/tsin_tls.go) keep their settings in their own
// columns, like the Vision and HTTP ones, so a read failure leaves every
// other T.Sin setting as it was.

type tsinTLSSettings struct {
	Countries   []string
	ServerName  string
	MainCountry string
}

// loadTSinTLS reads the countries with a TLS config, the server name and the
// country on port 443; any failure reads as none.
func (s *Server) loadTSinTLS(ctx context.Context) tsinTLSSettings {
	settings := tsinTLSSettings{Countries: []string{}}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(tls_countries, ''), COALESCE(tls_server_name, ''), COALESCE(tls_main_country, '') FROM tsin_settings ORDER BY id LIMIT 1`).
		Scan(&raw, &settings.ServerName, &settings.MainCountry); err != nil {
		return settings
	}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &settings.Countries)
	}
	if settings.Countries == nil {
		settings.Countries = []string{}
	}
	return settings
}

func (s *Server) saveTSinTLS(ctx context.Context, settings tsinTLSSettings) error {
	if settings.Countries == nil {
		settings.Countries = []string{}
	}
	encoded, err := json.Marshal(settings.Countries)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tsin_settings SET tls_countries = ?, tls_server_name = ?, tls_main_country = ?`,
		string(encoded), settings.ServerName, settings.MainCountry)
	return err
}

// tsinTLSCertReady says whether the certificate and key for a server name are
// on this server, where the panel reads them to send to the node.
func tsinTLSCertReady(serverName string) bool {
	if strings.TrimSpace(serverName) == "" {
		return false
	}
	certFile, keyFile := xrayconfig.TSinTLSCertificatePaths(serverName)
	for _, file := range []string{certFile, keyFile} {
		info, err := os.Stat(file)
		if err != nil || info.IsDir() || info.Size() == 0 {
			return false
		}
	}
	return true
}
