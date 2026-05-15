// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package mcps

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/oauth2"

	"github.com/achetronic/baifo/internal/storage"
)

// TokenStore persists OAuth tokens per MCP across process restarts.
type TokenStore struct {
	db *storage.DB
}

// NewTokenStore returns a store backed by storage SQLite DB.
func NewTokenStore(db *storage.DB) *TokenStore {
	return &TokenStore{db: db}
}

// Save writes tok under the given mcpName, replacing whatever was there before.
func (s *TokenStore) Save(mcpName string, tok *oauth2.Token) error {
	if s == nil || s.db == nil {
		return nil
	}
	if mcpName == "" {
		return fmt.Errorf("token store: empty mcp name")
	}
	if tok == nil {
		return s.Delete(mcpName)
	}
	data, err := json.Marshal(persistedToken{
		AccessToken:  tok.AccessToken,
		TokenType:    tok.TokenType,
		RefreshToken: tok.RefreshToken,
		Expiry:       tok.Expiry,
	})
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}

	_, err = s.db.SQL().Exec(`
		INSERT INTO oauth_tokens (mcp_name, token_data) VALUES (?, ?)
		ON CONFLICT(mcp_name) DO UPDATE SET token_data = excluded.token_data;
	`, mcpName, string(data))
	if err != nil {
		return fmt.Errorf("insert oauth token: %w", err)
	}
	return nil
}

// Load returns the token previously persisted for mcpName, or nil if no token exists.
func (s *TokenStore) Load(mcpName string) (*oauth2.Token, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var dataStr string
	err := s.db.SQL().QueryRow("SELECT token_data FROM oauth_tokens WHERE mcp_name = ?;", mcpName).Scan(&dataStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	var p persistedToken
	if err := json.Unmarshal([]byte(dataStr), &p); err != nil {
		_ = s.Delete(mcpName)
		return nil, nil
	}
	return &oauth2.Token{
		AccessToken:  p.AccessToken,
		TokenType:    p.TokenType,
		RefreshToken: p.RefreshToken,
		Expiry:       p.Expiry,
	}, nil
}

// Delete removes the token for mcpName, if any.
func (s *TokenStore) Delete(mcpName string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.SQL().Exec("DELETE FROM oauth_tokens WHERE mcp_name = ?;", mcpName)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	return nil
}

type persistedToken struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}
