package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

// A CDN-fronted inbound only ever reaches the one node its hostname resolves
// to, so covering every node means one hostname and one host row each. Doing
// that by hand is where it goes wrong: a path copied with a character
// missing, an SNI left pointing at the previous node, a DNS record created
// grey instead of orange. This builds the host rows from the one that already
// works and hands back exactly the DNS records still to create, because the
// panel has no credentials for the DNS provider and cannot make them itself.

type cdnHostsRequest struct {
	// InboundTag is the CDN inbound to extend. Empty means the one
	// auto-configure creates.
	InboundTag string `json:"inbound_tag"`
	// DomainSuffix is the zone the hostnames are built under, e.g.
	// "example.com" gives "<node>.example.com".
	DomainSuffix string `json:"domain_suffix"`
	// CloudflareToken, when given, lets the panel create the DNS records
	// itself. It is used for this one request and never stored.
	CloudflareToken string `json:"cloudflare_token"`
}

type cdnHostPlan struct {
	NodeID     int64  `json:"node_id"`
	NodeName   string `json:"node_name"`
	NodeIP     string `json:"node_ip"`
	Hostname   string `json:"hostname"`
	DNSName    string `json:"dns_name"`
	Remark     string `json:"remark"`
	Created    bool   `json:"created"`
	SkipReason string `json:"skip_reason,omitempty"`
	// DNSCreated says the panel made the record; DNSError says why it could
	// not, so one failing record does not hide the rest.
	DNSCreated bool   `json:"dns_created"`
	DNSError   string `json:"dns_error,omitempty"`
	// HostError is kept apart from DNSError on purpose. Reporting a failed
	// host write as a DNS error sends an admin to Cloudflare to look for a
	// problem that is in the panel.
	HostError string `json:"host_error,omitempty"`
}

type cdnHostsResponse struct {
	InboundTag string        `json:"inbound_tag"`
	Hosts      []cdnHostPlan `json:"hosts"`
	Created    int           `json:"created"`
	DNSCreated int           `json:"dns_created"`
	// DNSManaged is false when no token was given, which is what tells the
	// dashboard to show the records the admin still has to add by hand.
	DNSManaged bool   `json:"dns_managed"`
	Detail     string `json:"detail"`
}

func (s *Server) handleCoreCDNHosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	var request cdnHostsRequest
	if err := decodeOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	suffix := normalizeCDNSuffix(request.DomainSuffix)
	if suffix == "" {
		writeError(w, http.StatusUnprocessableEntity, "a domain is required, for example example.com")
		return
	}
	inboundTag := strings.TrimSpace(request.InboundTag)
	if inboundTag == "" {
		inboundTag = "auto-cdn-xhttp"
	}

	ctx := r.Context()
	template, err := s.cdnTemplateHost(ctx, inboundTag)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	nodes, err := s.cdnEligibleNodes(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(nodes) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no node is serving the shared config yet")
		return
	}
	existing, err := s.cdnExistingAddresses(ctx, inboundTag)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	plans := make([]cdnHostPlan, 0, len(nodes))
	used := map[string]bool{}
	for _, node := range nodes {
		label := cdnHostLabel(node.Name, node.ID, used)
		plan := cdnHostPlan{
			NodeID:   node.ID,
			NodeName: node.Name,
			NodeIP:   node.Address,
			DNSName:  label,
			Hostname: label + "." + suffix,
			Remark:   strings.TrimSpace(node.Name) + " · CDN",
		}
		if existing[strings.ToLower(plan.Hostname)] {
			plan.SkipReason = "a host with this address already exists"
			plans = append(plans, plan)
			continue
		}
		plans = append(plans, plan)
	}

	// DNS first: a host row whose name resolves nowhere is worse than no host
	// at all, because it reaches users as a config that silently never
	// connects.
	dnsManaged := strings.TrimSpace(request.CloudflareToken) != ""
	dnsCreated := 0
	if dnsManaged {
		client := newCloudflareClient(request.CloudflareToken)
		zoneID, zoneErr := client.zoneID(ctx, suffix)
		if zoneErr != nil {
			writeError(w, http.StatusBadRequest, zoneErr.Error())
			return
		}
		for i := range plans {
			if plans[i].SkipReason != "" {
				continue
			}
			if err := client.upsertProxiedRecord(ctx, zoneID, plans[i].Hostname, plans[i].NodeIP); err != nil {
				plans[i].DNSError = err.Error()
				continue
			}
			plans[i].DNSCreated = true
			dnsCreated++
		}
	}

	created := 0
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		for i := range plans {
			if plans[i].SkipReason != "" {
				continue
			}
			// A record the panel tried and failed to create means this host
			// would not resolve, so it is not written.
			if dnsManaged && !plans[i].DNSCreated {
				continue
			}
			payload := template
			payload.Remark = plans[i].Remark
			payload.Address = plans[i].Hostname
			hostname := plans[i].Hostname
			payload.SNI = &hostname
			payload.Host = &hostname
			if _, insertErr := insertHostTx(ctx, tx, inboundTag, payload); insertErr != nil {
				return insertErr
			}
			plans[i].Created = true
			created++
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the hosts: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, cdnHostsResponse{
		InboundTag: inboundTag,
		Hosts:      plans,
		Created:    created,
		DNSCreated: dnsCreated,
		DNSManaged: dnsManaged,
		Detail:     "CDN hosts built",
	})
}

