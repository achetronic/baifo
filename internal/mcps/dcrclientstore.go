// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package mcps

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/achetronic/baifo/internal/storage"
)

// DCRClientStore persists the client credentials that a Dynamic
// Client Registration (RFC 7591) handshake produced for each MCP.
type DCRClientStore struct {
	db *storage.DB
}

// NewDCRClientStore returns a store backed by storage SQLite DB.
func NewDCRClientStore(db *storage.DB) *DCRClientStore {
	return &DCRClientStore{db: db}
}

// PersistedDCRClient is the on-disk shape of a registered client.
type PersistedDCRClient struct {
	Issuer                  string `json:"issuer,omitempty"`
	ClientID                string `json:"client_id"`
	ClientSecret            string `json:"client_secret,omitempty"`
	RegistrationAccessToken string `json:"registration_access_token,omitempty"`
	RegistrationClientURI   string `json:"registration_client_uri,omitempty"`
}

// Save writes c under the given mcpName, replacing whatever was there before.
func (s *DCRClientStore) Save(mcpName string, c *PersistedDCRClient) error {
	if s == nil || s.db == nil {
		return nil
	}
	if mcpName == "" {
		return fmt.Errorf("dcr client store: empty mcp name")
	}
	if c == nil {
		return s.Delete(mcpName)
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal dcr client: %w", err)
	}

	_, err = s.db.SQL().Exec(`
		INSERT INTO oauth_clients (mcp_name, client_data) VALUES (?, ?)
		ON CONFLICT(mcp_name) DO UPDATE SET client_data = excluded.client_data;
	`, mcpName, string(data))
	if err != nil {
		return fmt.Errorf("insert oauth client: %w", err)
	}
	return nil
}

// Load returns the persisted client for mcpName, or nil if none exists.
func (s *DCRClientStore) Load(mcpName string) (*PersistedDCRClient, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var dataStr string
	err := s.db.SQL().QueryRow("SELECT client_data FROM oauth_clients WHERE mcp_name = ?;", mcpName).Scan(&dataStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	var c PersistedDCRClient
	if err := json.Unmarshal([]byte(dataStr), &c); err != nil {
		_ = s.Delete(mcpName)
		return nil, nil
	}
	if c.ClientID == "" {
		_ = s.Delete(mcpName)
		return nil, nil
	}
	return &c, nil
}

// Delete removes the persisted client for mcpName, if any.
func (s *DCRClientStore) Delete(mcpName string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.SQL().Exec("DELETE FROM oauth_clients WHERE mcp_name = ?;", mcpName)
	if err != nil {
		return fmt.Errorf("delete dcr client: %w", err)
	}
	return nil
}
