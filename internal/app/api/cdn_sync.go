package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
)

// syncCDNAutomation brings every node's CDN hostname in line with the nodes as
// they are right now. It is written to be safe to run at any moment and as
// often as wanted: each step checks what is already there and only changes what
// disagrees, so running it twice does nothing the second time.
//
// Order matters. The DNS record is created before the host row, because a host
// row whose hostname resolves nowhere reaches users as a config that silently
// never connects -- worse than no config at all, since they have no way to tell
// the difference.
func (s *Server) syncCDNAutomation(ctx context.Context, config cdnAutomation) ([]cdnHostPlan, error) {
	// Every caller comes through here -- the background pass after a node
	// change, the Sync now button, and the save that syncs -- so one lock here
	// is what keeps two of them from both deciding a host is missing.
	s.cdnSyncRunMu.Lock()
	defer s.cdnSyncRunMu.Unlock()
	return s.syncCDNAutomationLocked(ctx, config)
}

func (s *Server) syncCDNAutomationLocked(ctx context.Context, config cdnAutomation) ([]cdnHostPlan, error) {
	suffix := normalizeCDNSuffix(config.DomainSuffix)
	if suffix == "" {
		return nil, fmt.Errorf("no CDN domain is configured")
	}
	inboundTag := config.inboundTag()

	template, err := s.cdnTemplateHost(ctx, inboundTag)
	if err != nil {
		return nil, err
	}
	nodes, err := s.cdnEligibleNodes(ctx)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no node is serving the shared config yet")
	}
	current, err := s.cdnCurrentHostnames(ctx)
	if err != nil {
		return nil, err
	}

	client := newCloudflareClient(config.Token)
	zoneID, err := client.zoneID(ctx, suffix)
	if err != nil {
		return nil, err
	}

	plans := make([]cdnHostPlan, 0, len(nodes))
	used := map[string]bool{}
	failures := 0
	for _, node := range nodes {
		label := cdnHostLabel(node.Name, node.ID, used)
		plan := cdnHostPlan{
			NodeID:   node.ID,
			NodeName: node.Name,
			NodeIP:   node.Address,
			DNSName:  label,
			Hostname: label + "." + suffix,
			Remark:   cdnHostRemark(node.Name),
		}

		if err := client.upsertProxiedRecord(ctx, zoneID, plan.Hostname, plan.NodeIP); err != nil {
			// One node's record failing must not stop the others: a zone-wide
			// problem will show up on every row, and a single bad address only
			// on its own.
			plan.DNSError = err.Error()
			failures++
			plans = append(plans, plan)
			continue
		}
		plan.DNSCreated = true

		// A rename gives the node a new label, which leaves the previous
		// hostname pointing at it with nothing referring to it. Retire it.
		previous := strings.TrimSpace(current[node.ID])
		if previous != "" && !strings.EqualFold(previous, plan.Hostname) {
			s.retireCDNHostname(ctx, client, zoneID, inboundTag, previous, config.RemoveRecordsOnDelete)
		}

		changed, hostErr := s.upsertCDNHostRow(ctx, inboundTag, template, plan)
		if hostErr != nil {
			plan.HostError = hostErr.Error()
			failures++
			plans = append(plans, plan)
			continue
		}
		plan.Created = changed
		if err := s.setNodeCDNHostname(ctx, node.ID, plan.Hostname); err != nil {
			logging.Warnf(logging.ComponentNode,
				"cdn automation: could not remember hostname for node %d: %v", node.ID, err)
		}
		plans = append(plans, plan)
	}

	detail := fmt.Sprintf("%d node(s) checked, %d changed", len(plans), countCDNCreated(plans))
	if failures > 0 {
		detail = fmt.Sprintf("%s, %d failed", detail, failures)
	}

	// Last, and never fatal. A proxy client cannot answer a challenge, so
	// without this rule Cloudflare refuses these configs with a 403 -- but the
	// records and host rows above are correct either way, and an admin who has
	// the rule in place by hand needs this to stay out of the way.
	if config.ManageSecurityRule {
		covered, ruleErr := s.ensureCDNSecurityRule(ctx, client, zoneID, suffix, plans)
		switch {
		case ruleErr != nil:
			detail = fmt.Sprintf("%s; security rule not written: %s", detail, ruleErr.Error())
			logging.Warnf(logging.ComponentNode, "cdn automation: security rule not written: %v", ruleErr)
		case covered > 0:
			detail = fmt.Sprintf("%s; security rule covers %d hostname(s)", detail, covered)
		}
	}

	s.recordCDNSync(ctx, detail)
	return plans, nil
}

