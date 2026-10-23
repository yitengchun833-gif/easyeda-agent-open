#!/usr/bin/env python3
"""Small executable regressions for the four source-replication failure modes."""
import json
import importlib.util
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import copy
from collections import Counter
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("preflight", Path(__file__).with_name("schematic_preflight.py"))
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)


class PreflightTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "source.txt").write_text("Independent source fixture: a T rail and 3 labelled ports.")
        proof = {"source": "S", "locator": "page 1, region A"}
        self.req = {
            "sources": [{"id": "S", "path": "source.txt", "sha256": gate.sha(self.root / "source.txt")}],
            "target": {"project": "p", "doc": "d", "axis": "y-up", "units": "raw", "sheet": [0,0,200,200]},
            "depiction": [
                {"id": "tee", "kind": "junction", "at": [50,100], "net": "SIG",
                 "directions": ["left","right","down"], "evidence": proof},
                {"id": "label", "kind": "text", "text": "TO_J_1", "evidence": proof},
                {"id": "bus-label", "kind": "named_wire_budget", "net": "SIG",
                 "bounds": [0,0,100,120], "maximum": 1, "evidence": proof}],
            "parts": [{"ref": "J1", "required": {"pin_count": 3}, "selected": {"pin_count": 3},
                       "evidence": {"pin_count": proof}}]}
        self.plan = {"route": {"project": "p", "doc": "d"}, "actions": [
            {"action": "schematic.wire.create", "payload": {"points": [10,100,90,100], "net": "SIG"}},
            {"action": "schematic.wire.create", "payload": {"points": [50,60,50,100]}},
            {"action": "schematic.component.modify", "payload": {"patch": {"name": "TO_J_1"}}}]}
        self.req["sources"].append({"id": "DRAW", "path": str(gate.RULE_FILES["drawing"]),
                                    "sha256": gate.sha(gate.RULE_FILES["drawing"])})
        ids = gate.re.findall(r"^\| (SCH-(?:G|L)\d+) \|", gate.RULE_FILES["drawing"].read_text(encoding="utf-8"), gate.re.M)
        self.req["execution_rules"] = {
            "version": "fixture-r1", "loaded": {key: gate.sha(path) for key, path in gate.RULE_FILES.items()},
            "checks": [{"id": rid, "rule_id": rid, "origin": "internal", "category": "schematic",
                        "strength": "required", "disposition": "apply", "target": "fixture page",
                        "acceptance_criterion": "Apply the source rule to the fixture", "method": "review",
                        "basis": [{"kind": "source", "id": "DRAW", "locator": rid}]} for rid in ids]}
        self.confirm_rules()

    def confirm_rules(self):
        rules = self.req["execution_rules"]
        fingerprint = gate.rule_digest(self.req)
        rules["confirmation"] = {"version": rules["version"], "sha256": fingerprint,
                                 "evidence": {"source": "S", "locator": "fixture user confirmation"}}
        self.plan["rule_binding"] = {"version": rules["version"], "sha256": fingerprint}

    def run_gate(self, stage="schematic"):
        return gate.audit(self.req, self.plan, self.root, stage)

    def codes(self):
        return {r["code"] for r in self.run_gate()["findings"]}

    def test_valid_geometry_is_only_schematic_preflight(self):
        report = self.run_gate()
        self.assertTrue(report["passed"])
        self.assertTrue(report["pcb_gaps"])
        self.assertFalse(self.run_gate("pcb")["passed"])

    def test_rules_missing_unconfirmed_and_stale_plan_are_blocked(self):
        original = copy.deepcopy(self.req["execution_rules"])
        del self.req["execution_rules"]
        self.assertIn("execution_rules_missing", self.codes())
        self.req["execution_rules"] = original
        self.req["execution_rules"]["confirmation"]["version"] = "old"
        self.assertIn("rules_not_confirmed", self.codes())
        self.confirm_rules()
        self.plan["rule_binding"]["sha256"] = "stale"
        self.assertIn("plan_rules_stale", self.codes())

    def test_rule_changes_require_confirmation_and_missing_default_is_detected(self):
        self.req["execution_rules"]["checks"].pop()
        self.assertTrue({"internal_rules_missing", "rules_not_confirmed"} <= self.codes())
        self.req["execution_rules"]["loaded"]["drawing"] = "old-version"
        self.assertIn("rule_source_changed", self.codes())

    def test_each_generic_layout_rule_is_required_even_with_fresh_confirmation(self):
        original = copy.deepcopy(self.req["execution_rules"]["checks"])
        required = {f"SCH-L{i:02d}" for i in range(1, 9)}
        self.assertTrue(required <= {c["rule_id"] for c in original})
        for missing in sorted(required):
            with self.subTest(missing=missing):
                self.req["execution_rules"]["checks"] = [copy.deepcopy(c) for c in original
                                                         if c["rule_id"] != missing]
                self.confirm_rules()
                report = self.run_gate()
                self.assertFalse(report["passed"])
                finding = next(f for f in report["findings"] if f["code"] == "internal_rules_missing")
                self.assertIn(missing, str(finding))

    def test_explicit_override_needs_user_proof_and_hard_conflicts_block(self):
        check = self.req["execution_rules"]["checks"][0]
        check.update(disposition="override", reason="User asks for a region title")
        self.confirm_rules()
        self.assertIn("rule_override_unconfirmed", self.codes())
        check["decision"] = {"source": "S", "locator": "fixture explicit exception"}
        self.confirm_rules()
        self.assertTrue(self.run_gate()["passed"])
        check["strength"] = "hard"
        self.confirm_rules()
        self.assertIn("hard_rule_override", self.codes())
        check["disposition"] = "conflict"
        self.confirm_rules()
        self.assertIn("rule_unresolved", self.codes())

    def test_trial_component_disabling_is_blocked(self):
        self.plan["actions"][2]["payload"]["patch"]["addIntoPcb"] = False
        self.assertIn("component_property_protected", self.codes())
        check = next(c for c in self.req["execution_rules"]["checks"] if c["rule_id"] == "SCH-G08")
        check.update(disposition="override", reason="Fixture user explicitly requests property change",
                     decision={"source": "S", "locator": "fixture explicit property change"})
        self.confirm_rules()
        self.assertTrue(self.run_gate()["passed"])

    def test_unknown_internal_id_and_unresolved_condition_are_blocked(self):
        extra = copy.deepcopy(self.req["execution_rules"]["checks"][0])
        extra.update(id="unknown-default", rule_id="SCH-G99")
        self.req["execution_rules"]["checks"].append(extra)
        self.confirm_rules()
        self.assertIn("internal_rule_unknown", self.codes())
        self.req["execution_rules"]["checks"].pop()
        self.req["execution_rules"]["checks"][0]["disposition"] = "needs_input"
        self.confirm_rules()
        self.assertIn("rule_unresolved", self.codes())

    def test_one_read_per_source_and_no_cross_call_cache(self):
        opened = Counter()
        native = Path.open

        def counted(path, mode="r", *args, **kwargs):
            if "r" in mode:
                opened[str(path.resolve())] += 1
            return native(path, mode, *args, **kwargs)

        with patch.object(Path, "open", counted):
            self.assertTrue(self.run_gate()["passed"])
        self.assertEqual(len(opened), 3)
        self.assertTrue(all(n == 1 for n in opened.values()), opened)
        (self.root / "source.txt").write_text("changed after the first check")
        self.assertIn("source_changed", self.codes())

    def test_missing_input_branch(self):
        self.plan["actions"][0]["payload"]["points"] = [50,100,90,100]
        self.assertIn("missing_branch", self.codes())

    def test_placement_defers_but_does_not_remove_complete_checks(self):
        self.plan['actions'] = [{'action':'schematic.component.place', 'payload':{'designator':'J1','x':50,'y':50}}]
        report = gate.audit(self.req, self.plan, self.root, phase='placement')
        self.assertTrue(report['passed'])
        self.assertFalse(report['complete_plan_checked'])
        self.assertEqual(len(report['deferred_assertions']), 3)
        self.assertFalse(self.run_gate()['passed'])
        self.plan['actions'].append({'action':'schematic.save','payload':{}})
        self.assertFalse(gate.audit(self.req, self.plan, self.root, phase='placement')['passed'])

    def test_named_same_net_disconnected_is_not_a_junction(self):
        self.plan["actions"][1]["payload"]["points"] = [50,60,50,90]
        self.plan["actions"][1]["payload"]["net"] = "SIG"
        self.assertIn("missing_branch", self.codes())

    def test_lost_cross_reference_text(self):
        self.plan["actions"][2]["payload"] = {"kind": "net_port_out", "net": "SIG"}
        self.assertIn("missing_text", self.codes())

    def test_repeated_named_stubs(self):
        self.plan["actions"][1]["payload"]["net"] = "SIG"
        self.assertIn("repeated_net_assignment", self.codes())

    def test_wrong_y_axis_and_outside_sheet(self):
        self.req["target"]["axis"] = "y-down"
        self.plan["actions"][1]["payload"]["points"] = [50,-60,50,100]
        self.assertTrue({"coordinate_contract", "outside_sheet"} <= self.codes())

    def test_source_change_and_empty_coverage(self):
        (self.root / "source.txt").write_text("changed")
        self.req["depiction"] = []
        self.assertTrue({"source_changed", "no_depiction_checks"} <= self.codes())

    def test_mismatch_and_missing_part_source(self):
        self.req["parts"][0]["selected"]["pin_count"] = 2
        self.req["parts"][0]["evidence"] = {}
        self.assertTrue({"part_mismatch","part_evidence_missing"} <= self.codes())

    def test_complete_pcb_evidence(self):
        part = self.req["parts"][0]
        for key in gate.PCB_FIELDS:
            value = {"manufacturer": "Example", "mpn": "X", "package": "HDR",
                     "pin_map": {"1": "1"}, "dimensions": {"pitch_mm": 2.54}}[key]
            part["required"][key] = part["selected"][key] = value
            part["evidence"][key] = {"source": "S", "locator": "fixture package drawing"}
        self.assertTrue(self.run_gate("pcb")["passed"])

    def test_stale_document_and_malformed_points(self):
        self.plan["actions"][0]["doc"] = "other"
        self.plan["actions"][1]["payload"]["points"] = [0,0,1]
        self.assertTrue({"action_target_mismatch","invalid_geometry"} <= self.codes())

    def test_missing_or_unknown_contract_does_not_pass(self):
        self.req["depiction"][0]["kind"] = "magic"
        self.req["parts"] = []
        self.assertTrue({"unsupported_assertion","no_part_review"} <= self.codes())

    def test_cli_rejects_execution_plan_as_source(self):
        plan = self.root / "plan.json"
        plan.write_text(json.dumps(self.plan), encoding="utf-8")
        self.req["sources"][0].update(path="plan.json", sha256=gate.sha(plan))
        req = self.root / "requirements.json"
        req.write_text(json.dumps(self.req), encoding="utf-8")
        cp = subprocess.run([sys.executable, str(Path(gate.__file__)), "--requirements",
                             str(req), "--plan", str(plan)], capture_output=True,
                            text=True, encoding="utf-8")
        self.assertEqual(cp.returncode, 2)
        self.assertIn("source cannot", json.loads(cp.stdout)["error"])

    def test_cli_cannot_overwrite_source_or_plan(self):
        plan = self.root / "plan.json"
        plan.write_text(json.dumps(self.plan), encoding="utf-8")
        req = self.root / "requirements.json"
        req.write_text(json.dumps(self.req), encoding="utf-8")
        for output in (plan, req, self.root / "source.txt", gate.RULE_FILES["management"]):
            before = output.read_bytes()
            cp = subprocess.run([sys.executable, str(Path(gate.__file__)), "--requirements",
                                 str(req), "--plan", str(plan), "--out", str(output)],
                                capture_output=True, text=True, encoding="utf-8")
            self.assertEqual(cp.returncode, 2)
            self.assertIn("cannot overwrite", json.loads(cp.stdout)["error"])
            self.assertEqual(output.read_bytes(), before)


if __name__ == "__main__":
    unittest.main()
