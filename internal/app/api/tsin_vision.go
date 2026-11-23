package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The Vision configs (xrayconfig/tsin_vision.go) are kept in their own
// columns, apart from the other settings, so a read failure leaves every
// other T.Sin setting as it was.

// loadTSinVision reads the countries with a Vision config and the site those
// configs borrow; any failure reads as none and the default site.
func (s *Server) loadTSinVision(ctx context.Context) ([]string, string) {
	var raw, sni string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(vision_countries, ''), COALESCE(vision_sni, '') FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&raw, &sni); err != nil {
		return []string{}, xrayconfig.DefaultTSinVisionSNI
	}
	countries := []string{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &countries)
	}
	if strings.TrimSpace(sni) == "" {
		sni = xrayconfig.DefaultTSinVisionSNI
	}
	return countries, sni
}

func (s *Server) saveTSinVision(ctx context.Context, countries []string, sni string) error {
	if countries == nil {
		countries = []string{}
	}
	encoded, err := json.Marshal(countries)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tsin_settings SET vision_countries = ?, vision_sni = ?`, string(encoded), sni)
	return err
}

// loadTSinCDNHidden says whether the Cloudflare configs of the countries are
// kept out of subscriptions; any failure reads as shown.
func (s *Server) loadTSinCDNHidden(ctx context.Context) bool {
	var hidden sql.NullBool
	if err := s.db.QueryRowContext(ctx, `SELECT cdn_hidden FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&hidden); err != nil {
		return false
	}
	return hidden.Bool
}

func (s *Server) saveTSinCDNHidden(ctx context.Context, hidden bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tsin_settings SET cdn_hidden = ?`, boolToInt(hidden))
	return err
}

// applyTSinCDNHidden shows or hides the countries' Cloudflare configs, the
// ones reached through the gate, by their host rows only: the inbounds stay,
// so nothing else on the server changes, and the other configs of each
// country (tunnel, direct, Vision, Fastly) are left as they are. Users whose
// service loses or regains a config are queued for an update, as when a host
// is switched in the hosts page.
func (s *Server) applyTSinCDNHidden(ctx context.Context, inbounds []xrayconfig.TSinInbound, hidden bool) error {
	tags := []string{}
	for _, inbound := range inbounds {
		if inbound.Direct || inbound.Vision || inbound.HTTP || inbound.Fastly || inbound.Tunnel || inbound.Cloud {
			continue
		}
		tags = append(tags, inbound.Tag)
	}
	if len(tags) == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		serviceSet := map[int64]bool{}
		beforeServiceTags := map[int64]map[string]bool{}
		var hostIDs []int64
		for _, tag := range tags {
			rows, err := tx.QueryContext(ctx, `SELECT id FROM hosts WHERE inbound_tag = ? AND COALESCE(is_disabled, 0) <> ?`, tag, boolToInt(hidden))
			if err != nil {
				return err
			}
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				hostIDs = append(hostIDs, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		if len(hostIDs) == 0 {
			return nil
		}
		for _, id := range hostIDs {
			services, err := serviceIDsForHostTx(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := addAffectedServiceIDsTx(ctx, tx, serviceSet, beforeServiceTags, services); err != nil {
				return err
			}
		}
		for _, id := range hostIDs {
			if _, err := tx.ExecContext(ctx, `UPDATE hosts SET is_disabled = ? WHERE id = ?`, boolToInt(hidden), id); err != nil {
				return err
			}
		}
		changed, err := changedServiceRuntimeInboundSetsTx(ctx, tx, beforeServiceTags, serviceSet)
		if err != nil {
			return err
		}
		return enqueueAffectedServicesUsersTx(ctx, tx, changed)
	})
}
