// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package mcps

import (
	"testing"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/storage"
)

// TestAuthorizationCodeConfig_AutoAdvertisesBoth confirms the default
// (auto) mode offers BOTH CIMD and DCR, letting the MCP SDK choose:
// CIMD when the AS supports it, DCR otherwise.
func TestAuthorizationCodeConfig_AutoAdvertisesBoth(t *testing.T) {
	spec := Spec{Name: "kc", Type: TypeHTTP, Endpoint: "https://kc.example/mcp"}

	cfg, err := authorizationCodeConfig(spec, nil, nil)
	if err != nil {
		t.Fatalf("authorizationCodeConfig: %v", err)
	}
	if cfg.ClientIDMetadataDocumentConfig == nil {
		t.Error("auto mode must advertise CIMD")
	}
	if cfg.ClientIDMetadataDocumentConfig != nil &&
		cfg.ClientIDMetadataDocumentConfig.URL != brandingCIMDURL {
		t.Errorf("CIMD URL = %q, want %q", cfg.ClientIDMetadataDocumentConfig.URL, brandingCIMDURL)
	}
	if cfg.DynamicClientRegistrationConfig == nil {
		t.Error("auto mode must advertise DCR")
	}
	if len(cfg.DynamicClientRegistrationConfig.Metadata.RedirectURIs) == 0 {
		t.Error("DCR metadata must carry the loopback redirect-URI pool")
	}
	if cfg.RedirectURL == "" {
		t.Error("RedirectURL must be pinned to one of the pool entries")
	}
}

// TestAuthorizationCodeConfig_RegistrationDCROnly confirms
// auth.registration=dcr suppresses CIMD entirely — the determinate
// escape hatch for an IdP that announces CIMD support but rejects our
// client_id URL.
func TestAuthorizationCodeConfig_RegistrationDCROnly(t *testing.T) {
	spec := Spec{
		Name: "kc", Type: TypeHTTP, Endpoint: "https://kc.example/mcp",
		Auth: config.MCPAuth{Kind: config.MCPAuthKindOAuth, Registration: config.MCPRegistrationDCR},
	}
	cfg, err := authorizationCodeConfig(spec, nil, nil)
	if err != nil {
		t.Fatalf("authorizationCodeConfig: %v", err)
	}
	if cfg.ClientIDMetadataDocumentConfig != nil {
		t.Error("registration=dcr must NOT advertise CIMD")
	}
	if cfg.DynamicClientRegistrationConfig == nil {
		t.Error("registration=dcr must advertise DCR")
	}
}

// TestAuthorizationCodeConfig_RegistrationCIMDOnly confirms
// auth.registration=cimd advertises CIMD and suppresses DCR. CIMD is
// advertised unconditionally now (the operator owns the choice; if the
// brand document is not served the AS rejects it, which is the user's
// responsibility to resolve via dcr/auto).
func TestAuthorizationCodeConfig_RegistrationCIMDOnly(t *testing.T) {
	spec := Spec{
		Name: "kc", Type: TypeHTTP, Endpoint: "https://kc.example/mcp",
		Auth: config.MCPAuth{Kind: config.MCPAuthKindOAuth, Registration: config.MCPRegistrationCIMD},
	}
	cfg, err := authorizationCodeConfig(spec, nil, nil)
	if err != nil {
		t.Fatalf("authorizationCodeConfig: %v", err)
	}
	if cfg.ClientIDMetadataDocumentConfig == nil {
		t.Error("registration=cimd must advertise CIMD")
	}
	if cfg.DynamicClientRegistrationConfig != nil {
		t.Error("registration=cimd must NOT advertise DCR")
	}
	if cfg.RedirectURL == "" {
		t.Error("cimd mode still needs a redirect URL for the SDK's exact-match check")
	}
}

// TestAuthorizationCodeConfig_CIMDIgnoresCachedDCRClient is the
// regression for "registration: cimd looks ignored". A cached client
// always comes from a previous DCR; reusing it in cimd mode would keep
// using the old DCR client and mask the mode switch. In cimd mode the
// cached client must NOT be loaded; in auto mode it must.
func TestAuthorizationCodeConfig_CIMDIgnoresCachedDCRClient(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	defer db.Close()
	clients := NewDCRClientStore(db)
	if err := clients.Save("kc", &PersistedDCRClient{ClientID: "old-dcr-client"}); err != nil {
		t.Fatalf("seed cached client: %v", err)
	}

	cimd := Spec{
		Name: "kc", Type: TypeHTTP, Endpoint: "https://kc.example/mcp",
		Auth: config.MCPAuth{Kind: config.MCPAuthKindOAuth, Registration: config.MCPRegistrationCIMD},
	}
	cfg, err := authorizationCodeConfig(cimd, clients, nil)
	if err != nil {
		t.Fatalf("authorizationCodeConfig cimd: %v", err)
	}
	if cfg.PreregisteredClient != nil {
		t.Errorf("cimd mode must not reuse the cached DCR client, got %+v", cfg.PreregisteredClient)
	}

	auto := cimd
	auto.Auth.Registration = config.MCPRegistrationAuto
	cfg, err = authorizationCodeConfig(auto, clients, nil)
	if err != nil {
		t.Fatalf("authorizationCodeConfig auto: %v", err)
	}
	if cfg.PreregisteredClient == nil || cfg.PreregisteredClient.ClientID != "old-dcr-client" {
		t.Errorf("auto mode should reuse the cached DCR client, got %+v", cfg.PreregisteredClient)
	}
}
