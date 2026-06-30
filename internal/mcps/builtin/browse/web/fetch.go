// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

const (
	// MaxFetchSize is the maximum response size to read (5MB).
	MaxFetchSize = 5 * 1024 * 1024
	// LargeContentThreshold — above this, content is saved to a temp file.
	LargeContentThreshold = 50 * 1024
)

// browserUA mimics a real browser to avoid bot blocks.
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var multipleNewlinesRe = regexp.MustCompile(`\n{3,}`)

// FetchResult holds the result of a web fetch.
type FetchResult struct {
	URL         string
	Content     string
	SavedToFile string // non-empty if content was saved to a temp file
}

// Fetch downloads a URL and returns its content as clean text. HTML is
// stripped of noise and converted to plain text. If the content is
// large, it is saved to a temp file and only a preview is returned.
func Fetch(ctx context.Context, client *http.Client, rawURL string, _ int) (*FetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchSize))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	content := string(body)
	if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		cleaned := removeNoisyElements(content)
		converted, err := convertHTMLToText(cleaned)
		if err != nil {
			return nil, fmt.Errorf("convert HTML: %w", err)
		}
		content = cleanupText(converted)
	}

	result := &FetchResult{URL: rawURL}
	if len(content) > LargeContentThreshold {
		tmp, err := os.CreateTemp("", "baifo-web-fetch-*.txt")
		if err != nil {
			return nil, fmt.Errorf("create temp file: %w", err)
		}
		if _, err := tmp.WriteString(content); err != nil {
			if closeErr := tmp.Close(); closeErr != nil {
				slog.Warn("failed to close temp file after write error", "path", tmp.Name(), "error", closeErr)
			}
			return nil, fmt.Errorf("write temp file: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return nil, fmt.Errorf("close temp file: %w", err)
		}
		result.SavedToFile = tmp.Name()
		result.Content = fmt.Sprintf("Content saved to: %s\n\nFirst 2000 chars:\n\n%s",
			tmp.Name(), content[:minInt(2000, len(content))])
	} else {
		result.Content = content
	}
	return result, nil
}

// Download saves a URL's content to a file on disk and returns the
// number of bytes written.
func Download(ctx context.Context, client *http.Client, rawURL, filePath string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("request failed with status %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return 0, fmt.Errorf("create directories: %w", err)
	}
	out, err := os.Create(filePath)
	if err != nil {
		return 0, fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return 0, fmt.Errorf("write file: %w", err)
	}
	return written, nil
}

// removeNoisyElements strips scripts, styles, nav, header, footer etc.
// from an HTML document, returning the rendered remainder.
func removeNoisyElements(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent
	}
	noisyTags := map[string]bool{
		"script": true, "style": true, "nav": true,
		"header": true, "footer": true, "aside": true,
		"noscript": true, "iframe": true, "svg": true,
	}
	var removeNodes func(*html.Node)
	removeNodes = func(n *html.Node) {
		var toRemove []*html.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && noisyTags[c.Data] {
				toRemove = append(toRemove, c)
			} else {
				removeNodes(c)
			}
		}
		for _, node := range toRemove {
			n.RemoveChild(node)
		}
	}
	removeNodes(doc)
	var buf strings.Builder
	if err := html.Render(&buf, doc); err != nil {
		return htmlContent
	}
	return buf.String()
}

// convertHTMLToText converts HTML to plain text. Tries markdown
// conversion first (preserves structure), falls back to body text.
func convertHTMLToText(htmlContent string) (string, error) {
	converter := md.NewConverter("", true, nil)
	result, err := converter.ConvertString(htmlContent)
	if err != nil {
		doc, err2 := goquery.NewDocumentFromReader(strings.NewReader(htmlContent))
		if err2 != nil {
			return "", fmt.Errorf("parse HTML: %w", err)
		}
		return doc.Find("body").Text(), nil
	}
	return result, nil
}

// cleanupText collapses repeated newlines and trims trailing whitespace
// per line.
func cleanupText(content string) string {
	content = multipleNewlinesRe.ReplaceAllString(content, "\n\n")
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// minInt is the int variant of the generic min builtin. We define it
// locally to avoid pulling cmp/golang.org/x for one call site.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
