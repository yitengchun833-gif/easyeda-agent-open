package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPcbDrcRulesSetConfigSetSelectsOnlyRuleConfig(t *testing.T) {
	for _, tc := range []struct {
		name, modules, module string
		dryRun                bool
		ignored               string
	}{
		{"single", `["ruleConfig"]`, "", true, ""},
		{"single-explicit", `["ruleConfig"]`, "ruleConfig", false, ""},
		{"multi-preview", `["ruleConfig","netRules","netClasses"]`, "ruleConfig", true, "netRules, netClasses"},
		{"multi-apply", `["ruleConfig","netRules","netClasses"]`, "ruleConfig", false, "netRules, netClasses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := `{"ruleConfig":{"Physics":{"Track":{"PWR":{"unit":"mm","form":{"data":{"1":{"defaultValue":0.508}}}}}}}}`
			if tc.ignored != "" {
				data = strings.TrimSuffix(data, "}") + `,"netRules":[{"name":"USB_DP"},{"name":"usb_dp"}],"netClasses":[]}`
			}
			file := `{"schema":"easyeda-eda-rule-config-set","schemaVersion":1,"modules":` + tc.modules + `,"data":` + data + `}`
			path := filepath.Join(t.TempDir(), "config-set.json")
			if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, captured, cleanup := newCapturingDaemon(t)
			defer cleanup()
			var stdout, stderr bytes.Buffer
			cmd := newPcbCmd(cfg, &stdout, &stderr)
			args := []string{"drc-rules-set", "--from", path}
			if tc.module != "" {
				args = append(args, "--module", tc.module)
			}
			if tc.dryRun {
				args = append(args, "--dry-run")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			var source struct{ RuleConfig map[string]any }
			if err := json.Unmarshal([]byte(data), &source); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"ruleConfiguration": source.RuleConfig, "dryRun": tc.dryRun}
			captured.mu.Lock()
			defer captured.mu.Unlock()
			if captured.action != "pcb.drc.rules.set" || !reflect.DeepEqual(captured.payload, want) {
				t.Fatalf("action=%s payload=%#v want=%#v", captured.action, captured.payload, want)
			}
			if tc.ignored != "" && !strings.Contains(stderr.String(), "not processed: "+tc.ignored) {
				t.Fatalf("missing ignored-module notice: %s", stderr.String())
			}
		})
	}
}

