// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package web is the low-level HTTP primitive used by the built-in
// browse MCP. It is a verbatim copy (modulo the package path) of
// github.com/achetronic/browse-mcp internal/web/, exposed here under
// the baifo module so it can be imported in-process.
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const (
	ProviderDuckDuckGo = "duckduckgo"
	ProviderTavily     = "tavily"
	ProviderSerper     = "serper"
)

// SearchResult represents a single search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// SearchConfig holds provider configuration.
type SearchConfig struct {
	TavilyAPIKey string
	SerperAPIKey string
}

// Search performs a web search using the specified provider.
func Search(ctx context.Context, client *http.Client, query, provider string, maxResults int, cfg SearchConfig) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 20 {
		maxResults = 20
	}

	switch provider {
	case ProviderTavily:
		if cfg.TavilyAPIKey == "" {
			return nil, fmt.Errorf("tavily API key not configured")
		}
		return searchTavily(ctx, client, query, maxResults, cfg.TavilyAPIKey)
	case ProviderSerper:
		if cfg.SerperAPIKey == "" {
			return nil, fmt.Errorf("serper API key not configured")
		}
		return searchSerper(ctx, client, query, maxResults, cfg.SerperAPIKey)
	default:
		return searchDuckDuckGo(ctx, client, query, maxResults)
	}
}

// searchDuckDuckGo searches using DuckDuckGo HTML (no API key required).
func searchDuckDuckGo(ctx context.Context, client *http.Client, query string, maxResults int) ([]SearchResult, error) {
	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	var results []SearchResult
	doc.Find(".result").Each(func(i int, s *goquery.Selection) {
		if i >= maxResults {
			return
		}
		title := strings.TrimSpace(s.Find(".result__title").Text())
		snippet := strings.TrimSpace(s.Find(".result__snippet").Text())
		link, _ := s.Find(".result__url").Attr("href")
		if link == "" {
			link, _ = s.Find("a.result__a").Attr("href")
		}
		if title != "" {
			results = append(results, SearchResult{
				Title:   title,
				URL:     link,
				Snippet: snippet,
			})
		}
	})
	return results, nil
}

// searchTavily searches using the Tavily API.
func searchTavily(ctx context.Context, client *http.Client, query string, maxResults int, apiKey string) ([]SearchResult, error) {
	payload := map[string]any{
		"api_key":        apiKey,
		"query":          query,
		"max_results":    maxResults,
		"include_answer": false,
		"search_depth":   "basic",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var data struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	out := make([]SearchResult, 0, len(data.Results))
	for _, r := range data.Results {
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return out, nil
}

// searchSerper searches using the Serper API.
func searchSerper(ctx context.Context, client *http.Client, query string, maxResults int, apiKey string) ([]SearchResult, error) {
	payload := map[string]any{"q": query, "num": maxResults}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://google.serper.dev/search", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var data struct {
		Organic []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic"`
	}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	out := make([]SearchResult, 0, len(data.Organic))
	for _, r := range data.Organic {
		out = append(out, SearchResult{Title: r.Title, URL: r.Link, Snippet: r.Snippet})
	}
	return out, nil
}

// NewHTTPClient creates a default HTTP client with a sensible timeout.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}
