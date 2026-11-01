package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

type contextKey string

const (
	adminContextKey             contextKey = "admin"
	recentActionBatchContextKey contextKey = "recent_action_batch"
	// apiActorContextKey carries the holder the rejection log fills in. See
	// apiActorHolder for why the principal cannot simply be read back.
	apiActorContextKey contextKey = "api_actor"
)

type adminPrincipal struct {
	ID       int64
	Username string
	Role     string
	Context  adminapp.EffectiveAdminContext
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := s.authenticate(r.Context(), r)
		if err != nil {
			writeAuthError(w, err)
			return
		}
		recordAPIActor(r, principal)
		if principal.Context.Source == adminapp.AuthSourceSession && !requestOriginAllowed(r) {
			writeError(w, http.StatusForbidden, "Invalid request origin")
			return
		}
		if configChangeBlocked(principal, r) {
			writeError(w, http.StatusForbidden, "This account can restart and run tests, but cannot change settings")
			return
		}
		ctx := context.WithValue(r.Context(), adminContextKey, principal)
		if batchID := recentActionBatchID(r); batchID != "" {
			ctx = context.WithValue(ctx, recentActionBatchContextKey, batchID)
		}
		next(w, r.WithContext(ctx))
	}
}

func recentActionBatchID(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("X-Next-Action-Batch"))
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '-' && char != '_' {
			return ""
		}
	}
	return value
}

func (s *Server) requireSudo(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		principal, _ := r.Context().Value(adminContextKey).(adminPrincipal)
		if principal.Role == string(adminapp.RoleFullAccess) {
			next(w, r)
			return
		}
		if principal.Role != string(adminapp.RoleSudo) || !sudoScopeAllowed(principal.Context.Admin.Permissions.Sudo, r.URL.Path) {
			writeError(w, http.StatusForbidden, "You're not allowed")
			return
		}
		next(w, r)
	})
}

func (s *Server) authenticate(ctx context.Context, r *http.Request) (adminPrincipal, error) {
	token := bearerToken(r)
	if token != "" {
		authCtx, err := s.adminAuth.AuthenticateBearer(ctx, token)
		if err != nil {
			return adminPrincipal{}, err
		}
		return principalFromContext(authCtx), nil
	}
	sessionToken := sessionCookieToken(r)
	if sessionToken == "" {
		return adminPrincipal{}, errors.New("missing credentials")
	}
	authCtx, err := s.adminAuth.AuthenticateSession(ctx, sessionToken)
	if err != nil {
		return adminPrincipal{}, err
	}
	return principalFromContext(authCtx), nil
}

func (s *Server) requireSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requestOriginAllowed(r) {
			writeError(w, http.StatusForbidden, "Invalid request origin")
			return
		}
		next(w, r)
	}
}

func requestOriginAllowed(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	source := strings.TrimSpace(r.Header.Get("Origin"))
	if source == "" {
		source = strings.TrimSpace(r.Header.Get("Referer"))
	}
	parsed, err := url.Parse(source)
	if err != nil || parsed.Host == "" {
		return false
	}
	forwardedHost := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
	sameOriginFetch := strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
	for _, host := range []string{r.Host, forwardedHost} {
		host = strings.TrimSpace(host)
		if strings.EqualFold(parsed.Host, host) {
			return true
		}
		// Nginx's common `Host $host` setting drops non-default public ports.
		// Only trust a hostname-only match when the browser confirms same-origin.
		candidate := &url.URL{Host: host}
		if sameOriginFetch && candidate.Port() == "" && parsed.Hostname() != "" && strings.EqualFold(parsed.Hostname(), candidate.Hostname()) {
			return true
		}
	}
	return false
}

func sudoScopeAllowed(scopes adminapp.SudoPermissionSettings, path string) bool {
	switch {
	case strings.HasPrefix(path, "/api/node"), strings.HasPrefix(path, "/api/nodes"):
		return scopes.Nodes
	case strings.HasPrefix(path, "/api/settings/backup"), strings.Contains(path, "/telegram/backup"):
		return scopes.Backups
	case strings.HasPrefix(path, "/api/settings/subscriptions"):
		return scopes.Subscriptions
	case strings.HasPrefix(path, "/api/settings/phpmyadmin"):
		return scopes.PHPMyAdmin
	case strings.HasPrefix(path, "/api/maintenance"):
		return scopes.Maintenance
	case strings.HasPrefix(path, "/api/settings"):
		return scopes.Settings
	case strings.HasPrefix(path, "/api/core"), strings.HasPrefix(path, "/api/xray"),
		strings.HasPrefix(path, "/api/inbounds"), strings.HasPrefix(path, "/api/panel/xray"),
		strings.HasPrefix(path, "/xray"), strings.HasPrefix(path, "/inbounds"):
		return scopes.Xray
	default:
		return false
	}
}

// configPathPrefixes are the routes that change how traffic is served: Xray
// config and inbounds, nodes, hosts and services.
var configPathPrefixes = []string{
	"/api/core", "/api/xray", "/api/inbounds", "/api/panel/xray",
	"/api/node", "/api/nodes", "/api/hosts", "/api/v2/services",
	"/xray", "/inbounds",
}

// supportPaths are the actions a support account may still run: restarts,
// resyncs and tests, which bring a service back without changing its setup.
var supportPaths = map[string]bool{
	"/api/core/restart":                  true,
	"/api/core/verify-protocols":         true,
	"/api/panel/xray/testOutbound":       true,
	"/api/panel/xray/testOutbounds":      true,
	"/api/panel/xray/routeTest":          true,
	"/api/hosts/certificate-fingerprint": true,
	"/api/core/access/operators":         true,
}

var supportNodeActions = []string{"/reconnect", "/restart", "/sync", "/service/restart"}

// configChangeBlocked reports whether a sudo admin without the change_config
// permission is trying to change Xray, node, host or service setup. Reads
// are always allowed, as are the actions in supportPaths and node restarts.
func configChangeBlocked(principal adminPrincipal, r *http.Request) bool {
	if principal.Role != string(adminapp.RoleSudo) || principal.Context.Admin.Permissions.Sudo.ChangeConfig {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	path := r.URL.Path
	isConfig := false
	for _, prefix := range configPathPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			isConfig = true
			break
		}
	}
	if !isConfig || supportPaths[path] {
		return false
	}
	if strings.HasPrefix(path, "/api/node/") && r.Method == http.MethodPost {
		for _, action := range supportNodeActions {
			if strings.HasSuffix(path, action) {
				return false
			}
		}
	}
	return true
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token
	}
	return ""
}

func principalFromContext(authCtx adminapp.EffectiveAdminContext) adminPrincipal {
	return adminPrincipal{
		ID:       authCtx.Admin.ID,
		Username: authCtx.Admin.Username,
		Role:     string(authCtx.Admin.Role),
		Context:  authCtx,
	}
}

func writeAuthError(w http.ResponseWriter, err error) {
	if errors.Is(err, adminapp.ErrPermissionDenied) {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// Match the Python panel's response for every token authentication failure
	// (invalid, expired, missing, etc.) so Marzban-compatible sales bots
	// recognize an expired admin token and re-authenticate via /admin/token.
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, "Could not validate credentials")
}