// cdnTemplateHost copies the host that already works on this inbound. The
// path, port and TLS settings have to match the inbound exactly, and reading
// them back is safer than rebuilding them from assumptions.
func (s *Server) cdnTemplateHost(ctx context.Context, inboundTag string) (hostPayload, error) {
	var payload hostPayload
	var port sql.NullInt64
	var path, sni, host, security, alpn, fingerprint sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT port, path, sni, host, security, alpn, fingerprint FROM hosts
WHERE inbound_tag = ? ORDER BY id LIMIT 1`, inboundTag).Scan(&port, &path, &sni, &host, &security, &alpn, &fingerprint)
	if err == sql.ErrNoRows {
		return hostPayload{}, statusError{
			status: http.StatusUnprocessableEntity,
			detail: "inbound " + inboundTag + " has no host to copy yet; run auto-configure with a CDN domain first",
		}
	}
	if err != nil {
		return hostPayload{}, err
	}
	if port.Valid {
		value := port.Int64
		payload.Port = &value
	}
	if path.Valid && strings.TrimSpace(path.String) != "" {
		value := path.String
		payload.Path = &value
	}
	payload.Security = security.String
	payload.ALPN = alpn.String
	payload.Fingerprint = fingerprint.String
	return payload, nil
}

type cdnNode struct {
	ID      int64
	Name    string
	Address string
}

// cdnEligibleNodes lists the nodes that run the shared config, which are the
// only ones a shared CDN inbound can reach.
func (s *Server) cdnEligibleNodes(ctx context.Context) ([]cdnNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(name, ''), COALESCE(address, '') FROM nodes
WHERE TRIM(COALESCE(address, '')) != ''
  AND LOWER(COALESCE(status, '')) NOT IN ('deleted', 'disabled', 'limited')
  AND LOWER(COALESCE(xray_config_mode, 'default')) <> 'custom'
ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	nodes := []cdnNode{}
	for rows.Next() {
		var node cdnNode
		if err := rows.Scan(&node.ID, &node.Name, &node.Address); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func (s *Server) cdnExistingAddresses(ctx context.Context, inboundTag string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(address, '') FROM hosts WHERE inbound_tag = ?`, inboundTag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	addresses := map[string]bool{}
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, err
		}
		addresses[strings.ToLower(strings.TrimSpace(address))] = true
	}
	return addresses, rows.Err()
}

// cdnHostLabel turns a node's name into a DNS label. Node names carry flags,
// punctuation and spaces that no DNS name may hold, and two nodes can share a
// name once stripped, so a collision falls back to the node id rather than
// quietly pointing two nodes at one hostname.
func cdnHostLabel(name string, nodeID int64, used map[string]bool) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	label := strings.Trim(b.String(), "-")
	if len(label) > 40 {
		label = strings.Trim(label[:40], "-")
	}
	if label == "" {
		label = "node"
	}
	if used[label] {
		label = label + "-" + strconv.FormatInt(nodeID, 10)
	}
	used[label] = true
	return label
}

// normalizeCDNSuffix accepts what an admin is likely to paste -- a bare zone,
// a full URL, a leading dot -- and returns the zone the hostnames hang off.
func normalizeCDNSuffix(value string) string {
	suffix := strings.ToLower(strings.TrimSpace(value))
	suffix = strings.TrimPrefix(suffix, "https://")
	suffix = strings.TrimPrefix(suffix, "http://")
	if slash := strings.Index(suffix, "/"); slash >= 0 {
		suffix = suffix[:slash]
	}
	suffix = strings.Trim(suffix, ".")
	if suffix == "" || !isValidPublicHostnameLocal(suffix) {
		return ""
	}
	return suffix
}

func isValidPublicHostnameLocal(value string) bool {
	if len(value) > 253 || !strings.Contains(value, ".") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-') {
				return false
			}
		}
	}
	return true
}
