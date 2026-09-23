package api

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// autoProvisionServiceName is the fixed name of the service that bundles the
// hosts created by the auto-provision button, so re-running it finds and
// reuses the same service instead of creating duplicates.
const autoProvisionServiceName = "Best Protocols (Auto)"

// autoProvisionServiceFlow is applied to the bundle service so VLESS+REALITY
// users get Vision, the current standard. The panel drops the flow
// automatically on inbounds that cannot use it (XHTTP, CDN, Hysteria2).
const autoProvisionServiceFlow = "xtls-rprx-vision"

const (
	verifyProtocolsTimeout     = 20 * time.Second
	verifyProtocolProbeTimeout = 4 * time.Second
)

// Warning codes returned to the dashboard. Xray only runs on nodes, never on
// the panel server itself, so without a connected node that uses the panel's
// default config nothing is served and users' {SERVER_IP} has no address.
const (
	provisionWarningNoNodes              = "no_nodes"
	provisionWarningNoConnectedNodes     = "no_connected_nodes"
	provisionWarningNoDefaultConfigNodes = "no_default_config_nodes"
)

type provisionNode struct {
	ID            int64
	Name          string
	Address       string
	Connected     bool
	DefaultConfig bool
}

type provisionNodeSummary struct {
	Total     int `json:"total"`
	Connected int `json:"connected"`
	Serving   int `json:"serving"`
}

type autoProvisionRequest struct {
	// CDNDomain optionally adds a Cloudflare-fronted inbound so users can
	// still connect when the server's own IP is filtered.
	CDNDomain string `json:"cdn_domain"`
	// Gaming adds the latency-oriented UDP inbounds (mKCP and a second
	// Hysteria2) alongside the default set.
	Gaming bool `json:"gaming"`
}

type autoProvisionResponse struct {
	ServiceID    int64                `json:"service_id"`
	ServiceName  string               `json:"service_name"`
	Flow         string               `json:"flow,omitempty"`
	Protocols    []any                `json:"protocols"`
	Retired      []string             `json:"retired,omitempty"`
	CDNRequested bool                 `json:"cdn_requested"`
	Nodes        provisionNodeSummary `json:"nodes"`
	Warning      string               `json:"warning,omitempty"`
	Detail       string               `json:"detail"`
}

