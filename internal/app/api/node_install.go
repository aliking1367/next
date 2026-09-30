package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	nodeapp "github.com/aliking1367/next/internal/app/node"
)

// Installing a node used to mean pasting a multi-line PEM bundle into a
// terminal, which is where it most often went wrong: a provider's web console
// with the wrong keyboard layout silently corrupts the key, the node starts,
// and the panel then reports a dial timeout that points nowhere near the
// cause.
//
// The panel now hands out a single-use token instead. The node server fetches
// its own bundle over HTTPS, so the admin runs one line, or pastes a startup
// script when creating the server and never opens a terminal at all.

const nodeInstallScriptPath = "/node-install.sh"

type nodeInstallTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
	// Command is what an admin runs on a server that already exists.
	Command string `json:"command"`
	// StartupScript goes in the provider's "startup script" or "user data"
	// field when creating a server, so the node installs itself on first boot.
	StartupScript string `json:"startup_script"`
}

func (s *Server) handleNodeInstallToken(w http.ResponseWriter, r *http.Request, nodeID int64) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	token, err := s.nodeMutations.CreateInstallToken(ctx, nodeID, nodeapp.DefaultInstallTokenTTL)
	if err != nil {
		writeNodeMutationError(w, err)
		return
	}
	base := panelBaseURL(r)
	writeJSON(w, http.StatusOK, nodeInstallTokenResponse{
		Token:         token.Token,
		ExpiresAt:     token.ExpiresAt.UTC().Format(time.RFC3339),
		Command:       nodeInstallCommand(base, token.Token),
		StartupScript: nodeStartupScript(base, token.Token),
	})
}

// handleNodeInstallBundle is reached by the node server itself, with the
// token as its only credential. It is deliberately terse: every failure
// answers the same way, so a caller trying tokens learns nothing from the
// difference between unknown, used and expired.
func (s *Server) handleNodeInstallBundle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	bundle, err := s.nodeMutations.RedeemInstallToken(ctx, token)
	if err != nil {
		if errors.Is(err, nodeapp.ErrInstallTokenInvalid) {
			writeError(w, http.StatusNotFound, "install token is not valid")
			return
		}
		writeError(w, http.StatusInternalServerError, "could not read the install bundle")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(bundle.InstallBundlePEM()))
}

// handleNodeInstallScript serves the installer the one-line command runs. It
// holds no secret of its own: the token travels in the caller's argument.
func (s *Server) handleNodeInstallScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(nodeInstallScript(panelBaseURL(r))))
}

// panelBaseURL is the address the node server must call back on. It is taken
// from the request, because that is the address the admin actually reaches
// the panel at -- behind a CDN or a port rewrite, no stored setting would be
// right.
func panelBaseURL(r *http.Request) string {
	scheme := "https"
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = strings.ToLower(strings.Split(forwarded, ",")[0])
	} else if r.TLS == nil {
		scheme = "http"
	}
	host := strings.TrimSpace(r.Host)
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = strings.Split(forwarded, ",")[0]
	}
	return strings.TrimRight(scheme+"://"+strings.TrimSpace(host), "/")
}

func nodeInstallCommand(baseURL string, token string) string {
	return fmt.Sprintf("bash <(curl -fsSL %s%s) --token %s", baseURL, nodeInstallScriptPath, token)
}

// nodeStartupScript is the same installer with the token already in it, ready
// to paste into a provider's startup-script field. It runs as root on first
// boot, so the admin never opens a terminal on the node at all.
func nodeStartupScript(baseURL string, token string) string {
	return "#!/usr/bin/env bash\n" +
		"# Next node install. Runs once on first boot.\n" +
		fmt.Sprintf("curl -fsSL %s%s -o /tmp/next-node-install.sh\n", baseURL, nodeInstallScriptPath) +
		fmt.Sprintf("bash /tmp/next-node-install.sh --token %s\n", token)
}

// nodeInstallScript is written to be safe to run unattended: it fails loudly
// rather than half-installing, keeps the bundle out of the process list, and
// deletes it as soon as the installer has read it.
func nodeInstallScript(baseURL string) string {
	escaped := strings.ReplaceAll(baseURL, "'", "")
	installerURL := "https://raw.githubusercontent.com/" + nodeInstallerRepo + "/master/scripts/next/next-node-binary.sh"
	return `#!/usr/bin/env bash
set -euo pipefail

PANEL_URL='` + escaped + `'
TOKEN=""

while [ $# -gt 0 ]; do
    case "$1" in
        --token)
            TOKEN="${2:-}"
            shift 2
            ;;
        --panel-url)
            PANEL_URL="${2:-}"
            shift 2
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 1
            ;;
    esac
done

if [ -z "$TOKEN" ]; then
    echo "A --token is required. Copy the install command from the panel's Nodes page." >&2
    exit 1
fi

if [ "$(id -u)" != "0" ]; then
    echo "Run this as root." >&2
    exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
    apt-get update -y >/dev/null 2>&1 || true
    apt-get install -y curl >/dev/null 2>&1 || true
fi

WORK=$(mktemp -d)
# The bundle holds this node's private key: keep it out of shared /tmp and
# remove it as soon as the installer has read it, whatever happens next.
chmod 700 "$WORK"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

echo "Fetching this node's install bundle from the panel"
if ! curl -fsSL "$PANEL_URL/api/node/install-bundle?token=$TOKEN" -o "$WORK/bundle.pem"; then
    echo "The panel refused the token. It is single-use and expires; generate a new one in the panel." >&2
    exit 1
fi
if ! grep -q -- "-----END CERTIFICATE-----" "$WORK/bundle.pem"; then
    echo "The panel did not return a usable bundle." >&2
    exit 1
fi

echo "Downloading the node installer"
curl -fsSL '` + installerURL + `' -o "$WORK/next-node.sh"

echo "Installing"
bash "$WORK/next-node.sh" install --binary --bundle-file "$WORK/bundle.pem" </dev/null

# The panel dials these; a node that installs but cannot be reached looks
# exactly like a broken install.
if command -v ufw >/dev/null 2>&1; then
    ufw allow 62050/tcp >/dev/null 2>&1 || true
    ufw allow 62052/tcp >/dev/null 2>&1 || true
fi

echo "Done. The node should turn Connected in the panel within a minute."
`
}

const nodeInstallerRepo = "aliking1367/next"
