// SPDX-License-Identifier: Apache-2.0

package browse

import (
	"fmt"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ADKTools returns every tool of the built-in browse MCP wrapped as a
// google.golang.org/adk tool.Tool. The names match the original
// browse-mcp tool names so existing prompts keep working.
func (t *Tools) ADKTools() ([]tool.Tool, error) {
	defs := []toolDef{
		{
			name: "web_search",
			description: "Search the web and return a list of results with title, URL and snippet. " +
				"Default provider is DuckDuckGo (no API key needed). Set provider to use Tavily or " +
				"Serper if their API keys are configured. Recommended flow: web_search first to " +
				"find relevant URLs, then web_fetch on the most relevant ones.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name: "web_search",
						Description: "Search the web and return a list of results with title, URL and snippet. " +
							"Default provider is DuckDuckGo (no API key needed). Set provider to use Tavily or " +
							"Serper if their API keys are configured. Recommended flow: web_search first to " +
							"find relevant URLs, then web_fetch on the most relevant ones.",
					},
					func(ctx tool.Context, a WebSearchArgs) (WebSearchResult, error) {
						return t.WebSearch(ctx, a)
					},
				)
			},
		},
		{
			name: "web_fetch",
			description: "Fetch a URL and return its content as clean text. HTML is automatically " +
				"stripped of noise (scripts, nav, ads) and converted to readable text. For large " +
				"pages (>50KB) the content is saved to a temp file and the path is returned in " +
				"saved_to_file — use the filesystem tools to read it. Max response size: 5MB.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name: "web_fetch",
						Description: "Fetch a URL and return its content as clean text. HTML is automatically " +
							"stripped of noise (scripts, nav, ads) and converted to readable text. For large " +
							"pages (>50KB) the content is saved to a temp file and the path is returned in " +
							"saved_to_file — use the filesystem tools to read it. Max response size: 5MB.",
					},
					func(ctx tool.Context, a WebFetchArgs) (WebFetchResult, error) {
						return t.WebFetch(ctx, a)
					},
				)
			},
		},
		{
			name: "web_download",
			description: "Download a file from a URL and save it to disk. Use this for binary " +
				"files, PDFs, images, or any file to save locally. Returns the number of bytes " +
				"written and the final file path. When a download directory is configured, " +
				"file_path is resolved inside it and traversal is rejected.",
			build: func() (tool.Tool, error) {
				return functiontool.New(
					functiontool.Config{
						Name: "web_download",
						Description: "Download a file from a URL and save it to disk. Use this for binary " +
							"files, PDFs, images, or any file to save locally. Returns the number of bytes " +
							"written and the final file path. When a download directory is configured, " +
							"file_path is resolved inside it and traversal is rejected.",
					},
					func(ctx tool.Context, a WebDownloadArgs) (WebDownloadResult, error) {
						return t.WebDownload(ctx, a)
					},
				)
			},
		},
	}

	out := make([]tool.Tool, 0, len(defs))
	for _, d := range defs {
		tt, err := d.build()
		if err != nil {
			return nil, fmt.Errorf("build tool %q: %w", d.name, err)
		}
		out = append(out, tt)
	}
	return out, nil
}

// toolDef keeps the registration table compact.
type toolDef struct {
	name        string
	description string
	build       func() (tool.Tool, error)
}