// localPortBusy reports whether something on this machine already listens on
// the TCP port (typically the panel's own HTTPS). It only sees this machine;
// a node on another server can still have its own conflicts.
func localPortBusy(port int) bool {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err == nil {
		_ = listener.Close()
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}

// provisionNodes lists the nodes that could run the auto-configured
// inbounds. Nodes on a custom Xray config don't use the panel's shared
// config, so they never serve these inbounds.
func (s *Server) provisionNodes(ctx context.Context) ([]provisionNode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(name, ''), COALESCE(address, ''),
		       LOWER(COALESCE(status, '')), LOWER(COALESCE(xray_config_mode, 'default'))
		  FROM nodes
		 WHERE LOWER(COALESCE(status, '')) <> 'deleted'
		 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []provisionNode{}
	for rows.Next() {
		var node provisionNode
		var status, mode string
		if err := rows.Scan(&node.ID, &node.Name, &node.Address, &status, &mode); err != nil {
			return nil, err
		}
		node.Address = strings.TrimSpace(node.Address)
		node.Connected = status == "connected"
		node.DefaultConfig = mode == "" || mode == "default"
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func summarizeProvisionNodes(nodes []provisionNode) (provisionNodeSummary, string) {
	summary := provisionNodeSummary{Total: len(nodes)}
	for _, node := range nodes {
		if node.Connected {
			summary.Connected++
			if node.DefaultConfig {
				summary.Serving++
			}
		}
	}
	switch {
	case summary.Total == 0:
		return summary, provisionWarningNoNodes
	case summary.Connected == 0:
		return summary, provisionWarningNoConnectedNodes
	case summary.Serving == 0:
		return summary, provisionWarningNoDefaultConfigNodes
	}
	return summary, ""
}

// handleCoreAutoConfigure is the one-click "auto-configure best protocols"
// action: it creates a curated set of modern Xray inbounds (VLESS+REALITY,
// Trojan+REALITY, Hysteria2, Shadowsocks) on the master node and bundles
// their hosts into a single service, so a sudo admin only has to create a
// user afterward to hand out working access to all of them at once.
func (s *Server) handleCoreAutoConfigure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}

	var request autoProvisionRequest
	if err := decodeOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.configRepo.AutoProvisionBestProtocols(r.Context(), xrayconfig.AutoProvisionOptions{
		CDNDomain: request.CDNDomain,
		Gaming:    request.Gaming,
		PortBusy:  localPortBusy,
	})
	if err != nil {
		if errors.Is(err, xrayconfig.ErrInvalidInbound) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to auto-configure protocols: "+err.Error())
		return
	}

	tags := make([]string, 0, len(result.Protocols))
	protocols := make([]any, 0, len(result.Protocols))
	for _, protocol := range result.Protocols {
		tags = append(tags, protocol.Tag)
		protocols = append(protocols, protocol)
	}

	var serviceID int64
	var flow string
	err = s.withTx(r.Context(), func(tx *sql.Tx) error {
		var findErr error
		serviceID, findErr = findOrCreateAutoProvisionServiceTx(r.Context(), tx)
		if findErr != nil {
			return findErr
		}
		flowChanged, currentFlow, findErr := ensureAutoProvisionServiceFlowTx(r.Context(), tx, serviceID)
		if findErr != nil {
			return findErr
		}
		flow = currentFlow
		beforeTags, findErr := serviceRuntimeInboundTagsTx(r.Context(), tx, serviceID)
		if findErr != nil {
			return findErr
		}
		hostIDs, findErr := hostIDsForInboundTagsTx(r.Context(), tx, tags)
		if findErr != nil {
			return findErr
		}
		assignments, findErr := mergedServiceHostAssignmentsTx(r.Context(), tx, serviceID, hostIDs)
		if findErr != nil {
			return findErr
		}
		if findErr := syncServiceHostsTx(r.Context(), tx, serviceID, assignments); findErr != nil {
			return findErr
		}
		afterTags, findErr := serviceRuntimeInboundTagsTx(r.Context(), tx, serviceID)
		if findErr != nil {
			return findErr
		}
		// Existing users must pick up new inbounds / the flow change: nodes build
		// each user's clients from the service's hosts at sync time.
		if flowChanged || !stringBoolMapsEqual(beforeTags, afterTags) {
			return enqueueAffectedServicesUsersTx(r.Context(), tx, map[int64]bool{serviceID: true})
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to attach auto-configured hosts to a service: "+err.Error())
		return
	}

	nodes, err := s.provisionNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read nodes: "+err.Error())
		return
	}
	summary, warning := summarizeProvisionNodes(nodes)

	writeJSON(w, http.StatusOK, autoProvisionResponse{
		ServiceID:    serviceID,
		ServiceName:  autoProvisionServiceName,
		Flow:         flow,
		Protocols:    protocols,
		Retired:      result.Retired,
		CDNRequested: result.CDNRequested,
		Nodes:        summary,
		Warning:      warning,
		Detail:       "Best protocols configured",
	})
}

// handleCoreVerifyProtocols re-tests the auto-configured inbounds. Xray runs
// on nodes, not on the panel server, so each inbound is probed at the address
// of every connected node that uses the panel's default config: a TCP dial
// and, for TLS/REALITY, a real handshake. It is a separate action from
// auto-configure because a node applies a new config asynchronously (the
// node operation queue), so a check run in the same request would race the
// reload.
func (s *Server) handleCoreVerifyProtocols(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), verifyProtocolsTimeout)
	defer cancel()

	nodes, err := s.provisionNodes(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to read nodes: "+err.Error())
		return
	}
	summary, warning := summarizeProvisionNodes(nodes)

	inbounds := make([]map[string]any, 0, len(xrayconfig.AutoProvisionTags()))
	for _, tag := range xrayconfig.AutoProvisionTags() {
		inbound, err := s.configRepo.GetInbound(ctx, tag)
		if err != nil {
			if errors.Is(err, xrayconfig.ErrInboundNotFound) {
				continue
			}
			writeError(w, http.StatusInternalServerError, "Failed to read inbound "+tag+": "+err.Error())
			return
		}
		inbounds = append(inbounds, inbound)
	}

	type probe struct {
		node    provisionNode
		inbound map[string]any
	}
	probes := []probe{}
	for _, node := range nodes {
		if !node.Connected || !node.DefaultConfig {
			continue
		}
		for _, inbound := range inbounds {
			probes = append(probes, probe{node: node, inbound: inbound})
		}
	}

	results := make([]xrayconfig.ReachabilityResult, len(probes))
	var wg sync.WaitGroup
	for i, item := range probes {
		wg.Add(1)
		go func(i int, item probe) {
			defer wg.Done()
			probeCtx, probeCancel := context.WithTimeout(ctx, verifyProtocolProbeTimeout)
			defer probeCancel()
			result := xrayconfig.CheckInboundReachability(probeCtx, item.node.Address, item.inbound)
			result.NodeID = item.node.ID
			result.NodeName = item.node.Name
			results[i] = result
		}(i, item)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes":   summary,
		"warning": warning,
		"results": results,
		"detail":  "Probed from the panel server at each node's address; reachability from a client network still needs a real client test.",
	})
}

func findOrCreateAutoProvisionServiceTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM services WHERE name = ?`, autoProvisionServiceName).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	now := dbTimestamp(time.Now().UTC())
	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO services (name, description, used_traffic, lifetime_used_traffic, users_usage, created_at, updated_at) VALUES (?, ?, 0, 0, 0, ?, ?)`,
		autoProvisionServiceName,
		"Automatically created by the auto-configure best protocols action.",
		now,
		now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ensureAutoProvisionServiceFlowTx sets Vision on the bundle service only when
// no flow is chosen yet, so an admin's own choice is never overwritten. It
// returns whether it changed the service and the flow now in effect. Panels
// without the flow column simply skip it.
func ensureAutoProvisionServiceFlowTx(ctx context.Context, tx *sql.Tx, serviceID int64) (bool, string, error) {
	current, supported, err := serviceFlowTx(ctx, tx, serviceID)
	if err != nil {
		return false, "", err
	}
	if !supported {
		return false, "", nil
	}
	if current != "" {
		return false, current, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE services SET flow = ? WHERE id = ?`, autoProvisionServiceFlow, serviceID); err != nil {
		if serviceFlowColumnMissing(err) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, autoProvisionServiceFlow, nil
}

func hostIDsForInboundTagsTx(ctx context.Context, tx *sql.Tx, tags []string) ([]int64, error) {
	ids := make([]int64, 0, len(tags))
	if len(tags) == 0 {
		return ids, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(tags)), ",")
	args := make([]any, len(tags))
	for i, tag := range tags {
		args[i] = tag
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM hosts WHERE inbound_tag IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// mergedServiceHostAssignmentsTx keeps any hosts already attached to the
// service (an admin may have added their own) and adds the newly
// auto-configured ones, so re-running the action never drops manual changes.
func mergedServiceHostAssignmentsTx(ctx context.Context, tx *sql.Tx, serviceID int64, newHostIDs []int64) ([]serviceHostAssignment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT host_id FROM service_hosts WHERE service_id = ? ORDER BY sort`, serviceID)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	assignments := make([]serviceHostAssignment, 0, len(newHostIDs))
	for rows.Next() {
		var hostID int64
		if err := rows.Scan(&hostID); err != nil {
			rows.Close()
			return nil, err
		}
		if !seen[hostID] {
			seen[hostID] = true
			assignments = append(assignments, serviceHostAssignment{HostID: hostID})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, hostID := range newHostIDs {
		if !seen[hostID] {
			seen[hostID] = true
			assignments = append(assignments, serviceHostAssignment{HostID: hostID})
		}
	}
	return assignments, nil
}
