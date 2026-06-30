// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package browse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTestServer starts an httptest.Server with the given handler and
// returns Tools wired to it. Test ensures the server is closed.
func withTestServer(t *testing.T, h http.Handler) *Tools {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Config{HTTPClient: srv.Client()})
}

func TestWebFetchHTMLStripsNoise(t *testing.T) {
	body := `<html><head><style>x</style></head><body>
<nav>nav</nav>
<main><p>Real content here.</p></main>
<script>evil()</script>
</body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	tools := New(Config{HTTPClient: srv.Client()})
	res, err := tools.WebFetch(context.Background(), WebFetchArgs{URL: srv.URL})
	if err != nil {
		t.Fatalf("WebFetch: %v", err)
	}
	if !strings.Contains(res.Content, "Real content here") {
		t.Errorf("expected main content in result, got %q", res.Content)
	}
	if strings.Contains(res.Content, "evil()") {
		t.Errorf("script content leaked: %q", res.Content)
	}
}

func TestWebFetchRejectsNonHTTP(t *testing.T) {
	tools := New(Config{})
	if _, err := tools.WebFetch(context.Background(), WebFetchArgs{URL: "ftp://x"}); err == nil {
		t.Error("expected error for non-http URL")
	}
}

func TestWebFetchRequiresURL(t *testing.T) {
	tools := New(Config{})
	if _, err := tools.WebFetch(context.Background(), WebFetchArgs{}); err == nil {
		t.Error("expected error for missing URL")
	}
}

func TestWebDownloadWritesFile(t *testing.T) {
	payload := []byte("binary payload bytes\x00\x01")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	tools := New(Config{HTTPClient: srv.Client()})

	res, err := tools.WebDownload(context.Background(), WebDownloadArgs{
		URL:      srv.URL,
		FilePath: dest,
	})
	if err != nil {
		t.Fatalf("WebDownload: %v", err)
	}
	if res.BytesWritten != int64(len(payload)) {
		t.Errorf("bytes_written: got %d, want %d", res.BytesWritten, len(payload))
	}
	got, _ := os.ReadFile(dest)
	if string(got) != string(payload) {
		t.Errorf("downloaded content mismatch")
	}
}

func TestWebDownloadRespectsDownloadDir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hi"))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tools := New(Config{HTTPClient: srv.Client(), DownloadDir: dir})

	// Inside the dir → accepted, resolved.
	res, err := tools.WebDownload(context.Background(), WebDownloadArgs{
		URL:      srv.URL,
		FilePath: "ok.bin",
	})
	if err != nil {
		t.Fatalf("inside dir: %v", err)
	}
	if !strings.HasPrefix(res.FilePath, dir) {
		t.Errorf("FilePath not resolved into download dir: %q", res.FilePath)
	}

	// Traversal → rejected.
	if _, err := tools.WebDownload(context.Background(), WebDownloadArgs{
		URL:      srv.URL,
		FilePath: "../escape.bin",
	}); err == nil {
		t.Error("expected error for traversal, got nil")
	}
}

func TestWebSearchRequiresQuery(t *testing.T) {
	tools := New(Config{})
	if _, err := tools.WebSearch(context.Background(), WebSearchArgs{}); err == nil {
		t.Error("expected error for empty query")
	}
}

func TestWebSearchRejectsTavilyWithoutKey(t *testing.T) {
	tools := New(Config{})
	if _, err := tools.WebSearch(context.Background(), WebSearchArgs{
		Query:    "x",
		Provider: "tavily",
	}); err == nil {
		t.Error("expected error for tavily without key")
	}
}

func TestADKToolsBuildEveryTool(t *testing.T) {
	tools := New(Config{})
	list, err := tools.ADKTools()
	if err != nil {
		t.Fatalf("ADKTools: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("ADKTools count: got %d, want 3", len(list))
	}
	want := map[string]bool{"web_search": true, "web_fetch": true, "web_download": true}
	for _, tl := range list {
		if !want[tl.Name()] {
			t.Errorf("unexpected tool name %q", tl.Name())
		}
		if tl.Description() == "" {
			t.Errorf("tool %q has empty description", tl.Name())
		}
	}
}

// Silence the unused-helper warning for withTestServer in case nobody
// ends up referencing it directly. Kept available because future tests
// will likely need it.
var _ = withTestServer