func TestPcbDrcRulesSetRejectsConfigSetBeforeDispatch(t *testing.T) {
	valid := `{"schema":"easyeda-eda-rule-config-set","schemaVersion":1,"modules":["ruleConfig"],"data":{"ruleConfig":{"Physics":{}}}}`
	for _, tc := range []struct{ name, file, module, want string }{
		{"schema", strings.Replace(valid, "easyeda-eda-rule-config-set", "other-format", 1), "", "unsupported rule file schema"},
		{"version", strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1), "", "schemaVersion 2"},
		{"string-version", strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":"1"`, 1), "", "only 1 is supported"},
		{"missing-schema", strings.Replace(valid, `"schema":"easyeda-eda-rule-config-set",`, "", 1), "", "requires schema"},
		{"missing-schema-and-version", `{"modules":["ruleConfig"],"data":{"ruleConfig":{"Physics":{}}}}`, "", "requires schema"},
		{"version-and-data-only", `{"schemaVersion":1,"data":{"ruleConfig":{"Physics":{}}}}`, "", "requires schema"},
		{"version-and-modules-only", `{"schemaVersion":1,"modules":["ruleConfig"]}`, "", "requires schema"},
		{"missing-version", strings.Replace(valid, `"schemaVersion":1,`, "", 1), "", "only 1 is supported"},
		{"multi-needs-selection", strings.Replace(valid, `["ruleConfig"]`, `["ruleConfig","netRules"]`, 1), "", "requires --module ruleConfig"},
		{"unsupported-module", valid, "netRules", "must be ruleConfig"},
		{"no-ruleConfig", strings.Replace(valid, `["ruleConfig"]`, `["netRules"]`, 1), "ruleConfig", "does not declare ruleConfig"},
		{"duplicate-module", strings.Replace(valid, `["ruleConfig"]`, `["ruleConfig","ruleConfig"]`, 1), "", "unique non-empty names"},
		{"bad-modules", strings.Replace(valid, `["ruleConfig"]`, `"ruleConfig"`, 1), "", "non-empty array"},
		{"missing-config", strings.Replace(valid, `"ruleConfig":{"Physics":{}}`, `"ruleConfig":null`, 1), "", "data.ruleConfig"},
		{"undeclared-data", strings.Replace(valid, `"Physics":{}}`, `"Physics":{}},"netRules":[]`, 1), "", "not declared in modules"},
		{"legacy-selection", `{"Physics":{}}`, "ruleConfig", "--module applies only"},
		{"embedded-explicit", `{"ruleConfiguration":` + valid + `}`, "", "embedded rule-config-set"},
		{"embedded-legacy", `{"rules":` + valid + `}`, "", "embedded rule-config-set"},
		{"embedded-result-rules", `{"result":{"rules":` + valid + `}}`, "", "embedded rule-config-set"},
		{"embedded-result-config", `{"result":{"ruleConfiguration":` + valid + `}}`, "", "embedded rule-config-set"},
		{"embedded-named-config", `{"name":"custom","config":` + valid + `}`, "", "embedded rule-config-set"},
		{"embedded-config-chain", `{"ruleConfiguration":{"config":{"config":` + valid + `}}}`, "", "embedded rule-config-set"},
		{"embedded-unknown-version", `{"ruleConfiguration":` + strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":2`, 1) + `}`, "", "embedded rule-config-set"},
		{"embedded-incomplete", `{"ruleConfiguration":{"modules":["ruleConfig"],"data":{"ruleConfig":{"Physics":{}}}}}`, "", "embedded rule-config-set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config-set.json")
			if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, captured, cleanup := newCapturingDaemon(t)
			defer cleanup()
			var out bytes.Buffer
			cmd := newPcbCmd(cfg, &out, &out)
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			args := []string{"drc-rules-set", "--from", path}
			if tc.module != "" {
				args = append(args, "--module", tc.module)
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			captured.mu.Lock()
			defer captured.mu.Unlock()
			if err == nil || !strings.Contains(err.Error(), tc.want) || captured.action != "" {
				t.Fatalf("error=%v, want %q without dispatch; action=%s", err, tc.want, captured.action)
			}
		})
	}
}

func TestPcbDrcRulesSetLegacyMetadataAndNetCaseRemainOpaque(t *testing.T) {
	rules := `{"schema":"host-specific","schemaVersion":9,"Physics":{}}`
	named := `{"name":"Custom","config":` + rules + `}`
	netRules := `[{"name":"USB_DP"},{"name":"usb_dp"}]`
	for _, tc := range []struct{ name, input, want string }{
		{"explicit", `{"ruleConfiguration":` + rules + `,"netRules":` + netRules + `}`, `{"ruleConfiguration":` + rules + `,"netRules":` + netRules + `}`},
		{"bare", rules, `{"ruleConfiguration":` + rules + `}`},
		{"named", named, `{"ruleConfiguration":` + named + `}`},
		{"legacy", `{"rules":` + rules + `,"netRules":` + netRules + `}`, `{"ruleConfiguration":` + rules + `,"netRules":` + netRules + `}`},
		{"action", `{"ok":true,"result":{"rules":` + named + `,"netRules":` + netRules + `}}`, `{"ruleConfiguration":` + named + `,"netRules":` + netRules + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, ignored, err := parsePcbDrcRulesSetSpec([]byte(tc.input), "")
			if err != nil || len(ignored) != 0 {
				t.Fatalf("error=%v ignored=%v", err, ignored)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(spec, want) {
				t.Fatalf("legacy values changed: %#v", spec)
			}
		})
	}
}
