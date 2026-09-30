package app

import (
	"fmt"
	"slices"
	"sort"
)

func verifySchPreservedClearResult(result map[string]any, ids []string) error {
	if result["preserveParts"] != true || result["instancesPreserved"] != true {
		return fmt.Errorf("part-preserving clear was not verified")
	}
	raw, ok := result["preservedPartIds"].([]any)
	if !ok {
		return fmt.Errorf("preserved part inventory unavailable")
	}
	got := []string{}
	for _, v := range raw {
		s, ok := v.(string)
		if !ok || s == "" {
			return fmt.Errorf("invalid preserved part identity")
		}
		got = append(got, s)
	}
	want := append([]string{}, ids...)
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		return fmt.Errorf("preserved part set differs from explicit target")
	}
	return nil
}

// Missing enumeration classes or warnings are unknown state, never an empty page.
func verifySchClearResult(result map[string]any, requireEmpty bool) error {
	if v, ok := result["warnings"]; ok {
		warnings, valid := v.([]any)
		if !valid || len(warnings) > 0 {
			return fmt.Errorf("page clear enumeration/deletion warnings: %v", v)
		}
	}
	groups, ok := result["deletedIds"].(map[string]any)
	if !ok {
		return fmt.Errorf("missing complete page primitive enumeration")
	}
	// The connector omits empty classes; a successful enumeration is signalled
	// by no warnings. Every reported class must still be a real array.
	for key, ids := range groups {
		if _, ok := ids.([]any); !ok {
			return fmt.Errorf("page enumeration unavailable for %s", key)
		}
	}
	remaining, ok := toFloat(result["remaining"])
	if !ok || !finiteStateNumber(remaining) || remaining < 0 {
		return fmt.Errorf("page remaining count unavailable")
	}
	if requireEmpty && remaining != 0 {
		return fmt.Errorf("page is not empty: %g non-preserved primitives remain", remaining)
	}
	return nil
}
