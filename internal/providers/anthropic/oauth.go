// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package anthropic

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	claudeClientID      = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	authEndpoint        = "https://claude.ai/oauth/authorize"
	tokenEndpoint       = "https://platform.claude.com/v1/oauth/token"
	oauthScopes         = "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	proxyVersion        = "2.1.77"
	billingHeaderPrefix = "x-anthropic-billing-header:"
)

type TokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope"`
}

func (t *TokenSet) IsExpired() bool {
	return time.Now().After(t.ExpiresAt.Add(-5 * time.Minute))
}

// tokenFilePath returns where the OAuth tokens for the provider named
// name live. Keyed by provider name so several anthropic-type providers
// (e.g. two different OAuth orgs) keep separate credentials. The name
// comes from baifo.yaml, so characters unsafe for filenames are mapped
// to '_'.
func tokenFilePath(dir, name string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, name)
	return filepath.Join(dir, "oauth_"+safe+".json")
}

// RunOAuthFlow triggers the interactive login for the provider named
// name, storing the tokens in its per-provider file.
func RunOAuthFlow(dir, name string) error {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return fmt.Errorf("generate PKCE: %w", err)
	}
	state, err := randomBase64URL(32)
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}
	redirectURI := "https://platform.claude.com/oauth/code/callback"

	p := url.Values{}
	p.Set("client_id", claudeClientID)
	p.Set("response_type", "code")
	p.Set("redirect_uri", redirectURI)
	p.Set("scope", oauthScopes)
	p.Set("code_challenge", challenge)
	p.Set("code_challenge_method", "S256")
	p.Set("state", state)
	authURL := authEndpoint + "?" + p.Encode()

	fmt.Printf("\nOpening browser for Anthropic authentication...\n")
	fmt.Printf("If it doesn't open, copy this URL:\n\n%s\n\n", authURL)
	openBrowser(authURL)

	fmt.Printf("After logging in, Anthropic's page will show a code.\n")
	fmt.Printf("Copy and paste the code from the URL (the '?code=...' value) here:\n\n")
	fmt.Printf("Code: ")

	var raw string
	if _, err := fmt.Scanln(&raw); err != nil {
		return fmt.Errorf("failed to read code: %w", err)
	}
	raw = strings.TrimSpace(raw)

	code := raw
	if idx := strings.Index(raw, "#"); idx != -1 {
		code = raw[:idx]
	}

	fmt.Println("Fetching tokens...")
	ts, err := postToken(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     claudeClientID,
		"code":          code,
		"state":         state,
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	})
	if err != nil {
		return fmt.Errorf("failed to fetch tokens: %w", err)
	}

	path := tokenFilePath(dir, name)
	data, _ := json.MarshalIndent(ts, "", "  ")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("save tokens: %w", err)
	}

	fmt.Printf("Login complete.\n")
	return nil
}

// generatePKCE returns a PKCE code_verifier and its S256 code_challenge.
// 64 random bytes encode to 86 base64url chars (no padding), which satisfies
// RFC 7636's 43–128 character requirement for the verifier without truncation.
func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 64)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

// randomBase64URL returns n bytes of cryptographic randomness encoded as a
// base64url string (truncated to n characters). Returns an error if the
// system's random source fails.
func randomBase64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := base64.RawURLEncoding.EncodeToString(b)
	if len(s) > n {
		return s[:n], nil
	}
	return s, nil
}

func openBrowser(u string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{u}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", u}
	default:
		cmd, args = "xdg-open", []string{u}
	}
	exec.Command(cmd, args...).Start()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func postToken(body map[string]string) (*TokenSet, error) {
	oldRT := body["refresh_token"]

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}

		payload, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", tokenEndpoint, strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")

		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("invalid response: %s", data)
			continue
		}

		var tr tokenResponse
		if err := json.Unmarshal(data, &tr); err != nil {
			lastErr = fmt.Errorf("invalid response: %s", data)
			continue
		}
		if tr.Error != "" {
			return nil, fmt.Errorf("%s: %s", tr.Error, tr.ErrorDesc)
		}
		if tr.AccessToken == "" {
			lastErr = fmt.Errorf("missing access_token: %s", data)
			continue
		}

		rt := tr.RefreshToken
		if rt == "" {
			rt = oldRT
		}

		return &TokenSet{
			AccessToken:  tr.AccessToken,
			RefreshToken: rt,
			TokenType:    tr.TokenType,
			ExpiresIn:    tr.ExpiresIn,
			ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
			Scope:        tr.Scope,
		}, nil
	}
	return nil, lastErr
}

// OAuthTransport is an http.RoundTripper that automatically adds OAuth
// bearer tokens, handles token refresh, and injects the billing header
// required by Anthropic for OAuth users.
type OAuthTransport struct {
	Base      http.RoundTripper
	TokenFile string

	mu sync.RWMutex
	// refreshMu serialises concurrent token-refresh attempts so that only
	// one goroutine performs the postToken network call at a time.  The main
	// RWMutex (mu) is released before the call and only briefly re-acquired
	// to swap the TokenSet pointer, keeping concurrent readers unblocked.
	refreshMu sync.Mutex
	tokens    *TokenSet
}

