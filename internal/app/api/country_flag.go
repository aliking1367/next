package api

import (
	"strings"

	"github.com/aliking1367/next/internal/app/countryflag"
)

// A user picking a config from a list reads the flag before the words, so the
// CDN host rows the panel builds carry one: "🇳🇱 Amsterdam · CDN" rather than
// "Amsterdam · CDN".
//
// The country comes from the node's name, which is the only place the panel is
// told where a node is. The table and the matching live in countryflag, shared
// with the labels on multi-location links -- a node named "آلمان" gets the same
// flag in both places, and a country added once reaches both.

// cdnHostRemark is the label a user sees for a CDN config.
func cdnHostRemark(nodeName string) string {
	name := strings.TrimSpace(nodeName)
	if name == "" {
		name = "node"
	}
	return countryflag.Label(name, name) + " · CDN"
}