// ensureCDNSecurityRule asks Cloudflare to stop challenging the hostnames this
// sync just confirmed. Only hostnames that resolve to a node are offered, and
// the panel's own hostname is excluded even if a node's label would collide with
// it -- the panel has a dashboard and an API and keeps every protection.
func (s *Server) ensureCDNSecurityRule(ctx context.Context, client cloudflareClient, zoneID, suffix string, plans []cdnHostPlan) (int, error) {
	hostnames := make([]string, 0, len(plans))
	for _, plan := range plans {
		if plan.DNSError != "" || plan.HostError != "" || plan.Hostname == "" {
			continue
		}
		hostnames = append(hostnames, plan.Hostname)
	}
	eligible := cdnSkipRuleHostnames(hostnames, suffix, s.panelOwnHostname(ctx))
	if len(eligible) == 0 {
		return 0, nil
	}
	return client.ensureCDNSkipRule(ctx, zoneID, eligible)
}

// panelOwnHostname reads the hostname the panel is served on, so the security
// rule can refuse to include it. An empty answer only loses this one extra
// guard: the hostnames offered are still limited to the ones the panel built
// for nodes under the managed zone.
func (s *Server) panelOwnHostname(ctx context.Context) string {
	settings, err := s.settingsRepo.SubscriptionSettings(ctx)
	if err != nil {
		return ""
	}
	prefix := strings.TrimSpace(settings.SubscriptionURLPrefix)
	if prefix == "" {
		return ""
	}
	prefix = strings.TrimPrefix(strings.TrimPrefix(prefix, "https://"), "http://")
	if slash := strings.Index(prefix, "/"); slash >= 0 {
		prefix = prefix[:slash]
	}
	if host, _, found := strings.Cut(prefix, ":"); found {
		prefix = host
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(prefix), "."))
}

// upsertCDNHostRow writes the host row for one node and reports whether
// anything actually changed, so a sync that found everything already correct
// does not claim to have built hosts.
func (s *Server) upsertCDNHostRow(ctx context.Context, inboundTag string, template hostPayload, plan cdnHostPlan) (bool, error) {
	payload := template
	payload.Remark = plan.Remark
	payload.Address = plan.Hostname
	hostname := plan.Hostname
	payload.SNI = &hostname
	payload.Host = &hostname

	var existingID int64
	var existingSNI, existingHost, existingRemark sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, sni, host, remark FROM hosts WHERE inbound_tag = ? AND LOWER(COALESCE(address, '')) = ? ORDER BY id LIMIT 1`,
		inboundTag, strings.ToLower(plan.Hostname)).Scan(&existingID, &existingSNI, &existingHost, &existingRemark)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == sql.ErrNoRows {
		insertErr := s.withTx(ctx, func(tx *sql.Tx) error {
			_, insertErr := insertHostTx(ctx, tx, inboundTag, normalizeHostPayload(payload))
			return insertErr
		})
		return insertErr == nil, insertErr
	}

	// The row exists. Only rewrite it when one of the fields this feature owns
	// has drifted, so an admin's own edits to the other fields are left alone.
	if strings.EqualFold(existingSNI.String, plan.Hostname) &&
		strings.EqualFold(existingHost.String, plan.Hostname) &&
		existingRemark.String == plan.Remark {
		return false, nil
	}
	updateErr := s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE hosts SET remark = ?, sni = ?, host = ? WHERE id = ?`,
			plan.Remark, plan.Hostname, plan.Hostname, existingID)
		return err
	})
	return updateErr == nil, updateErr
}

