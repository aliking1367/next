package xrayconfig

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Reachability status values.
const (
	ReachabilityOK      = "ok"
	ReachabilityFailed  = "failed"
	ReachabilitySkipped = "skipped"
)

// ReachabilityResult reports whether one inbound is actually serving traffic
// on the node that runs it.
//
// This answers "did the node's Xray pick up this config and open the port",
// as seen from the panel server. It deliberately does not claim anything
// about reachability from a particular client network — that depends on
// upstream filtering and can only be confirmed by a real client there.
type ReachabilityResult struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	NodeID   int64  `json:"node_id,omitempty"`
	NodeName string `json:"node_name,omitempty"`
	Address  string `json:"address,omitempty"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
}

// CheckInboundReachability dials an inbound at host (the address of the node
// that runs Xray — the panel itself never does) and, for TLS/REALITY
// inbounds, completes a TLS handshake against it. A REALITY inbound answers
// an unauthenticated handshake by proxying its masquerade target, so a
// successful handshake also proves the REALITY fallback works.
func CheckInboundReachability(ctx context.Context, host string, inbound map[string]any) ReachabilityResult {
	tag := stringValue(inbound["tag"])
	protocol := normalizeProxyProtocol(stringValue(inbound["protocol"]))
	result := ReachabilityResult{Tag: tag, Protocol: protocol}

	port, err := parseConfigPort(inbound["port"])
	if err != nil || port < 1 || port > 65535 {
		result.Status = ReachabilityFailed
		result.Detail = "inbound has no usable port"
		return result
	}
	result.Port = port
	host = strings.TrimSpace(host)
	result.Address = host

	stream := mapValue(inbound["streamSettings"])
	network := streamNetwork(stream)
	if protocol == "hysteria" || network == "hysteria" || network == "quic" {
		result.Status = ReachabilitySkipped
		result.Detail = "UDP/QUIC inbound: a listening socket cannot be probed remotely, test it from a client"
		return result
	}
	if host == "" {
		result.Status = ReachabilityFailed
		result.Detail = "the node has no address to probe"
		return result
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		result.Status = ReachabilityFailed
		result.Detail = tcpFailureDetail(err)
		return result
	}
	defer conn.Close()

	security := strings.ToLower(strings.TrimSpace(stringValue(stream["security"])))
	if security != "tls" && security != "reality" {
		result.Status = ReachabilityOK
		result.Detail = "port is accepting connections"
		return result
	}

	serverName := inboundHandshakeServerName(stream, security)
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	// InsecureSkipVerify: this is a liveness probe against our own port, not a
	// trust decision — REALITY intentionally serves another site's certificate.
	tlsConn := tls.Client(conn, &tls.Config{ServerName: serverName, InsecureSkipVerify: true})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		result.Status = ReachabilityFailed
		result.Detail = fmt.Sprintf("port is open but the TLS handshake failed: %v", err)
		return result
	}
	result.Status = ReachabilityOK
	if security == "reality" {
		result.Detail = fmt.Sprintf("REALITY handshake succeeded as %q", serverName)
		return result
	}
	result.Detail = "TLS handshake succeeded"
	return result
}

func inboundHandshakeServerName(stream map[string]any, security string) string {
	if security == "reality" {
		reality := mapValue(stream["realitySettings"])
		if names := stringList(reality["serverNames"]); len(names) > 0 {
			return names[0]
		}
		if name := firstNonEmptyString(reality["serverName"]); name != "" {
			return name
		}
	}
	tlsSettings := mapValue(stream["tlsSettings"])
	if name := firstNonEmptyString(tlsSettings["serverName"]); name != "" {
		return name
	}
	return "localhost"
}

func tcpFailureDetail(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "connection timed out; the node's Xray may still be reloading, or a firewall drops it"
	}
	return "port is not accepting connections; the node's Xray may not have applied the config yet, or a firewall blocks it"
}
