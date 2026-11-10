package api

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"
)

// The Fastly check sends, for each country with a Fastly config, the request
// an XHTTP client opens first (the download GET of a new session) through the
// Fastly domain. The country's inbound answers it with 200; anything else
// names what is missing in Fastly.

type tsinFastlyCheck struct {
	Country string `json:"country"`
	Port    int    `json:"port"`
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Status  int    `json:"status,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// tsinFastlyVerdict reads Fastly's answer to an XHTTP download request.
func tsinFastlyVerdict(code int, header http.Header) (state, reason string) {
	fromFastly := header.Get("X-Served-By") != "" || header.Get("Fastly-Debug-Digest") != "" || strings.Contains(strings.ToLower(header.Get("Via")), "varnish")
	switch {
	case code == http.StatusOK:
		return "ok", ""
	case code == http.StatusNotFound && !fromFastly:
		// The server answered, but on another country's path: the backend
		// condition sends this path to the wrong port.
		return "fail", "wrong_backend"
	case code == http.StatusNotFound || code == 421 || code == http.StatusInternalServerError:
		// Fastly itself does not know the domain, or has no backend for it.
		return "fail", "no_service"
	case code == http.StatusServiceUnavailable || code == http.StatusBadGateway || code == http.StatusGatewayTimeout:
		return "fail", "backend_down"
	}
	return "fail", "unexpected"
}

func (s *Server) handleCoreTSinFastlyCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	settings, err := s.loadTSinSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	host := strings.TrimSpace(settings.FastlyHost)
	if host == "" {
		writeError(w, http.StatusUnprocessableEntity, "enter the Fastly domain and press Apply first")
		return
	}
	backends := s.configRepo.TSinFastlyBackends(ctx, settings.FastlyCountries)
	if len(backends) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no country has a Fastly config yet; choose them and press Apply")
		return
	}
	results := make([]tsinFastlyCheck, 0, len(backends))
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
		for _, backend := range backends {
			results = append(results, tsinFastlyCheck{Country: backend.Country, Port: backend.Port, State: "fail", Reason: "dns", Detail: err.Error()})
		}
		writeJSON(w, http.StatusOK, map[string]any{"host": host, "results": results})
		return
	}
	for _, backend := range backends {
		item := tsinFastlyCheck{Country: backend.Country, Port: backend.Port}
		session := make([]byte, 8)
		_, _ = rand.Read(session)
		target := "https://" + host + strings.TrimRight(backend.Path, "/") + "/" + hex.EncodeToString(session)
		response, err := tsinFastlyProbe(ctx, target)
		if err != nil {
			item.State, item.Reason, item.Detail = "fail", "unreachable", err.Error()
		} else {
			item.Status = response.StatusCode
			item.State, item.Reason = tsinFastlyVerdict(response.StatusCode, response.Header)
		}
		results = append(results, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"host": host, "results": results})
}

// tsinFastlyProbe sends the XHTTP download request and returns as soon as the
// headers arrive: the body is the session's download stream, which stays
// open, so it is not read.
func tsinFastlyProbe(ctx context.Context, target string) (*http.Response, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// XHTTP refuses a request without the padding its clients send, here in
	// the Referer the way Xray's own client puts it.
	request.Header.Set("Referer", target+"?x_padding="+strings.Repeat("X", 400))
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			ForceAttemptHTTP2: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	_ = response.Body.Close()
	return response, nil
}
