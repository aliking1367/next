package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/aliking1367/next/internal/app/xrayconfig"

	"github.com/aliking1367/next/internal/app/logging"
)

// Keeping a node reachable through a CDN is four things that all have to agree:
// a proxied DNS record for that node, a host row carrying that hostname, the
// same hostname as SNI, and the record pointing at the node's current address.
// Done by hand that is four places to edit every time a node is added, renamed,
// or moved to a fresh server because the old one was blocked -- and the failure
// mode is silent, because a config pointing at a stale record still looks
// perfectly valid to the user holding it.
//
// So the panel keeps the answers once and does the work itself: adding a node
// creates its hostname, changing a node's address re-points it, and removing a
// node takes it away again.

const defaultCDNInboundTag = "auto-cdn-xhttp"

// cdnAutomation is the stored configuration. The token is deliberately absent
// from every response type: automation needs it after the request that supplied
// it has ended, so it is stored, but it is never handed back out.
type cdnAutomation struct {
	Enabled               bool
	DomainSuffix          string
	InboundTag            string
	Token                 string
	RemoveRecordsOnDelete bool
	// ManageSecurityRule lets the panel keep the Cloudflare rule that stops
	// these hostnames being challenged. It is off unless an admin turns it on:
	// it switches a protection off, narrowly, and that is theirs to decide.
	ManageSecurityRule bool
	LastSyncAt         sql.NullTime
	LastSyncDetail     string
}

func (c cdnAutomation) inboundTag() string {
	if tag := strings.TrimSpace(c.InboundTag); tag != "" {
		return tag
	}
	return defaultCDNInboundTag
}

// inboundTags are the inbounds this run gives a hostname per node.
//
// An admin who named one tag gets that one and nothing else; they were
// specific on purpose. Otherwise every CDN-fronted inbound is covered, because
// a second CDN recipe that only ever receives the default host is a set of
// configs pointing at the bare domain, which reaches the panel or nothing at
// all -- and reads to a user as a config that simply does not work.
func (c cdnAutomation) inboundTags() []string {
	if tag := strings.TrimSpace(c.InboundTag); tag != "" {
		return []string{tag}
	}
	tags := xrayconfig.CDNInboundTags()
	if len(tags) == 0 {
		return []string{defaultCDNInboundTag}
	}
	return tags
}

// ready says whether the panel has everything it needs to act on its own.
func (c cdnAutomation) ready() bool {
	return c.Enabled && normalizeCDNSuffix(c.DomainSuffix) != "" && strings.TrimSpace(c.Token) != ""
}

type cdnAutomationResponse struct {
	Enabled               bool   `json:"enabled"`
	DomainSuffix          string `json:"domain_suffix"`
	InboundTag            string `json:"inbound_tag"`
	RemoveRecordsOnDelete bool   `json:"remove_records_on_delete"`
	ManageSecurityRule    bool   `json:"manage_security_rule"`
	// TokenSet reports that a token is stored without revealing it, which is
	// what the dashboard needs to show "configured" and nothing more.
	TokenSet       bool   `json:"token_set"`
	Ready          bool   `json:"ready"`
	LastSyncAt     string `json:"last_sync_at,omitempty"`
	LastSyncDetail string `json:"last_sync_detail,omitempty"`
}

type cdnAutomationUpdate struct {
	Enabled               *bool   `json:"enabled"`
	DomainSuffix          *string `json:"domain_suffix"`
	InboundTag            *string `json:"inbound_tag"`
	RemoveRecordsOnDelete *bool   `json:"remove_records_on_delete"`
	ManageSecurityRule    *bool   `json:"manage_security_rule"`
	// CloudflareToken replaces the stored token. Omitted leaves it alone; an
	// empty string clears it, which is how an admin revokes it from here.
	CloudflareToken *string `json:"cloudflare_token"`
	// SyncNow runs a full pass right after saving, so enabling the feature does
	// not also require remembering to press a second button.
	SyncNow bool `json:"sync_now"`
}

func (s *Server) loadCDNAutomation(ctx context.Context) (cdnAutomation, error) {
	var config cdnAutomation
	var enabled, removeOnDelete, manageRule int
	var suffix, tag, token, detail sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT enabled, domain_suffix, inbound_tag, cloudflare_token,
remove_records_on_delete, manage_security_rule, last_sync_at, last_sync_detail FROM cdn_automation ORDER BY id LIMIT 1`).
		Scan(&enabled, &suffix, &tag, &token, &removeOnDelete, &manageRule, &config.LastSyncAt, &detail)
	if err == sql.ErrNoRows {
		// A panel upgraded before the row existed behaves as "not configured"
		// rather than failing every node write.
		return cdnAutomation{RemoveRecordsOnDelete: true}, nil
	}
	if err != nil {
		return cdnAutomation{}, err
	}
	config.Enabled = enabled != 0
	config.RemoveRecordsOnDelete = removeOnDelete != 0
	config.ManageSecurityRule = manageRule != 0
	config.DomainSuffix = suffix.String
	config.InboundTag = tag.String
	config.Token = token.String
	config.LastSyncDetail = detail.String
	return config, nil
}

func (s *Server) saveCDNAutomation(ctx context.Context, config cdnAutomation) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE cdn_automation SET enabled = ?, domain_suffix = ?,
inbound_tag = ?, cloudflare_token = ?, remove_records_on_delete = ?, manage_security_rule = ?`,
			boolToInt(config.Enabled), config.DomainSuffix, config.InboundTag, config.Token,
			boolToInt(config.RemoveRecordsOnDelete), boolToInt(config.ManageSecurityRule))
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected > 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cdn_automation
(enabled, domain_suffix, inbound_tag, cloudflare_token, remove_records_on_delete, manage_security_rule, last_sync_detail)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			boolToInt(config.Enabled), config.DomainSuffix, config.InboundTag, config.Token,
			boolToInt(config.RemoveRecordsOnDelete), boolToInt(config.ManageSecurityRule), "")
		return err
	})
}

