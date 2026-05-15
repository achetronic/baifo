// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package secrets

import "sort"

// sortByName sorts a slice of Entry in lexicographic order of Name.
// Extracted so List stays free of inline sorting boilerplate.
func sortByName(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
}
