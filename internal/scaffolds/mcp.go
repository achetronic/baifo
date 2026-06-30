// SPDX-License-Identifier: Apache-2.0

package scaffolds

// MCP returns the YAML template for new MCPs.
// If you add a field to config.MCPEntry, update this scaffold.
func MCP(suggestedName string) string {
	name := suggestedName
	if name == "" {
		name = "my-mcp"
	}
	return `name: ` + name + `              # Unique identifier referenced by agents

type: http                # builtin | http | stdio

# Builtin server (when type: builtin)
# builtin: filesystem     # filesystem | browse

# HTTP server (when type: http)
endpoint: https://mcp.example.com
# insecure: false         # Skip TLS verification (not recommended)

# HTTP headers (supports ${secret:NAME} expansion)
# headers:
#   X-Tenant-ID: "acme"
#   Authorization: "Bearer ${secret:MY_API_KEY}"

# Stdio server (when type: stdio)
# command: /usr/local/bin/some-mcp
# args: ["--flag", "value"]
# env:
#   FOO: bar
# workdir: /tmp

# HTTP Authentication
auth:
  kind: none              # none | oauth
  # client_id: "..."
  # client_secret_ref: "..."
  # registration: auto   # auto (both CIMD and DCR) | dcr (Dynamic Client Registration) | cimd

# Optional tuning (type: builtin / builtin: filesystem only; ignored
# elsewhere). Sensible limits apply by default, nothing to configure.
# They cap how much text a single command, file read or search can
# return, so one accidental dump cannot flood the model's memory.
# The model is told about the effective limits and works around them.
# 0 or absent = default; positive = your own limit; -1 = no limit.
# options:
#   limit_exec_output_chars: 48000   # max text per command output (default 48000)
#   limit_read_file_chars: 120000    # max text per file read (default 120000)
#   limit_search_output_chars: 50000 # max text per search result (default 50000)
`
}
