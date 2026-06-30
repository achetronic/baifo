// SPDX-License-Identifier: Apache-2.0

package scaffolds

import (
	"strings"
	"testing"

	"github.com/achetronic/baifo/internal/config"
)

// TestMCPScaffoldCoversAllFields asserts that the scaffold mentions
// every YAML field declared on config.MCPEntry. If you add a new
// field to the struct, also document it in MCPScaffold (commented
// out is fine \u2014 the test only checks "the user can see it exists").
//
// The test is intentionally strict about field presence and loose
// about value content. Its job is to catch the "shipped a new field
// without telling anybody" pattern.
func TestMCPScaffoldCoversAllFields(t *testing.T) {
	fields := yamlFieldNames(config.MCPEntry{})
	scaffold := MCP("test")
	missing := scaffoldMentions(scaffold, fields)
	if len(missing) > 0 {
		t.Errorf("MCPScaffold missing fields: %s\nKnown fields: %s",
			strings.Join(missing, ", "), strings.Join(fields, ", "))
	}
}

// TestAgentScaffoldCoversAllFields is the agent counterpart.
func TestAgentScaffoldCoversAllFields(t *testing.T) {
	fields := yamlFieldNames(config.AgentTemplate{})
	scaffold := Agent("test")
	missing := scaffoldMentions(scaffold, fields)
	if len(missing) > 0 {
		t.Errorf("AgentScaffold missing fields: %s\nKnown fields: %s",
			strings.Join(missing, ", "), strings.Join(fields, ", "))
	}
}

// TestProviderScaffoldCoversAllFields is the provider counterpart.
func TestProviderScaffoldCoversAllFields(t *testing.T) {
	fields := yamlFieldNames(config.ProviderEntry{})
	scaffold := Provider("test")
	missing := scaffoldMentions(scaffold, fields)
	if len(missing) > 0 {
		t.Errorf("ProviderScaffold missing fields: %s\nKnown fields: %s",
			strings.Join(missing, ", "), strings.Join(fields, ", "))
	}
}
