package node

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// A node install token lets a node server fetch its own certificate bundle
// over HTTPS instead of the admin pasting a PEM block into a terminal, which
// is where node installs most often go wrong.
//
// The token is the whole credential for one node's private key, so it is
// single-use, short-lived, and only ever stored as a hash.

const (
	// DefaultInstallTokenTTL is long enough to create a server and boot it,
	// short enough that a token left in a chat log or a provider's startup
	// script field stops working the same day.
	DefaultInstallTokenTTL = 2 * time.Hour
	installTokenBytes      = 32
)

var (
	// ErrInstallTokenInvalid covers every reason a token cannot be redeemed:
	// unknown, already used, or expired. They are not told apart on purpose,
	// so a caller probing tokens learns nothing from the difference.
	ErrInstallTokenInvalid = errors.New("install token is not valid")
)

type InstallToken struct {
	Token     string    `json:"token"`
	NodeID    int64     `json:"node_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type InstallBundle struct {
	NodeID         int64
	NodeName       string
	Certificate    string
	CertificateKey string
}

// installTokenTime reads a stored timestamp back whichever way the driver
// hands it over: MySQL returns a time.Time, SQLite a string.
func installTokenTime(value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		return typed.UTC(), nil
	case []byte:
		return parseDBTime(string(typed))
	case string:
		return parseDBTime(typed)
	default:
		return time.Time{}, errors.New("unsupported timestamp")
	}
}

func installTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func newInstallToken() (string, error) {
	buffer := make([]byte, installTokenBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// CreateInstallToken issues a token for an existing node. Any token that node
// still has outstanding is dropped first: two live tokens for one node would
// mean a link the admin thought they had replaced still works.
func (r Repository) CreateInstallToken(ctx context.Context, nodeID int64, ttl time.Duration) (InstallToken, error) {
	if ttl <= 0 {
		ttl = DefaultInstallTokenTTL
	}
	// Only existence matters here, so this does not load the whole node: a
	// token is issued against an id, not against a node's runtime state.
	var status string
	switch err := r.db.QueryRowContext(ctx, `SELECT COALESCE(status, '') FROM nodes WHERE id = ?`, nodeID).Scan(&status); {
	case err == sql.ErrNoRows:
		return InstallToken{}, typedError(ErrorNotFound, "Node not found")
	case err != nil:
		return InstallToken{}, err
	}
	if strings.EqualFold(strings.TrimSpace(status), StatusDeleted) {
		return InstallToken{}, typedError(ErrorNotFound, "Node not found")
	}
	token, err := newInstallToken()
	if err != nil {
		return InstallToken{}, err
	}
	now := r.now().UTC()
	expiresAt := now.Add(ttl)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return InstallToken{}, err
	}
	defer rollbackQuiet(tx)
	if _, err := tx.ExecContext(ctx, `DELETE FROM node_install_tokens WHERE node_id = ?`, nodeID); err != nil {
		return InstallToken{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO node_install_tokens (node_id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		nodeID, installTokenHash(token), dbTimestamp(now), dbTimestamp(expiresAt)); err != nil {
		return InstallToken{}, err
	}
	if err := tx.Commit(); err != nil {
		return InstallToken{}, err
	}
	return InstallToken{Token: token, NodeID: nodeID, ExpiresAt: expiresAt}, nil
}

// RedeemInstallToken hands back the node's bundle exactly once. The token is
// marked used inside the same transaction that reads it, so two servers
// racing on the same token cannot both be installed.
func (r Repository) RedeemInstallToken(ctx context.Context, token string) (InstallBundle, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return InstallBundle{}, ErrInstallTokenInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return InstallBundle{}, err
	}
	defer rollbackQuiet(tx)

	var id, nodeID int64
	var expiresRaw any
	var usedRaw any
	err = tx.QueryRowContext(ctx,
		`SELECT id, node_id, expires_at, used_at FROM node_install_tokens WHERE token_hash = ? LIMIT 1`,
		installTokenHash(token)).Scan(&id, &nodeID, &expiresRaw, &usedRaw)
	if err == sql.ErrNoRows {
		return InstallBundle{}, ErrInstallTokenInvalid
	}
	if err != nil {
		return InstallBundle{}, err
	}
	if usedRaw != nil {
		return InstallBundle{}, ErrInstallTokenInvalid
	}
	expiresAt, err := installTokenTime(expiresRaw)
	if err != nil || !r.now().UTC().Before(expiresAt) {
		return InstallBundle{}, ErrInstallTokenInvalid
	}

	var name string
	var certificate, certificateKey sql.NullString
	var status sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(name, ''), certificate, certificate_key, COALESCE(status, '') FROM nodes WHERE id = ?`,
		nodeID).Scan(&name, &certificate, &certificateKey, &status)
	if err == sql.ErrNoRows {
		return InstallBundle{}, ErrInstallTokenInvalid
	}
	if err != nil {
		return InstallBundle{}, err
	}
	if strings.EqualFold(strings.TrimSpace(status.String), StatusDeleted) {
		return InstallBundle{}, ErrInstallTokenInvalid
	}
	if strings.TrimSpace(certificate.String) == "" || strings.TrimSpace(certificateKey.String) == "" {
		return InstallBundle{}, ErrInstallTokenInvalid
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE node_install_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL`,
		dbTimestamp(r.now().UTC()), id); err != nil {
		return InstallBundle{}, err
	}
	if err := tx.Commit(); err != nil {
		return InstallBundle{}, err
	}
	return InstallBundle{
		NodeID:         nodeID,
		NodeName:       name,
		Certificate:    strings.TrimSpace(certificate.String),
		CertificateKey: strings.TrimSpace(certificateKey.String),
	}, nil
}

// InstallBundlePEM is what the installer writes to disk: the certificate and
// its key in one file, the same shape the panel shows for a manual paste.
func (b InstallBundle) InstallBundlePEM() string {
	return strings.TrimSpace(b.Certificate) + "\n" + strings.TrimSpace(b.CertificateKey) + "\n"
}
