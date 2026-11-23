package api

import (
	"context"
	"errors"
	"strings"

	"github.com/aliking1367/next/internal/app/tsin"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// A T.Sin licence works on one server only. With a second licence on a second
// server, the direct configs (⚡, 💎 and HTTP) run there and leave through that
// server's own tunnel, while every other config stays with the countries'
// server and its licence. The direct target is that second server; empty keeps
// everything on one server, as before.

// loadTSinDirectTarget reads the direct configs' server; "" is none, and so is
// any failure.
func (s *Server) loadTSinDirectTarget(ctx context.Context) string {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(direct_target, '') FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&raw); err != nil {
		return ""
	}
	target, err := normalizeTSinDirectTarget(raw)
	if err != nil {
		return ""
	}
	return target
}

func (s *Server) saveTSinDirectTarget(ctx context.Context, target string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tsin_settings SET direct_target = ?`, target)
	return err
}

// normalizeTSinDirectTarget accepts "" (none) or a node, never the master:
// the master's tunnel is the panel's own, the countries' one.
func normalizeTSinDirectTarget(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "none" {
		return "", nil
	}
	target, err := normalizeTSinTarget(value)
	if err != nil {
		return "", err
	}
	if target == xrayconfig.MasterTargetID {
		return "", errTSinDirectTargetMaster
	}
	return target, nil
}

var errTSinDirectTargetMaster = errors.New("the direct configs' server must be a node with its own T.Sin licence, not the panel server")

// tsinDirectLicenceView adds the second licence as its server last reported
// it: T.Sin shows an account only to the server holding it, so the report is
// the only way the panel sees it.
func (s *Server) tsinDirectLicenceView(settings tsinSettings, view map[string]any) {
	target, _ := view["direct_target"].(string)
	if target == "" {
		return
	}
	licence := map[string]any{"target": target, "reported": false}
	view["direct_licence"] = licence
	report := s.tsinNode.load(target)
	if report == nil {
		return
	}
	licence["reported"] = true
	var status *tsin.Status
	if len(report.Account) > 0 {
		if parsed, err := tsin.ParseStatus(report.Account); err == nil {
			status = &parsed
			licence["account"] = parsed
		} else {
			licence["account_error"] = err.Error()
		}
	} else {
		licence["account_error"] = firstNonEmpty(report.AccountError, "the server got no account details from T.Sin")
	}
	licence["node_report"] = tsinNodeView(report, status, settings.Countries)
	licence["tunnel"] = map[string]any{"up": report.fresh() && report.tunnelUp(), "handshake_age": report.HandshakeAge}
}
