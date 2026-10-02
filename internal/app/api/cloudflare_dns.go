package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The panel can create the DNS records a CDN-fronted host needs, so an admin
// does not have to add one orange-cloud record per node by hand -- the step
// where a record is forgotten, or created grey, and the config then reaches
// the origin directly and defeats the point.
//
// The token is used for the one request that asked for it and is never
// stored, never logged and never written back in a response: it can edit the
// admin's whole zone, so the panel holds it no longer than it takes to use.

const cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

type cloudflareClient struct {
	token string
	http  *http.Client
}

func newCloudflareClient(token string) cloudflareClient {
	return cloudflareClient{
		token: strings.TrimSpace(token),
		http:  &http.Client{Timeout: 20 * time.Second},
	}
}

type cloudflareError struct {
	Message string
}

func (e cloudflareError) Error() string { return e.Message }

type cloudflareResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (c cloudflareClient) do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, cloudflareAPIBase+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		// The URL carries no secret, but the error text is shown to an admin,
		// so it stays about the destination rather than the request.
		return nil, cloudflareError{Message: "could not reach the Cloudflare API"}
	}
	defer response.Body.Close()

	var decoded cloudflareResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, cloudflareError{Message: fmt.Sprintf("Cloudflare answered %d with an unreadable body", response.StatusCode)}
	}
	if !decoded.Success {
		if len(decoded.Errors) > 0 {
			return nil, cloudflareError{Message: decoded.Errors[0].Message}
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusUnauthorized {
			return nil, cloudflareError{Message: "the token was refused; it needs Zone:Read and DNS:Edit on this zone"}
		}
		return nil, cloudflareError{Message: fmt.Sprintf("Cloudflare answered %d", response.StatusCode)}
	}
	return decoded.Result, nil
}

// zoneID finds the zone a hostname belongs to. An admin pastes the name of
// the zone they manage, which may itself be a subdomain, so this walks up the
// name until Cloudflare recognises one.
func (c cloudflareClient) zoneID(ctx context.Context, name string) (string, error) {
	candidate := strings.Trim(strings.ToLower(strings.TrimSpace(name)), ".")
	for strings.Contains(candidate, ".") {
		result, err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(candidate), nil)
		if err != nil {
			return "", err
		}
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(result, &zones); err != nil {
			return "", cloudflareError{Message: "could not read the zone list"}
		}
		if len(zones) > 0 {
			return zones[0].ID, nil
		}
		_, rest, found := strings.Cut(candidate, ".")
		if !found {
			break
		}
		candidate = rest
	}
	return "", cloudflareError{Message: "no Cloudflare zone matches " + name + "; check the token has access to it"}
}

type cloudflareDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

// upsertProxiedRecord points a hostname at an address through Cloudflare. It
// replaces whatever is there rather than adding a second record: two records
// for one name would round-robin, and half the connections would land on the
// wrong node.
func (c cloudflareClient) upsertProxiedRecord(ctx context.Context, zoneID, hostname, address string) error {
	recordType := "A"
	if ip := net.ParseIP(address); ip != nil && ip.To4() == nil {
		recordType = "AAAA"
	}
	result, err := c.do(ctx, http.MethodGet,
		"/zones/"+zoneID+"/dns_records?name="+url.QueryEscape(hostname), nil)
	if err != nil {
		return err
	}
	var existing []cloudflareDNSRecord
	if err := json.Unmarshal(result, &existing); err != nil {
		return cloudflareError{Message: "could not read the existing DNS records"}
	}

	payload := map[string]any{
		"type":    recordType,
		"name":    hostname,
		"content": address,
		// Proxied is the whole point: an unproxied record hands the origin IP
		// straight back to the client.
		"proxied": true,
		"ttl":     1,
		"comment": "Next panel CDN host",
	}
	for _, record := range existing {
		if strings.EqualFold(record.Name, hostname) {
			_, err := c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+record.ID, payload)
			return err
		}
	}
	_, err = c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", payload)
	return err
}
