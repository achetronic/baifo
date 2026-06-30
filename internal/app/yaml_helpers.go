// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// yamlMarshal renders a yaml.Node into its canonical text form. Kept
// here as a thin wrapper so call sites don't have to import yaml.v3
// directly; if the encoding strategy ever changes (e.g. switching to
// a different YAML library or applying a post-processor) only this
// helper needs touching.
func yamlMarshal(node *yaml.Node) (string, error) {
	if node == nil {
		return "", fmt.Errorf("yamlMarshal: nil node")
	}
	data, err := yaml.Marshal(node)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// yamlUnmarshal parses text into v. Used to materialise an MCPEntry
// (or any other struct) out of the editor buffer. Returns the
// underlying yaml.v3 error verbatim because its line/column messages
// are already operator-friendly.
func yamlUnmarshal(text string, v any) error {
	return yaml.Unmarshal([]byte(text), v)
}
