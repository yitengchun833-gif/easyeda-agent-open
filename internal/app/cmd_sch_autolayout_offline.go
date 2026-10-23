package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Offline previews reuse the template planner; omitted parts remain fixed obstacles.
func offlineAutolayout(from string, spec alSpec, rules autolayoutRules, asJSON bool, stdout io.Writer) error {
	raw, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	parts, sheet, _, err := parseAutolayoutPartsChecked(snapshot, true)
	if err != nil {
		return err
	}
	modules := make([]alModuleSpec, 0, len(spec.Modules))
	for _, m := range spec.Modules {
		modules = append(modules, alModuleSpec{Name: m.Name, Zone: m.Zone, Core: m.Core, Parts: m.Parts})
	}
	report := planAutolayout(modules, parts, sheet, rules)
	report.Page = spec.Page
	if asJSON {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return err
		}
	} else {
		renderAutolayoutReport(report, false, stdout)
	}
	if !report.OK {
		return fmt.Errorf("offline autolayout: incomplete: %v", report.Errors)
	}
	return nil
}
