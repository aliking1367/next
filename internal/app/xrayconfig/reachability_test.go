package xrayconfig

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func listenerPort(t *testing.T, listener net.Listener) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestCheckInboundReachabilityReportsOpenPlainPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	inbound := map[string]any{
		"tag":      "auto-shadowsocks",
		"protocol": "shadowsocks",
		"port":     listenerPort(t, listener),
	}
	result := CheckInboundReachability(context.Background(), "127.0.0.1", inbound)
	if result.Status != ReachabilityOK {
		t.Fatalf("expected ok, got %s (%s)", result.Status, result.Detail)
	}
}

func TestCheckInboundReachabilityReportsClosedPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listenerPort(t, listener)
	listener.Close() // nothing is listening on this port any more

	inbound := map[string]any{
		"tag":      "auto-shadowsocks",
		"protocol": "shadowsocks",
		"port":     port,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := CheckInboundReachability(ctx, "127.0.0.1", inbound)
	if result.Status != ReachabilityFailed {
		t.Fatalf("expected failed for a closed port, got %s (%s)", result.Status, result.Detail)
	}
}

func TestCheckInboundReachabilityCompletesTLSHandshake(t *testing.T) {
	certificate := selfSignedTestCertificate(t, "www.example.com")
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				conn.Close()
			}()
		}
	}()

	inbound := map[string]any{
		"tag":      "auto-vless-reality",
		"protocol": "vless",
		"port":     listenerPort(t, listener),
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"serverNames": []any{"www.example.com"},
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := CheckInboundReachability(ctx, "127.0.0.1", inbound)
	if result.Status != ReachabilityOK {
		t.Fatalf("expected ok, got %s (%s)", result.Status, result.Detail)
	}
	if !strings.Contains(result.Detail, "www.example.com") {
		t.Errorf("expected the detail to name the handshake SNI, got %q", result.Detail)
	}
}

func TestCheckInboundReachabilityFailsWhenTLSPortSpeaksPlainText(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("not tls\n"))
			conn.Close()
		}
	}()

	inbound := map[string]any{
		"tag":      "auto-trojan-reality",
		"protocol": "trojan",
		"port":     listenerPort(t, listener),
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"serverNames": []any{"www.example.com"},
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := CheckInboundReachability(ctx, "127.0.0.1", inbound)
	if result.Status != ReachabilityFailed {
		t.Fatalf("expected failed when the port does not speak TLS, got %s (%s)", result.Status, result.Detail)
	}
}

func TestCheckInboundReachabilitySkipsHysteria(t *testing.T) {
	inbound := map[string]any{
		"tag":      "auto-hysteria2",
		"protocol": "hysteria",
		"port":     20000,
		"streamSettings": map[string]any{
			"network":  "hysteria",
			"security": "tls",
		},
	}
	result := CheckInboundReachability(context.Background(), "127.0.0.1", inbound)
	if result.Status != ReachabilitySkipped {
		t.Fatalf("expected hysteria to be skipped, got %s (%s)", result.Status, result.Detail)
	}
}

func TestCheckInboundReachabilityRejectsInboundWithoutPort(t *testing.T) {
	result := CheckInboundReachability(context.Background(), "127.0.0.1", map[string]any{
		"tag":      "auto-vless-reality",
		"protocol": "vless",
	})
	if result.Status != ReachabilityFailed {
		t.Fatalf("expected failed for a portless inbound, got %s", result.Status)
	}
}

func TestCheckInboundReachabilityFailsWithoutNodeAddress(t *testing.T) {
	result := CheckInboundReachability(context.Background(), "  ", map[string]any{
		"tag":      "auto-vless-reality",
		"protocol": "vless",
		"port":     443,
	})
	if result.Status != ReachabilityFailed {
		t.Fatalf("expected failed when the node has no address, got %s (%s)", result.Status, result.Detail)
	}
}

func selfSignedTestCertificate(t *testing.T, commonName string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		DNSNames:              []string{commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