func (s *Server) recordCDNSync(ctx context.Context, detail string) {
	if _, err := s.db.ExecContext(ctx,
		`UPDATE cdn_automation SET last_sync_at = ?, last_sync_detail = ?`, time.Now().UTC(), detail); err != nil {
		logging.Warnf(logging.ComponentNode, "cdn automation: could not record the last sync: %v", err)
	}
}

func cdnAutomationView(config cdnAutomation) cdnAutomationResponse {
	view := cdnAutomationResponse{
		Enabled:               config.Enabled,
		DomainSuffix:          config.DomainSuffix,
		InboundTag:            config.inboundTag(),
		RemoveRecordsOnDelete: config.RemoveRecordsOnDelete,
		ManageSecurityRule:    config.ManageSecurityRule,
		TokenSet:              strings.TrimSpace(config.Token) != "",
		Ready:                 config.ready(),
		LastSyncDetail:        config.LastSyncDetail,
	}
	if config.LastSyncAt.Valid {
		view.LastSyncAt = config.LastSyncAt.Time.UTC().Format(time.RFC3339)
	}
	return view
}

func (s *Server) handleCoreCDNAutomation(w http.ResponseWriter, r *http.Request) {
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		config, err := s.loadCDNAutomation(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, cdnAutomationView(config))
	case http.MethodPut, http.MethodPost:
		s.updateCDNAutomation(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) updateCDNAutomation(w http.ResponseWriter, r *http.Request) {
	var payload cdnAutomationUpdate
	if err := decodeOptionalJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	config, err := s.loadCDNAutomation(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if payload.DomainSuffix != nil {
		suffix := normalizeCDNSuffix(*payload.DomainSuffix)
		if strings.TrimSpace(*payload.DomainSuffix) != "" && suffix == "" {
			writeError(w, http.StatusUnprocessableEntity, "that domain does not look like a zone, for example example.com")
			return
		}
		config.DomainSuffix = suffix
	}
	if payload.InboundTag != nil {
		config.InboundTag = strings.TrimSpace(*payload.InboundTag)
	}
	if payload.RemoveRecordsOnDelete != nil {
		config.RemoveRecordsOnDelete = *payload.RemoveRecordsOnDelete
	}
	if payload.ManageSecurityRule != nil {
		config.ManageSecurityRule = *payload.ManageSecurityRule
	}
	if payload.CloudflareToken != nil {
		config.Token = strings.TrimSpace(*payload.CloudflareToken)
	}
	if payload.Enabled != nil {
		config.Enabled = *payload.Enabled
	}
	// Turning this on without the two things it runs on would leave a switch
	// that reads as enabled and quietly does nothing.
	if config.Enabled {
		if normalizeCDNSuffix(config.DomainSuffix) == "" {
			writeError(w, http.StatusUnprocessableEntity, "a domain is required before automatic CDN hosts can be turned on")
			return
		}
		if strings.TrimSpace(config.Token) == "" {
			writeError(w, http.StatusUnprocessableEntity, "a Cloudflare API token is required before automatic CDN hosts can be turned on")
			return
		}
	}
	if err := s.saveCDNAutomation(r.Context(), config); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response := map[string]any{"settings": cdnAutomationView(config)}
	if payload.SyncNow && config.ready() {
		plans, syncErr := s.syncCDNAutomation(r.Context(), config)
		if syncErr != nil {
			response["sync_error"] = syncErr.Error()
		}
		response["hosts"] = plans
		response["created"] = countCDNCreated(plans)
		if refreshed, err := s.loadCDNAutomation(r.Context()); err == nil {
			response["settings"] = cdnAutomationView(refreshed)
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleCoreCDNAutomationSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	config, err := s.loadCDNAutomation(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !config.ready() {
		writeError(w, http.StatusUnprocessableEntity, "automatic CDN hosts are not configured yet")
		return
	}
	plans, syncErr := s.syncCDNAutomation(r.Context(), config)
	response := map[string]any{"hosts": plans, "created": countCDNCreated(plans)}
	if syncErr != nil {
		response["sync_error"] = syncErr.Error()
	}
	if refreshed, err := s.loadCDNAutomation(r.Context()); err == nil {
		response["settings"] = cdnAutomationView(refreshed)
	}
	writeJSON(w, http.StatusOK, response)
}

func countCDNCreated(plans []cdnHostPlan) int {
	created := 0
	for _, plan := range plans {
		if plan.Created {
			created++
		}
	}
	return created
}
