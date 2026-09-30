package app

import "strings"

// Schematic designators are authored identities, not display-normalized names.
// Keep the first declaration's spelling and order; only CSV whitespace and
// repeated identical entries are removed. Do not reuse PCB's uppercase sorter.
func schGroupDesignators(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, ref := range in {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

// A repeated declaration is a set comparison, so changing only input order
// does not rewrite the existing group's declaration order or timestamp.
func schGroupSameDesignators(a, b []string) bool {
	a, b = schGroupDesignators(a), schGroupDesignators(b)
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, ref := range a {
		seen[ref] = true
	}
	for _, ref := range b {
		if !seen[ref] {
			return false
		}
	}
	return true
}
