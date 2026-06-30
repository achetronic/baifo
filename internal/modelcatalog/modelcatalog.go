// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package modelcatalog provides pure, offline utilities to match user-provided
// model endpoints and provider types to the built-in catwalk model catalogue.
// It maps custom endpoint URLs and provider types to rich model metadata
// without performing any network I/O.
package modelcatalog

import (
	"strings"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/catwalk/pkg/embedded"
)

// MatchKind represents how a provider was resolved in the catalogue.
type MatchKind int

const (
	// MatchNone indicates the provider or URL could not be resolved.
	MatchNone MatchKind = iota
	// MatchByType indicates the lookup was resolved purely by provider type.
	MatchByType
	// MatchByURLExact indicates the URL matched an endpoint in the catalogue exactly.
	MatchByURLExact
	// MatchByURLHost indicates the URL matched a unique host in the catalogue.
	MatchByURLHost
)

type resolvedProvider struct {
	provider catwalk.Provider
	host     string
	full     string
}

var (
	validProviders []resolvedProvider
	byTypeIndex    map[string]catwalk.Provider
)

func init() {
	byTypeIndex = make(map[string]catwalk.Provider)
	all := embedded.GetAll()
	for _, p := range all {
		byTypeIndex[strings.ToLower(string(p.ID))] = p

		endpoint := strings.TrimSpace(p.APIEndpoint)
		if endpoint == "" || strings.HasPrefix(endpoint, "$") {
			continue
		}
		host, full := normalizeURL(endpoint)
		validProviders = append(validProviders, resolvedProvider{
			provider: p,
			host:     host,
			full:     full,
		})
	}
}

// normalizeURL simplifies a URL string to support robust matching.
// It trims whitespace, lowercases the string, strips leading http:// or https://,
// and removes trailing slashes. It returns the authority/host portion first,
// followed by the full cleaned URL.
func normalizeURL(u string) (string, string) {
	u = strings.ToLower(strings.TrimSpace(u))
	if strings.HasPrefix(u, "https://") {
		u = u[len("https://"):]
	} else if strings.HasPrefix(u, "http://") {
		u = u[len("http://"):]
	}
	for strings.HasSuffix(u, "/") {
		u = u[:len(u)-1]
	}
	full := u
	host := full
	if idx := strings.Index(full, "/"); idx >= 0 {
		host = full[:idx]
	}
	return host, full
}

// Resolve maps a provider type and optional custom URL to a catalogued provider.
// It resolves the provider in the exact order specified:
// 1. If the URL is empty, it looks up the provider by type.
// 2. If the URL is non-empty, it matches against non-placeholder API endpoints.
// It first attempts an exact match of the normalized URL, then falls back to a
// host-unique match.
func Resolve(providerType, url string) (catwalk.Provider, MatchKind, bool) {
	url = strings.TrimSpace(url)
	if url == "" {
		p, ok := byTypeIndex[strings.ToLower(providerType)]
		if ok {
			return p, MatchByType, true
		}
		return catwalk.Provider{}, MatchNone, false
	}

	inputHost, inputFull := normalizeURL(url)

	for _, rp := range validProviders {
		if inputFull == rp.full {
			return rp.provider, MatchByURLExact, true
		}
	}

	var matched resolvedProvider
	matchCount := 0
	for _, rp := range validProviders {
		if inputHost == rp.host {
			matchCount++
			matched = rp
		}
	}
	if matchCount == 1 {
		return matched.provider, MatchByURLHost, true
	}

	return catwalk.Provider{}, MatchNone, false
}

// ProviderByType looks up a catalogued provider solely by its type string.
// This is used for type-based lookups when custom URLs are absent.
func ProviderByType(providerType string) (catwalk.Provider, bool) {
	p, _, ok := Resolve(providerType, "")
	return p, ok
}
