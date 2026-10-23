"""Offline cases and independent-process revision reuse; no EDA calls."""
import copy
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

from module_reuse import ROOT, evaluate, rules

EXAMPLE = ROOT / "library/modules/ldo-3v3.request.example.json"


class ReuseTests(unittest.TestCase):
    def setUp(self):
        self.request = rules.read_json(EXAMPLE)

    def test_applicable_draft(self):
        result = evaluate(self.request)
        self.assertEqual(result["module_id"], "lib.ldo_3v3")
        self.assertEqual(result["status"], "candidate")
        self.assertEqual(result["differences"], [])
        self.assertTrue(result["draft"]["components"])
        self.assertTrue(result["draft"]["nets"])
        self.assertEqual(result["verification"]["electrical"], "unknown")
        self.assertEqual(result["jev_calls"], 0)

    def test_changed_condition(self):
        self.request["profile"]["facts"]["load_current_a"]["value"] = 0.2
        result = evaluate(self.request)
        self.assertEqual(result["status"], "candidate")
        self.assertEqual(result["differences"][0]["field"], "load_current_a")
        self.assertEqual(result["draft"]["requested_parameters"]["load_current_a"]["value"], 0.2)
        self.assertIn("thermal", result["draft"]["required_checks"])

    def test_missing_condition(self):
        del self.request["profile"]["facts"]["load_current_a"]
        result = evaluate(self.request)
        self.assertEqual(result["status"], "needs_input")
        self.assertEqual(result["label"], "需要补信息")
        self.assertIsNone(result["draft"])
        self.assertIn("load_current_a", result["selection"]["missing_facts"])

    def test_outside_scope(self):
        self.request["profile"]["facts"]["output_voltage_v"]["value"] = 5
        result = evaluate(self.request)
        self.assertEqual(result["status"], "excluded")
        self.assertEqual(result["label"], "不适用")
        self.assertIsNone(result["draft"])

    def test_unknown_unproven_invalid_and_mismatch_priority(self):
        for value in (None,):
            request = copy.deepcopy(self.request)
            request["profile"]["facts"]["load_current_a"]["value"] = value
            self.assertEqual(evaluate(request)["status"], "needs_input")
        request = copy.deepcopy(self.request)
        del request["profile"]["facts"]["input_voltage_v"]["provenance"]
        self.assertEqual(evaluate(request)["status"], "needs_input")
        request["profile"]["facts"]["output_voltage_v"]["value"] = 5
        self.assertEqual(evaluate(request)["status"], "excluded")
        for value in (True, "5V", float("nan"), -1):
            request = copy.deepcopy(self.request)
            request["profile"]["facts"]["input_voltage_v"]["value"] = value
            with self.assertRaises(ValueError):
                evaluate(request)

    def test_search_without_assuming_first_candidate(self):
        for query in ("not-a-module", "电源"):
            self.request["query"] = query
            self.assertEqual(evaluate(self.request)["status"], "needs_input")
        self.request["query"] = "lib.ldo_3v3"
        self.assertEqual(evaluate(self.request)["status"], "candidate")

    def test_second_process_adopts_persisted_revision(self):
        # All mutations stay in an isolated temporary copy inside this workspace.
        with tempfile.TemporaryDirectory(prefix="reuse-check-", dir=ROOT / "library/modules") as folder:
            root = Path(folder)
            (root / "library/modules").mkdir(parents=True)
            (root / "references").mkdir()
            for name in ("catalog.json", "ldo-3v3.knowledge.json", "ams1117-3v3.layout-input.json"):
                shutil.copyfile(ROOT / "library/modules" / name, root / "library/modules" / name)
            for name in ("standard-parts.json", "reusable-module-library.md", "schematic-replication.md"):
                shutil.copyfile(ROOT / "references" / name, root / "references" / name)
            request_path = root / "request.json"

            def invoke():
                request_path.write_text(json.dumps(self.request), encoding="utf-8")
                cp = subprocess.run([sys.executable, str(ROOT / "scripts/module_reuse.py"),
                                     str(request_path), "--library-root", str(root)],
                                    capture_output=True, text=True, encoding="utf-8", check=True)
                return json.loads(cp.stdout)

            first = invoke()
            path = root / "library/modules/ldo-3v3.knowledge.json"
            card = rules.read_json(path)
            card["revision"] = "2-simulated-review"
            note = "模拟工程师修订：第二次草案必须附输出电容 ESR 复核记录。"
            card["review_notes"].append(note)
            card["required_checks"].append("output_capacitor_esr_review")
            path.write_text(json.dumps(card, ensure_ascii=False), encoding="utf-8")
            self.request["profile"]["target_id"] = "synthetic-task-2:power"
            self.request["profile"]["facts"]["load_current_a"]["value"] = 0.2
            second = invoke()
            self.assertNotEqual(first["knowledge_sha256"], second["knowledge_sha256"])
            self.assertEqual(second["revision"], "2-simulated-review")
            self.assertIn(note, second["draft"]["review_notes"])
            self.assertIn("output_capacitor_esr_review", second["draft"]["required_checks"])
            self.assertEqual(second["draft"]["asset_sha256"], first["draft"]["asset_sha256"])
            self.assertEqual(second["verification"]["engineer_review"], "pending")
            card["sources"][0]["path"] = "../outside.json"
            path.write_text(json.dumps(card), encoding="utf-8")
            with self.assertRaises(ValueError):
                evaluate(self.request, root)


if __name__ == "__main__":
    unittest.main(verbosity=2)