// retireCDNHostname removes a hostname the panel built and no longer uses. The
// host row always goes; the DNS record only when the admin asked for it, since
// a record they later added other uses for is not the panel's to delete.
func (s *Server) retireCDNHostname(ctx context.Context, client cloudflareClient, zoneID, inboundTag, hostname string, removeRecord bool) {
	if strings.TrimSpace(hostname) == "" {
		return
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM hosts WHERE inbound_tag = ? AND LOWER(COALESCE(address, '')) = ?`,
		inboundTag, strings.ToLower(hostname)); err != nil {
		logging.Warnf(logging.ComponentNode, "cdn automation: could not remove host %s: %v", hostname, err)
	}
	if !removeRecord {
		return
	}
	if err := client.deleteRecords(ctx, zoneID, hostname); err != nil {
		logging.Warnf(logging.ComponentNode, "cdn automation: could not remove record %s: %v", hostname, err)
	}
}

func (s *Server) cdnCurrentHostnames(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(cdn_hostname, '') FROM nodes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hostnames := map[int64]string{}
	for rows.Next() {
		var id int64
		var hostname string
		if err := rows.Scan(&id, &hostname); err != nil {
			return nil, err
		}
		hostnames[id] = hostname
	}
	return hostnames, rows.Err()
}

func (s *Server) nodeCDNHostname(ctx context.Context, nodeID int64) string {
	var hostname sql.NullString
	if err := s.db.QueryRowContext(ctx,
		`SELECT cdn_hostname FROM nodes WHERE id = ?`, nodeID).Scan(&hostname); err != nil {
		return ""
	}
	return strings.TrimSpace(hostname.String)
}

func (s *Server) setNodeCDNHostname(ctx context.Context, nodeID int64, hostname string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET cdn_hostname = ? WHERE id = ?`, hostname, nodeID)
	return err
}

// cdnAutomationTimeout bounds the background passes. A node write must not wait
// on Cloudflare, and a Cloudflare call that never answers must not leave a
// goroutine running for the life of the process.
const cdnAutomationTimeout = 90 * time.Second

// kickCDNAutomation runs a sync in the background after a node changed.
//
// It is deliberately detached from the request: the admin's create or edit has
// already succeeded, and making them wait on a third-party API -- or fail
// because of it -- would mean a node they can see in the panel reports as an
// error. Failures are logged and shown on the next sync instead.
func (s *Server) kickCDNAutomation(reason string) {
	s.cdnSyncStateMu.Lock()
	if s.cdnSyncRunning {
		// Someone is already syncing. One more pass afterwards covers this
		// change too, however many arrive while that pass runs -- editing five
		// nodes must not leave five goroutines queued behind one another.
		s.cdnSyncPending = true
		s.cdnSyncStateMu.Unlock()
		return
	}
	s.cdnSyncRunning = true
	s.cdnSyncStateMu.Unlock()
	go s.runCDNAutomation(reason)
}

func (s *Server) runCDNAutomation(reason string) {
	for {
		s.runCDNAutomationOnce(reason)
		s.cdnSyncStateMu.Lock()
		if !s.cdnSyncPending {
			s.cdnSyncRunning = false
			s.cdnSyncStateMu.Unlock()
			return
		}
		s.cdnSyncPending = false
		s.cdnSyncStateMu.Unlock()
		reason = "further node changes"
	}
}

func (s *Server) runCDNAutomationOnce(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), cdnAutomationTimeout)
	defer cancel()
	config, err := s.loadCDNAutomation(ctx)
	if err != nil || !config.ready() {
		return
	}
	if _, err := s.syncCDNAutomation(ctx, config); err != nil {
		logging.Warnf(logging.ComponentNode, "cdn automation (%s) failed: %v", reason, err)
		s.recordCDNSync(ctx, reason+" failed: "+err.Error())
		return
	}
	logging.Infof(logging.ComponentNode, "cdn automation ran after %s", reason)
}

// retireCDNForNode is the delete path. The hostname is read before the node
// goes, because afterwards there is nothing left to look it up from.
func (s *Server) retireCDNForNode(hostname string) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), cdnAutomationTimeout)
		defer cancel()
		config, err := s.loadCDNAutomation(ctx)
		if err != nil || !config.ready() {
			return
		}
		client := newCloudflareClient(config.Token)
		zoneID, err := client.zoneID(ctx, normalizeCDNSuffix(config.DomainSuffix))
		if err != nil {
			logging.Warnf(logging.ComponentNode, "cdn automation: could not reach the zone to retire %s: %v", hostname, err)
			return
		}
		// Taking the same lock as a sync: removing a host row while a sync is
		// deciding which rows are missing would have it recreate what this is
		// removing.
		s.cdnSyncRunMu.Lock()
		defer s.cdnSyncRunMu.Unlock()
		s.retireCDNHostname(ctx, client, zoneID, config.inboundTag(), hostname, config.RemoveRecordsOnDelete)
		logging.Infof(logging.ComponentNode, "cdn automation retired %s", hostname)
	}()
}
