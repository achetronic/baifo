// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package browse implements the in-process built-in browse MCP.
//
// Extracted (not imported) from github.com/achetronic/browse-mcp:
//
//   - internal/web/{search,fetch}.go are reused verbatim under
//     ./web/ for the low-level HTTP primitives;
//   - the mark3labs/mcp-go handler layer is replaced by plain Go funcs
//     wrapped via google.golang.org/adk/tool/functiontool.New.
//
// Three tools: web_search, web_fetch, web_download.
package browse

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/achetronic/baifo/internal/mcps/builtin/browse/web"
)

// Config bundles construction-time options for the browse builtin.
type Config struct {
	// HTTPClient is reused across every call. When nil we fall back
	// to web.NewHTTPClient().
	HTTPClient *http.Client

	// DefaultProvider is used by WebSearch when args.Provider is empty.
	// Empty here means "duckduckgo".
	DefaultProvider string

	// DownloadDir, when non-empty, scopes WebDownload destinations: any
	// path is resolved relative to it and traversal (../) is rejected.
	// Leave empty to allow downloads to arbitrary absolute paths.
	DownloadDir string

	// API keys for the optional search providers. Empty means the
	// provider is rejected with a clear error.
	TavilyAPIKey string
	SerperAPIKey string
}

// Tools is the entry point of the built-in browse MCP.
type Tools struct {
	cfg Config
}

// New constructs a Tools instance. A default HTTP client is provided
// when cfg.HTTPClient is nil.
func New(cfg Config) *Tools {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = web.NewHTTPClient()
	}
	return &Tools{cfg: cfg}
}

// web_search

// WebSearchArgs is the input of the WebSearch tool.
type WebSearchArgs struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results,omitempty"`
	Provider   string `json:"provider,omitempty"`
}

// WebSearchResult is what the LLM receives.
type WebSearchResult struct {
	Results []web.SearchResult `json:"results"`
	Total   int                `json:"total"`
}

// WebSearch queries one of the configured providers (DuckDuckGo by
// default; Tavily / Serper if their API key is set) and returns the
// list of matches.
func (t *Tools) WebSearch(ctx context.Context, args WebSearchArgs) (WebSearchResult, error) {
	if args.Query == "" {
		return WebSearchResult{}, fmt.Errorf("query is required")
	}
	provider := args.Provider
	if provider == "" {
		provider = t.cfg.DefaultProvider
	}
	if provider == "" {
		provider = web.ProviderDuckDuckGo
	}

	results, err := web.Search(ctx, t.cfg.HTTPClient, args.Query, provider, args.MaxResults, web.SearchConfig{
		TavilyAPIKey: t.cfg.TavilyAPIKey,
		SerperAPIKey: t.cfg.SerperAPIKey,
	})
	if err != nil {
		return WebSearchResult{}, fmt.Errorf("search: %w", err)
	}
	return WebSearchResult{Results: results, Total: len(results)}, nil
}

// web_fetch

// WebFetchArgs is the input of the WebFetch tool.
type WebFetchArgs struct {
	URL     string `json:"url"`
	Timeout int    `json:"timeout,omitempty"`
}

// WebFetchResult is what the LLM receives.
type WebFetchResult struct {
	URL         string `json:"url"`
	Content     string `json:"content"`
	SavedToFile string `json:"saved_to_file,omitempty"`
}

// WebFetch downloads a URL and returns its clean-text content. HTML is
// stripped of noise and converted to markdown-style text. Pages above
// 50KB are spilled to a temp file and only the first 2000 chars are
// returned inline.
func (t *Tools) WebFetch(ctx context.Context, args WebFetchArgs) (WebFetchResult, error) {
	if args.URL == "" {
		return WebFetchResult{}, fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
		return WebFetchResult{}, fmt.Errorf("url must start with http:// or https://")
	}
	res, err := web.Fetch(ctx, t.cfg.HTTPClient, args.URL, args.Timeout)
	if err != nil {
		return WebFetchResult{}, fmt.Errorf("fetch: %w", err)
	}
	return WebFetchResult{
		URL:         res.URL,
		Content:     res.Content,
		SavedToFile: res.SavedToFile,
	}, nil
}

// web_download

// WebDownloadArgs is the input of the WebDownload tool.
type WebDownloadArgs struct {
	URL      string `json:"url"`
	FilePath string `json:"file_path"`
}

// WebDownloadResult is what the LLM receives.
type WebDownloadResult struct {
	BytesWritten int64  `json:"bytes_written"`
	FilePath     string `json:"file_path"`
}

// WebDownload saves the body of url to file_path. When the Tools was
// constructed with a DownloadDir, file_path is resolved relative to it
// and traversal (../) is rejected.
func (t *Tools) WebDownload(ctx context.Context, args WebDownloadArgs) (WebDownloadResult, error) {
	if args.URL == "" {
		return WebDownloadResult{}, fmt.Errorf("url is required")
	}
	if args.FilePath == "" {
		return WebDownloadResult{}, fmt.Errorf("file_path is required")
	}
	if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
		return WebDownloadResult{}, fmt.Errorf("url must start with http:// or https://")
	}

	dest := args.FilePath
	if t.cfg.DownloadDir != "" {
		resolved := filepath.Join(t.cfg.DownloadDir, args.FilePath)
		cleanBase := filepath.Clean(t.cfg.DownloadDir)
		cleanPath := filepath.Clean(resolved)
		if !strings.HasPrefix(cleanPath, cleanBase+string(filepath.Separator)) && cleanPath != cleanBase {
			return WebDownloadResult{}, fmt.Errorf("file_path must be inside %s", t.cfg.DownloadDir)
		}
		dest = cleanPath
	}

	written, err := web.Download(ctx, t.cfg.HTTPClient, args.URL, dest)
	if err != nil {
		return WebDownloadResult{}, fmt.Errorf("download: %w", err)
	}
	return WebDownloadResult{BytesWritten: written, FilePath: dest}, nil
}