func (t *OAuthTransport) load() (*TokenSet, error) {
	data, err := os.ReadFile(t.TokenFile)
	if err != nil {
		return nil, err
	}
	var ts TokenSet
	if err := json.Unmarshal(data, &ts); err != nil {
		return nil, err
	}
	if ts.AccessToken == "" {
		return nil, fmt.Errorf("empty token file")
	}
	return &ts, nil
}

func (t *OAuthTransport) getTokens() (*TokenSet, error) {
	t.mu.RLock()
	ts := t.tokens
	t.mu.RUnlock()

	if ts == nil {
		t.mu.Lock()
		if t.tokens == nil {
			var err error
			t.tokens, err = t.load()
			if err != nil {
				t.mu.Unlock()
				return nil, fmt.Errorf("load tokens: %w", err)
			}
		}
		ts = t.tokens
		t.mu.Unlock()
	}

	if ts.IsExpired() {
		// refreshMu ensures only one goroutine performs the postToken network
		// call at a time; others block here and then find a fresh token.
		t.refreshMu.Lock()

		// Re-check: another goroutine may have refreshed while we waited.
		t.mu.RLock()
		current := t.tokens
		t.mu.RUnlock()
		if current != ts && !current.IsExpired() {
			t.refreshMu.Unlock()
			return current, nil
		}

		// Capture refresh_token before the network call (no lock held).
		refreshToken := ts.RefreshToken

		newTs, err := postToken(map[string]string{
			"grant_type":    "refresh_token",
			"client_id":     claudeClientID,
			"refresh_token": refreshToken,
		})
		if err != nil {
			t.refreshMu.Unlock()
			return nil, fmt.Errorf("refresh token: %w", err)
		}

		t.mu.Lock()
		t.tokens = newTs
		t.mu.Unlock()

		ts = newTs
		data, _ := json.MarshalIndent(ts, "", "  ")
		_ = os.WriteFile(t.TokenFile, data, 0600)
		t.refreshMu.Unlock()
	}

	return ts, nil
}

func (t *OAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ts, err := t.getTokens()
	if err != nil {
		return nil, err
	}

	// Clone the request so we don't mutate the original
	reqClone := req.Clone(req.Context())

	// Set the Auth header
	reqClone.Header.Set("Authorization", "Bearer "+ts.AccessToken)

	// Anthropic requires the oauth-2025-04-20 beta header for OAuth tokens
	existing := reqClone.Header.Get("Anthropic-Beta")
	if existing == "" {
		reqClone.Header.Set("Anthropic-Beta", "oauth-2025-04-20")
	} else if !strings.Contains(existing, "oauth-2025-04-20") {
		reqClone.Header.Set("Anthropic-Beta", existing+",oauth-2025-04-20")
	}

	// Inject billing header into the request body if it's a Messages API call
	if reqClone.Method == "POST" && reqClone.Body != nil && strings.HasPrefix(reqClone.URL.Path, "/v1/messages") {
		bodyBytes, _ := io.ReadAll(reqClone.Body)
		reqClone.Body.Close()
		newBody := injectBillingHeader(bodyBytes)
		reqClone.Body = io.NopCloser(bytes.NewReader(newBody))
		reqClone.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(newBody)), nil
		}
		reqClone.ContentLength = int64(len(newBody))
		reqClone.Header.Set("Content-Length", fmt.Sprintf("%d", len(newBody)))
	}

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	resp, err := base.RoundTrip(reqClone)
	if err != nil {
		return nil, err
	}

	// If we got a 401, token might be revoked or expired early.
	// We could retry here, but simply failing will trigger the next run to refresh.
	return resp, nil
}

func injectBillingHeader(body []byte) []byte {
	if bytes.Contains(body, []byte(billingHeaderPrefix)) {
		return body
	}
	var msg map[string]json.RawMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return body
	}
	if _, ok := msg["messages"]; !ok {
		return body
	}

	billingBlock := map[string]string{
		"type": "text",
		"text": fmt.Sprintf("%s cc_version=%s; cc_entrypoint=cli; cch=00000;", billingHeaderPrefix, proxyVersion),
	}
	billingJSON, _ := json.Marshal(billingBlock)

	sysRaw, hasSystem := msg["system"]

	if !hasSystem || len(bytes.TrimSpace(sysRaw)) == 0 {
		msg["system"] = json.RawMessage(fmt.Sprintf("[%s]", billingJSON))
	} else {
		trimmed := bytes.TrimSpace(sysRaw)
		if trimmed[0] == '[' {
			var sysArr []json.RawMessage
			if err := json.Unmarshal(trimmed, &sysArr); err != nil {
				return body
			}
			sysArr = append([]json.RawMessage{billingJSON}, sysArr...)
			newSys, _ := json.Marshal(sysArr)
			msg["system"] = json.RawMessage(newSys)
		} else if trimmed[0] == '"' {
			var sysStr string
			if err := json.Unmarshal(trimmed, &sysStr); err != nil {
				return body
			}
			origBlock, _ := json.Marshal(map[string]interface{}{
				"type":          "text",
				"text":          sysStr,
				"cache_control": map[string]string{"type": "ephemeral", "ttl": "1h"},
			})
			msg["system"] = json.RawMessage(fmt.Sprintf("[%s,%s]", billingJSON, origBlock))
		} else {
			return body
		}
	}

	newBody, err := json.Marshal(msg)
	if err != nil {
		return body
	}
	return newBody
}
