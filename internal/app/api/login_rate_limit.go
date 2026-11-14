package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	loginMaxFailures     = 5
	loginFailureWindow   = 5 * time.Minute
	loginAttemptCapacity = 4096
	// loginMaxFailuresPerIP caps one address across every username, so it
	// cannot try a password against many accounts, nor fill the table with
	// made-up usernames until new keys stop being tracked.
	loginMaxFailuresPerIP = 20
)

// cloudflareNetworks are Cloudflare's published edge ranges. A request whose
// TCP peer is one of them came through Cloudflare, and only then is its
// CF-Connecting-IP header the real client; anyone else could write it.
var cloudflareNetworks = func() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range strings.Fields(`173.245.48.0/20 103.21.244.0/22 103.22.200.0/22 103.31.4.0/22
141.101.64.0/18 108.162.192.0/18 190.93.240.0/20 188.114.96.0/20 197.234.240.0/22 198.41.128.0/17
162.158.0.0/15 104.16.0.0/13 104.24.0.0/14 172.64.0.0/13 131.0.72.0/22
2400:cb00::/32 2606:4700::/32 2803:f800::/32 2405:b500::/32 2405:8100::/32 2a06:98c0::/29 2c0f:f248::/32`) {
		if _, network, err := net.ParseCIDR(cidr); err == nil {
			out = append(out, network)
		}
	}
	return out
}()

func ipInNetworks(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// loginClientIP is the address login attempts are counted against. Behind
// Cloudflare the TCP peer is a Cloudflare edge, shared by everyone, so the
// client is taken from CF-Connecting-IP when, and only when, the peer is a
// Cloudflare edge. Behind a proxy on the same machine or network (the peer is
// loopback or private) X-Real-IP or the first X-Forwarded-For is used.
// Otherwise the peer itself.
func loginClientIP(r *http.Request) string {
	peer := strings.TrimSpace(requestPeer(r))
	ip := net.ParseIP(strings.Trim(peer, "[]"))
	if ip == nil {
		return peer
	}
	header := func(name string) string {
		value := strings.TrimSpace(strings.Split(r.Header.Get(name), ",")[0])
		if parsed := net.ParseIP(value); parsed != nil {
			return parsed.String()
		}
		return ""
	}
	if ipInNetworks(ip, cloudflareNetworks) {
		if client := header("CF-Connecting-IP"); client != "" {
			return client
		}
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		for _, name := range []string{"CF-Connecting-IP", "X-Real-IP", "X-Forwarded-For"} {
			if client := header(name); client != "" {
				return client
			}
		}
	}
	return ip.String()
}

// loginIPKey is the per-address counter beside a per-username key.
func loginIPKey(key string) string {
	ip, _, _ := strings.Cut(key, "\x00")
	return ip + "\x00*"
}

type loginAttempt struct {
	failures int
	resetAt  time.Time
}

type loginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	now      func() time.Time
}

func loginRateLimitKey(r *http.Request, username string) string {
	remote := strings.TrimSpace(loginClientIP(r))
	if remote == "" {
		remote = "unknown"
	}
	return remote + "\x00" + strings.ToLower(strings.TrimSpace(username))
}

func (l *loginRateLimiter) allowed(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	if ipAttempt, ok := l.attempts[loginIPKey(key)]; ok && now.Before(ipAttempt.resetAt) && ipAttempt.failures >= loginMaxFailuresPerIP {
		return false
	}
	attempt, ok := l.attempts[key]
	if !ok {
		l.removeExpired(now)
		return len(l.attempts) < loginAttemptCapacity
	}
	if !now.Before(attempt.resetAt) {
		delete(l.attempts, key)
		return true
	}
	return attempt.failures < loginMaxFailures
}

func (l *loginRateLimiter) recordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	l.countFailure(loginIPKey(key), now)
	l.countFailure(key, now)
}

func (l *loginRateLimiter) countFailure(key string, now time.Time) {
	attempt, ok := l.attempts[key]
	if ok && !now.Before(attempt.resetAt) {
		delete(l.attempts, key)
		ok = false
	}
	if !ok && len(l.attempts) >= loginAttemptCapacity {
		l.removeExpired(now)
		if len(l.attempts) >= loginAttemptCapacity {
			return
		}
	}
	attempt.failures++
	attempt.resetAt = now.Add(loginFailureWindow)
	if l.attempts == nil {
		l.attempts = make(map[string]loginAttempt)
	}
	l.attempts[key] = attempt
}

func (l *loginRateLimiter) removeExpired(now time.Time) {
	for key, attempt := range l.attempts {
		if !now.Before(attempt.resetAt) {
			delete(l.attempts, key)
		}
	}
}

func (l *loginRateLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

func (l *loginRateLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func writeLoginRateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(loginFailureWindow.Seconds())))
	writeError(w, http.StatusTooManyRequests, "too many login attempts; try again later")
}
