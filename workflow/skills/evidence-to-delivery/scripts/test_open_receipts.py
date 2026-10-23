#!/usr/bin/env python3
"""Public workflow CLI coverage for imported Open stage receipts; no EDA calls."""
import base64
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


CLI = Path(__file__).with_name("workflow.py")


class OpenDependencyTests(unittest.TestCase):
    def test_display_needs_native_proof_and_remote_writes_invalidate(self):
        from open_receipts import stage_invalidates_requirement
        target = {'projectUuid': 'p', 'documentUuid': 'd'}
        stage = {'check': 'geometry', 'primitiveIds': ['r'], 'steps': [
            {'action': 'schematic.attribute.modify', 'payload': {'primitiveId': 'a', 'props': {'x': 5}}}]}
        result = {'rules': {'status': 'pass', 'items': [{'id': 'identity', 'status': 'pass', 'coverage': 'checked'}]},
                  'results': [{'action': 'schematic.attribute.modify', 'ok': True, 'result': {'verified': True, 'primitiveId': 'a',
                               'attribute': {'primitiveId': 'a', 'ParentPrimitiveId': 'r', 'Key': 'Designator', 'Value': 'R1', 'X': 5}}}]}
        report = {'stage': {'completed': True}, 'execution': {'ok': True, 'context': target, 'result': result}}
        self.assertFalse(stage_invalidates_requirement(stage, 'pin_nets', report, target))
        self.assertTrue(stage_invalidates_requirement(stage, 'text_overlap', report, target))
        result['results'][0]['result']['verified'] = False
        self.assertTrue(stage_invalidates_requirement(stage, 'pin_nets', report, target))
        self.assertTrue(stage_invalidates_requirement({'primitiveIds': ['remote'], 'routeFrom': 'measure', 'steps': []}, 'pin_nets', {}, target))

    def test_incremental_prefix_binds_acceptance_scope_and_rejects_changed_hash(self):
        from types import SimpleNamespace
        from open_receipts import prefix_binding
        from workflow import digest

        def require(condition, message):
            if not condition:
                raise ValueError(message)

        root = Path.cwd()
        stage = {"id": "place", "check": "geometry", "steps": []}
        semantic = {"components": [], "nets": [], "idMap": {"resistor": "r"}}
        sources = [{"id": "SRC", "path": "source.json", "sha256": "a" * 64}]
        plan = {"taskKind": "incremental_module", "acceptanceScope": "module", "semanticTarget": semantic,
                "sources": sources, "stages": [stage], "executionReceipts": {"directory": "receipts"}}
        packet = {"sources": sources, "binding": {"receiptDirectory": "receipts", "idMap": {"resistor": "r"},
                  "semanticTargetSha256": digest({k: v for k, v in semantic.items() if k != "idMap"}),
                  "sourcesSha256": digest(sources), "stageSha256": digest(stage),
                  "acceptanceSha256": digest({"acceptanceScope": "module", "taskKind": "incremental_module"})}}
        workflow = SimpleNamespace(require=require, digest=digest, resolve=lambda base, value: (base / value).resolve())
        expected = prefix_binding(workflow, root, plan, stage, packet)
        self.assertEqual(expected["acceptanceSha256"], packet["binding"]["acceptanceSha256"])
        packet["binding"]["acceptanceSha256"] = digest({"acceptanceScope": "page", "taskKind": "incremental_module"})
        with self.assertRaisesRegex(ValueError, "immutable binding changed"):
            prefix_binding(workflow, root, plan, stage, packet)


class OpenReceiptWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="open-receipt-")
        self.root = Path(self.temp.name)
        self.cli("init", "--goal", "import Open execution evidence")
        self.apply({"op": "source", "source": {
            "id": "DEC-1", "kind": "decision", "text": "fixture design", "locator": "fixture"
        }})

    def tearDown(self):
        self.temp.cleanup()

    def cli(self, *args, ok=True):
        cp = subprocess.run([sys.executable, str(CLI), "--root", str(self.root), *args],
                            capture_output=True, text=True, encoding="utf-8")
        self.assertEqual(cp.returncode == 0, ok, (args, cp.stdout, cp.stderr))
        return json.loads(cp.stdout) if cp.stdout.strip().startswith("{") else cp.stdout

    def state(self):
        return json.loads((self.root / ".workflow/state.json").read_text(encoding="utf-8"))

    def apply(self, operation, ok=True):
        path = self.root / "operation.json"
        path.write_text(json.dumps(operation), encoding="utf-8")
        return self.cli("apply", str(path), "--expect-generation", str(self.state()["generation"]),
                        "--reconcile-views", "fixture generated evidence is not a plan decision", ok=ok)

    def apply_error(self, operation):
        path = self.root / "operation.json"
        path.write_text(json.dumps(operation), encoding="utf-8")
        cp = subprocess.run([sys.executable, str(CLI), "--root", str(self.root), "apply", str(path),
                             "--expect-generation", str(self.state()["generation"]), "--reconcile-views",
                             "fixture generated evidence is not a plan decision"],
                            capture_output=True, text=True, encoding="utf-8")
        self.assertNotEqual(cp.returncode, 0, (cp.stdout, cp.stderr))
        return cp.stderr

    def write_json(self, name, value):
        path = self.root / name
        path.write_text(json.dumps(value), encoding="utf-8")
        return path

    def digest(self, path):
        return hashlib.sha256(path.read_bytes()).hexdigest()

    @property
    def target(self):
        return {"projectUuid": "project-fixture", "documentUuid": "document-fixture"}

    def part(self, primitive_id="C1"):
        return {"primitiveId": primitive_id, "uniqueId": "u1", "designator": "C1", "pinsAvailable": True,
                "bbox": {"minX": 1, "minY": 2, "maxX": 3, "maxY": 4},
                "pins": [{"pinNumber": "1", "net": "VIN", "noConnected": False, "x": 1, "y": 2, "rotation": 0}]}

    def envelope(self, result, context=None):
        value = {"structuredContent": {"result": result}}
        if context is not None:
            value["context"] = context
        return value

    def open_plan(self, stage="geometry", steps=None, primitive_ids=None, requirements=None, name="open-plan.json"):
        source = self.write_json("open-source.json", {"kind": "fixture"})
        plan = {
            "schemaVersion": "easyeda.plan/1", "revision": 1, "target": self.target,
            "sources": [{"id": "SRC-OPEN", "path": str(source), "sha256": self.digest(source)}],
            "stages": [{"id": stage, "check": stage, "steps": steps if steps is not None else [
                {"action": "schematic.component.modify", "payload": {"primitiveId": "C1"}}
            ], **({"primitiveIds": primitive_ids} if primitive_ids is not None else {})}],
        }
        if requirements is not None:
            plan["requirements"] = requirements
            plan["semanticTarget"] = {"idMap": {"unique:C1": "C1"}, "components": [{"key": "unique:C1", "ref": "C1",
                "uniqueId": "u1", "pins": [{"number": "1", "state": "connected", "net": "VIN"}]}],
                "nets": [{"name": "VIN", "members": [{"componentKey": "unique:C1", "pinNumber": "1"}]}]}
        path = self.write_json(name, plan)
        return path, plan

    def add_item(self, item_id, item, change="C-1"):
        self.apply({"op": "propose", "id": change, "reason": "fixture plan",
                    "delta": [{"action": "ADDED", "id": item_id, "item": item}]})
        self.apply({"op": "approve", "id": change, "confirmation": "fixture approval"})

    def schematic_item(self, plan_path, required=True, depends=None):
        item = {"description": "apply Open stage", "acceptance": "receipt retained",
                "domain": "schematic", "sources": ["DEC-1"], "depends_on": depends or [], "objects": ["SCH-1"],
                "open_plan": {"path": str(plan_path), "sha256": self.digest(plan_path)}}
        if required:
            item["required_checks"] = ["execution_rules", "rule_application"]
        return item

    def geometry_packet(self, plan_path, plan, *, completed=True, component=None, placement=False,
                        source_override=None, target_override=None, state="settled", edit=None, rules_mode="layout"):
        stage = {"id": "geometry", "check": "geometry", "completed": completed}
        action = "schematic.component.place" if placement else "schematic.component.modify"
        result = {} if placement else {"primitiveId": "C1"}
        execution = {"ok": True, "partial": False, "completed": 1, "total": 1,
                     "results": [{"action": action, "ok": True, "result": result}],
                     "rules": {"status": "pass", "scope": ["C1"], "mode": rules_mode, "items": [
                         {"id": "identity", "severity": "error", "status": "pass", "coverage": "checked"},
                         {"id": "text", "severity": "warn", "status": "fail", "coverage": "checked"},
                     ]}}
        report = {"target": self.target, "sha256": self.digest(plan_path), "stage": stage,
                  "execution": self.envelope(execution, self.target), "primitiveIds": ["C1"],
                  "readback": self.envelope({"partial": False, "components": [component or self.part()],
                      "readScope": {"concurrentChange": False, "missingIds": [], "primitiveIds": ["C1"]}}, self.target),
                  "limits": ["No visual or persistence claim."]}
        if edit is not None:
            report["edit"] = edit
        return {"schemaVersion": "easyeda.execution/1", "dryRun": False, "state": state,
                "startedAt": "2026-10-07T23:59:59Z", "completedAt": "2026-10-08T00:00:00Z", "planFile": str(plan_path),
                "sha256": self.digest(plan_path), "revision": plan["revision"],
                "target": target_override or self.target,
                "sources": source_override if source_override is not None else plan["sources"],
                "stage": stage, "receipt": self.envelope(report, self.target)}

    def result(self, item="SCH-OPEN", receipt="open-receipt.json", status="partial", ok=True, **extra):
        op = {"op": "result", "item": item, "revision": self.state()["plan"][item]["revision"],
              "status": status, "note": "actual Open receipt", "open_receipt": receipt}
        op.update(extra)
        return self.apply(op, ok=ok)

    def imported_check(self, record, check_id):
        return next(check for check in record["checks"] if check["id"] == check_id)

    def native_delivery_fixture(self, scope="page", name="delivery"):
        """The same file chain emitted by run, finish and verify_delivery; no EDA calls."""
        from workflow import digest

        requirements = [
            {"id": "SRC", "sourceId": "SRC-OPEN", "when": "before_write", "method": "checker",
             "check": "source_binding", "severity": "error", "componentKeys": [], "netNames": []},
            {"id": "TARGET", "sourceId": "SRC-OPEN", "when": "delivery", "method": "checker",
             "check": "schematic.target.check", "severity": "error", "componentKeys": [], "netNames": []},
            {"id": "DRC", "sourceId": "SRC-OPEN", "when": "delivery", "method": "native",
             "check": "drc", "severity": "error", "componentKeys": [], "netNames": []},
        ]
        plan_path, plan = self.open_plan(requirements=requirements, name=name + "-plan.json")
        directory = self.root / (name + ".execution")
        directory.mkdir()
        plan.update(acceptanceScope=scope, confirmation={"status": "confirmed"},
                    taskKind={"page": "new_module", "module": "incremental_module", "local": "local_edit"}[scope],
                    executionReceipts={"directory": str(directory)})
        plan["stages"].append({"id": "final", "check": "final", "steps": []})
        self.write_json(plan_path.name, plan)

        def native(result, request_id, second):
            return {"structuredContent": {"ok": True, "id": request_id,
                    "createdAt": "2026-10-08T00:00:%02dZ" % second,
                    "context": dict(self.target, documentType="schematic"), "result": result}}

        part = dict(self.part(), componentType="part", netlistAvailable=True, x=1, y=2, rotation=0, mirror=False)
        inventory = {key: [] for key in ("components", "wires", "buses", "arcs", "circles", "rectangles",
                                        "polygons", "texts", "attributes", "objects")}
        inventory["components"] = [copy.deepcopy(part)]
        snapshot = {"components": [part], "wires": [], "wiresAvailable": True, "pagePrimitives": inventory,
                    "pagePrimitiveBounds": {}, "readScope": {"complete": True, "concurrentChange": False, "missingIds": []}}
        target_check = native({"status": "pass", "verified": True, "partial": False}, "target-read", 9)
        drc_check = native({"countsAvailable": True, "passed": True, "partial": False,
                           "summary": {"fatal": 0, "error": 0, "unknown": 0, "warn": 0}}, "drc-read", 9)
        first = self.geometry_packet(plan_path, plan)
        first["binding"] = {
            "semanticTargetSha256": digest({key: value for key, value in plan["semanticTarget"].items() if key != "idMap"}),
            "sourcesSha256": digest(plan["sources"]), "stageSha256": digest(plan["stages"][0]),
            "requirementsSha256": digest([rule for rule in requirements if rule["when"] == "before_write"]),
            "acceptanceSha256": digest({"acceptanceScope": scope, "taskKind": plan["taskKind"]}),
            "receiptDirectory": str(directory), "idMap": copy.deepcopy(plan["semanticTarget"]["idMap"]),
        }
        first["receipt"]["structuredContent"]["result"]["execution"]["structuredContent"].update(
            ok=True, id="write", createdAt="2026-10-08T00:00:00Z", context=self.target)
        first_path = self.write_json(str(directory.relative_to(self.root) / "stage-01.json"), first)
        stage = {"id": "final", "check": "final", "completed": True}
        final_report = {"target": self.target, "sha256": self.digest(plan_path), "stage": stage,
                        "execution": None,
                        "targetCheck": native(copy.deepcopy(target_check["structuredContent"]["result"]), "final-target", 2),
                        "drcCheck": native(copy.deepcopy(drc_check["structuredContent"]["result"]), "final-drc", 2),
                        "finalReadback": native(copy.deepcopy(snapshot), "final-read", 2),
                        "predecessors": [{"file": str(first_path), "sha256": self.digest(first_path)}]}
        final_packet = {"schemaVersion": "easyeda.execution/1", "dryRun": False, "state": "settled",
                        "startedAt": "2026-10-08T00:00:01Z", "completedAt": "2026-10-08T00:00:03Z",
                        "planFile": str(plan_path), "sha256": self.digest(plan_path), "revision": plan["revision"],
                        "target": self.target, "sources": plan["sources"], "stage": stage,
                        "receipt": self.envelope(final_report, self.target)}
        final_packet["binding"] = dict(first["binding"], stageSha256=digest(plan["stages"][-1]),
                                       requirementsSha256=digest(requirements))
        final_path = self.write_json(str(directory.relative_to(self.root) / "stage-02.json"), final_packet)
        image = directory / "review.png"
        image.write_bytes(base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jQ1kAAAAASUVORK5CYII="))
        run = {"structuredContent": {"ok": True, "partial": False, "remainingStages": [],
               "completedStages": [s["id"] for s in plan["stages"]], "planFile": str(plan_path),
               "sha256": self.digest(plan_path), "target": self.target,
               "review": {"imagePath": str(image), "imageSha256": self.digest(image), "saved": False,
                          "visualReview": {"window": "fixture-window", "operation": "visual_review", "source": "ai",
                                           "requestId": "write", "generation": 1, "screenshotRequestId": "image"},
                          "finish": {"window": "fixture-window", "operation": "finish", "requestId": "write"}}}}
        run_path = self.write_json(str(directory.relative_to(self.root) / "run.json"), run)
        checker = {"ok": True, "result": {"status": "synced", "coverage": {"drawingCompared": True}}}
        native_hash = hashlib.sha256(json.dumps(dict(snapshot, **self.target), separators=(",", ":")).encode()).hexdigest()
        diff = {"status": "synced", "checker": checker, "inputSha256": [native_hash, native_hash]}
        delivery = {"schemaVersion": "easyeda.delivery/1", "state": "settled", "planFile": str(plan_path),
                    "sha256": self.digest(plan_path), "target": self.target, "runResultFile": str(run_path),
                    "runResultSha256": self.digest(run_path), "imagePath": str(image), "imageSha256": self.digest(image),
                    "startedAt": "2026-10-08T00:00:04Z", "completedAt": "2026-10-08T00:00:06Z",
                    "receipt": {"ok": True, "partial": False, "saved": True, "acceptanceScope": scope,
                                "planFile": str(plan_path), "target": self.target, "sha256": self.digest(plan_path),
                                "visualReviewReceipt": {"structuredContent": {"ok": True}},
                                "finishReceipt": native({"saved": True}, "save", 5),
                                "preSaveReadback": native(copy.deepcopy(snapshot), "pre-save-read", 4),
                                "preSaveDiff": copy.deepcopy(diff), "persistence": {"status": "unverified"},
                                "wholePageComplete": False, "engineeringAccepted": False}}
        delivery_path = self.write_json(str(directory.relative_to(self.root) / "delivery.json"), delivery)
        verification = {"schemaVersion": "easyeda.delivery-verification/1", "derivedOnly": True, "readOnly": True,
                        "ok": True, "target": self.target, "acceptanceScope": scope,
                        "deliveryFile": str(delivery_path), "deliverySha256": self.digest(delivery_path),
                        "verifiedAt": "2026-10-08T00:00:10Z", "persistence": {"status": "verified",
                            "requestIds": {"save": "save", "close": "close", "open": "open", "readback": "reopen-read"},
                            "generation": 1, "instance": "fixture-daemon"},
                        "designDiff": copy.deepcopy(diff), "targetCheck": target_check, "drcCheck": drc_check,
                        "postReopenRequirements": {"blocking": False, "items": []},
                        "readback": native(copy.deepcopy(snapshot), "reopen-read", 9), "visualAccepted": True,
                        "wholePageComplete": scope == "page", "moduleComplete": scope == "module",
                        "localComplete": scope == "local", "engineeringAccepted": False,
                        "stages": [{"file": str(p), "sha256": self.digest(p)} for p in (first_path, final_path)]}
        verification_path = self.write_json(name + "-verification.json", verification)
        item = self.schematic_item(plan_path, required=False)
        item["required_checks"] = ["open_stage_receipt", "open_requirements", "open_target_check", "visual_review", "persistence"]
        item_id = "SCH-" + name.upper()
        self.add_item(item_id, item, change="C-" + name.upper())
        operation = {"op": "result", "item": item_id, "revision": 1, "status": "verified",
                     "note": "native saved/reopened evidence", "artifact": "fixture document", "saved": True,
                     "checks_passed": True, "open_receipt": str(final_path), "open_persistence": str(verification_path)}
        return operation, {"plan": plan_path, "source": Path(plan["sources"][0]["path"]), "stage": first_path,
                           "final": final_path, "image": image, "run": run_path, "delivery": delivery_path,
                           "verification": verification_path}

    def test_native_delivery_verification_closes_page_module_and_local_workflow(self):
        for scope in ("page", "module", "local"):
            with self.subTest(scope=scope):
                operation, files = self.native_delivery_fixture(scope, name=scope)
                originals = {key: files[key].read_bytes() for key in ("stage", "final", "delivery")}
                self.apply(operation)
                record = self.state()["results"][-1]
                self.assertEqual(record["status"], "verified")
                for check in ("open_stage_receipt", "open_requirements", "open_target_check", "visual_review", "persistence"):
                    self.assertEqual(self.imported_check(record, check)["status"], "pass")
                imported = record["open_persistence_import"]
                self.assertEqual(imported["stageReceipt"], str(files["final"]))
                self.assertEqual(imported["acceptanceScope"], scope)
                self.assertEqual(imported["wholePageComplete"], scope == "page")
                self.assertEqual(imported["moduleComplete"], scope == "module")
                self.assertEqual(imported["localComplete"], scope == "local")
                self.assertFalse(imported["engineeringAccepted"])
                for key, original in originals.items():
                    self.assertEqual(files[key].read_bytes(), original)

    def test_native_delivery_rejects_changed_hash_chain(self):
        for key in ("plan", "source", "stage", "final", "run", "delivery", "image"):
            with self.subTest(file=key):
                operation, files = self.native_delivery_fixture(name="drift-" + key)
                files[key].write_bytes(files[key].read_bytes() + b"\n")
                before = self.state()
                self.apply_error(operation)
                self.assertEqual(self.state()["generation"], before["generation"])
                self.assertEqual(self.state()["results"], before["results"])

    def test_native_delivery_accepts_final_only_run_with_complete_original_stage_chain(self):
        operation, files = self.native_delivery_fixture(name="final-only")
        run = json.loads(files["run"].read_text(encoding="utf-8"))
        run["structuredContent"]["completedStages"] = ["final"]
        files["run"].write_text(json.dumps(run), encoding="utf-8")
        delivery = json.loads(files["delivery"].read_text(encoding="utf-8"))
        delivery["runResultSha256"] = self.digest(files["run"])
        files["delivery"].write_text(json.dumps(delivery), encoding="utf-8")
        verification = json.loads(files["verification"].read_text(encoding="utf-8"))
        verification["deliverySha256"] = self.digest(files["delivery"])
        files["verification"].write_text(json.dumps(verification), encoding="utf-8")
        self.apply(operation)
        record = self.state()["results"][-1]
        self.assertEqual(record["status"], "verified")
        self.assertEqual(self.imported_check(record, "persistence")["status"], "pass")

    def test_native_delivery_rejects_same_target_readback_not_bound_to_design_diff(self):
        operation, files = self.native_delivery_fixture(name="mixed-readback")
        verification = json.loads(files["verification"].read_text(encoding="utf-8"))
        verification["readback"]["structuredContent"]["result"]["components"][0]["x"] += 1
        files["verification"].write_text(json.dumps(verification), encoding="utf-8")
        before = self.state()
        self.apply_error(operation)
        self.assertEqual(self.state()["generation"], before["generation"])
        self.assertEqual(self.state()["results"], before["results"])

    def test_native_delivery_rejects_unknown_save_reopen_drc_and_design_diff(self):
        mutations = {
            "save": lambda delivery, verification: delivery["receipt"].update(saved=None),
            "reopen": lambda delivery, verification: verification["persistence"]["requestIds"].pop("open"),
            "reused-id": lambda delivery, verification: verification["persistence"]["requestIds"].update(open="close"),
            "drc": lambda delivery, verification: verification["drcCheck"]["structuredContent"]["result"]["summary"].update(unknown=1),
            "diff": lambda delivery, verification: verification["designDiff"].update(status="different"),
        }
        for name, mutate in mutations.items():
            with self.subTest(failure=name):
                operation, files = self.native_delivery_fixture(name="invalid-" + name)
                delivery = json.loads(files["delivery"].read_text(encoding="utf-8"))
                verification = json.loads(files["verification"].read_text(encoding="utf-8"))
                mutate(delivery, verification)
                files["delivery"].write_text(json.dumps(delivery), encoding="utf-8")
                verification["deliverySha256"] = self.digest(files["delivery"])
                files["verification"].write_text(json.dumps(verification), encoding="utf-8")
                before = self.state()
                self.apply_error(operation)
                self.assertEqual(self.state()["generation"], before["generation"])
                self.assertEqual(self.state()["results"], before["results"])

    def test_geometry_receipt_binds_plan_source_target_and_generates_unknown_on_missing_geometry(self):
        plan_path, plan = self.open_plan()
        self.add_item("SCH-OPEN", self.schematic_item(plan_path))
        receipt = self.write_json("open-receipt.json", self.geometry_packet(plan_path, plan))
        self.result()
        record = self.state()["results"][-1]
        self.assertEqual(record["status"], "partial")
        self.assertEqual(self.imported_check(record, "open_stage_receipt")["status"], "pass")
        self.assertEqual(self.imported_check(record, "open_local_rules")["status"], "pass")
        self.assertIn(str(receipt.resolve()), [e["path"] for e in record["evidence"]])
        self.assertEqual(record["open_import"]["target"], self.target)
        self.assertFalse(record["open_import"]["wholePageComplete"])
        progress = (self.root / "progress.md").read_text(encoding="utf-8")
        self.assertIn("| 要求 | 绑定 | 检查机制 | 覆盖/未覆盖 | 证据 | 结果 |", progress)

        missing = self.geometry_packet(plan_path, plan, component={"primitiveId": "C1", "pinsAvailable": False})
        self.write_json("missing-geometry.json", missing)
        self.result(receipt="missing-geometry.json")
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_stage_receipt")["status"], "unknown")

        missing_rule_status = self.geometry_packet(plan_path, plan)
        del missing_rule_status["receipt"]["structuredContent"]["result"]["execution"]["structuredContent"]["result"]["rules"]["status"]
        self.write_json("missing-rule-status.json", missing_rule_status)
        self.result(receipt="missing-rule-status.json")
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_local_rules")["status"], "unknown")

        source = Path(plan["sources"][0]["path"])
        source.write_text(json.dumps({"kind": "changed"}), encoding="utf-8")
        checked = self.cli("check")
        self.assertIn("SCH-OPEN", checked["open_plan_issues"])
        self.assertEqual(checked["items"]["SCH-OPEN"]["status"], "needs_review")

    def test_geometry_does_not_trust_completed_or_placement_without_created_id(self):
        plan_path, plan = self.open_plan()
        self.add_item("SCH-OPEN", self.schematic_item(plan_path))
        self.write_json("incomplete.json", self.geometry_packet(plan_path, plan, completed=False))
        self.result(receipt="incomplete.json")
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_stage_receipt")["status"], "unknown")

        self.write_json("placement.json", self.geometry_packet(plan_path, plan, placement=True))
        self.result(receipt="placement.json")
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_stage_receipt")["status"], "unknown")

    def test_final_target_status_never_claims_visual_or_saved(self):
        plan_path, plan = self.open_plan(stage="final", steps=[])
        self.add_item("SCH-OPEN", self.schematic_item(plan_path))
        for expected in ("fail", "unknown", "pass"):
            stage = {"id": "final", "check": "final", "completed": True}
            report = {"target": self.target, "sha256": self.digest(plan_path), "stage": stage,
                      "execution": None, "targetCheck": self.envelope(
                          {"status": expected, "verified": expected == "pass", "partial": False}, self.target)}
            packet = {"schemaVersion": "easyeda.execution/1", "dryRun": False, "state": "settled",
                      "startedAt": "2026-10-07T23:59:59Z", "completedAt": "2026-10-08T00:00:00Z", "planFile": str(plan_path),
                      "sha256": self.digest(plan_path), "revision": 1, "target": self.target,
                      "sources": plan["sources"], "stage": stage, "receipt": self.envelope(report, self.target)}
            receipt_name = "final-" + expected + ".json"
            self.write_json(receipt_name, packet)
            self.result(receipt=receipt_name)
            record = self.state()["results"][-1]
            target_check = next(c for c in record["checks"] if c["id"] == "open_target_check")
            self.assertEqual(target_check["status"], expected)
            self.assertIn("visual_review", target_check["coverage"]["notChecked"])
            self.assertIn("persistence", target_check["coverage"]["notChecked"])
            self.assertNotIn("saved", record)

    def test_pending_unknown_and_bad_bindings_do_not_become_pass(self):
        plan_path, plan = self.open_plan()
        self.add_item("SCH-OPEN", self.schematic_item(plan_path))
        pending = self.geometry_packet(plan_path, plan, state="pending")
        pending.pop("completedAt")
        pending["receipt"] = self.envelope({})
        self.write_json("open-receipt.json", pending)
        self.result()
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_stage_receipt")["status"], "unknown")

        invalid_time = self.geometry_packet(plan_path, plan)
        invalid_time["startedAt"] = "2026-10-08T01:00:00Z"
        invalid_time["completedAt"] = "2026-10-08T00:00:00Z"
        self.write_json("invalid-time.json", invalid_time)
        self.result(receipt="invalid-time.json")
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_stage_receipt")["status"], "unknown")

    def test_local_hard_rule_failure_and_missing_facts_are_not_hidden_by_warnings(self):
        plan_path, plan = self.open_plan()
        self.add_item("SCH-OPEN", self.schematic_item(plan_path))
        failed = self.geometry_packet(plan_path, plan)
        report = failed["receipt"]["structuredContent"]["result"]
        rules = report["execution"]["structuredContent"]["result"]["rules"]
        rules["items"][0]["status"] = "fail"
        self.write_json("hard-fail.json", failed)
        self.result(receipt="hard-fail.json")
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_local_rules")["status"], "fail")

        unknown = self.geometry_packet(plan_path, plan)
        rules = unknown["receipt"]["structuredContent"]["result"]["execution"]["structuredContent"]["result"]["rules"]
        rules["items"] = [{"id": "facts", "severity": "error", "status": "unknown", "coverage": "unavailable"}]
        self.write_json("missing-facts.json", unknown)
        self.result(receipt="missing-facts.json")
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_local_rules")["status"], "unknown")

        for name, mutate in {
            "hash": lambda p: p.update(sha256="0" * 64),
            "target": lambda p: p.update(target={"projectUuid": "wrong", "documentUuid": "wrong"}),
            "source": lambda p: p.update(sources=[]),
        }.items():
            packet = self.geometry_packet(plan_path, plan)
            mutate(packet)
            receipt_name = "bad-" + name + ".json"
            self.write_json(receipt_name, packet)
            with self.subTest(name=name):
                self.result(receipt=receipt_name, ok=False)

    def test_requirements_are_recomputed_from_bound_sources_and_native_local_rules(self):
        requirements = [
            {"id": "SRC", "sourceId": "SRC-OPEN", "when": "before_write", "method": "checker",
             "check": "source_binding", "severity": "error", "componentKeys": [], "netNames": [], "stageId": "geometry"},
            {"id": "ID", "sourceId": "SRC-OPEN", "when": "after_edit", "method": "checker",
             "check": "identity", "severity": "error", "componentKeys": ["unique:C1"], "netNames": [], "stageId": "geometry"},
            {"id": "PAGE", "sourceId": "SRC-OPEN", "when": "after_edit", "method": "checker",
             "check": "identity", "severity": "error", "componentKeys": [], "netNames": []},
            {"id": "TEXT", "sourceId": "SRC-OPEN", "when": "after_edit", "method": "checker",
             "check": "text_overlap", "severity": "warn", "componentKeys": ["unique:C1"], "netNames": [], "stageId": "geometry"},
        ]
        plan_path, plan = self.open_plan(requirements=requirements)
        self.add_item("SCH-OPEN", self.schematic_item(plan_path))
        packet = self.geometry_packet(plan_path, plan)
        report = packet["receipt"]["structuredContent"]["result"]
        rules = report["execution"]["structuredContent"]["result"]["rules"]
        rules["items"] = [
            {"id": "identity", "severity": "error", "status": "pass", "coverage": "checked"},
            {"id": "text_overlap", "severity": "warn", "status": "fail", "coverage": "checked"},
        ]
        report["requirementResults"] = [{"status": "pass", "coverage": "checked"}]
        self.write_json("requirements.json", packet)
        self.result(receipt="requirements.json")
        imported = self.state()["results"][-1]["open_import"]["requirementResults"]
        by_id = {row["requirement"]["id"]: row for row in imported}
        self.assertEqual(by_id["SRC"]["status"], "pass")
        self.assertEqual(by_id["ID"]["status"], "pass")
        self.assertEqual(by_id["TEXT"]["status"], "fail")
        self.assertEqual(by_id["PAGE"]["status"], "unknown")
        self.assertEqual(self.imported_check(self.state()["results"][-1], "open_requirements")["status"], "unknown")

        wrong_identity = self.geometry_packet(plan_path, plan)
        wrong_identity["receipt"]["structuredContent"]["result"]["readback"]["structuredContent"]["result"]["components"][0]["uniqueId"] = "other"
        self.write_json("wrong-identity.json", wrong_identity)
        self.result(receipt="wrong-identity.json")
        identity = next(row for row in self.state()["results"][-1]["open_import"]["requirementResults"]
                        if row["requirement"]["id"] == "ID")
        self.assertEqual(identity["status"], "unknown")

    def test_stage_scope_requires_actual_unambiguous_component_records(self):
        stage_requirement = {"id": "ID", "sourceId": "SRC-OPEN", "when": "after_edit", "method": "checker",
                             "check": "identity", "severity": "error", "componentKeys": [], "netNames": [], "stageId": "geometry"}
        scope_path, scope_plan = self.open_plan(requirements=[stage_requirement])
        self.add_item("SCH-SCOPE", self.schematic_item(scope_path), change="C-SCOPE")
        for name, parts in [("missing", []), ("invalid", [None]), ("duplicate", [
                {"primitiveId": "C1", "designator": "C1", "uniqueId": "u1"},
                {"primitiveId": "C1", "designator": "C1", "uniqueId": "u1"}])]:
            broken = self.geometry_packet(scope_path, scope_plan)
            broken["receipt"]["structuredContent"]["result"]["readback"]["structuredContent"]["result"]["components"] = parts
            filename = "scope-" + name + ".json"
            self.write_json(filename, broken)
            with self.subTest(scope=name):
                self.result(item="SCH-SCOPE", receipt=filename)
                row = self.state()["results"][-1]["open_import"]["requirementResults"][0]
                self.assertEqual(row["status"], "unknown")

    def test_design_pin_nets_need_exact_semantic_targets_and_fresh_identity_readback(self):
        requirement = {"id": "PINS", "sourceId": "SRC-OPEN", "when": "after_edit", "method": "checker",
                       "check": "pin_nets", "severity": "error", "componentKeys": ["unique:C1"], "netNames": [], "stageId": "geometry"}
        plan_path, plan = self.open_plan(requirements=[requirement])
        self.add_item("SCH-OPEN", self.schematic_item(plan_path))
        packet = self.geometry_packet(plan_path, plan, rules_mode="design",
                                      edit={"mode": "design", "primitiveIds": ["C1"], "complete": True,
                                            "expectedPins": {"C1.1": "VIN"}})
        rules = packet["receipt"]["structuredContent"]["result"]["execution"]["structuredContent"]["result"]["rules"]
        rules["items"] = [{"id": "pin_nets", "severity": "error", "status": "pass", "coverage": "checked"}]
        self.write_json("design-pins.json", packet)
        self.result(receipt="design-pins.json")
        row = self.state()["results"][-1]["open_import"]["requirementResults"][0]
        self.assertEqual(row["status"], "pass")

        mismatch = self.geometry_packet(plan_path, plan, rules_mode="design",
                                        edit={"mode": "design", "primitiveIds": ["C1"], "complete": True,
                                              "expectedPins": {"C1.1": "GND"}})
        mismatch["receipt"]["structuredContent"]["result"]["execution"]["structuredContent"]["result"]["rules"]["items"] = [
            {"id": "pin_nets", "severity": "error", "status": "pass", "coverage": "checked"}]
        self.write_json("mismatch-pins.json", mismatch)
        self.result(receipt="mismatch-pins.json")
        self.assertEqual(self.state()["results"][-1]["open_import"]["requirementResults"][0]["status"], "unknown")

        whole_plan = json.loads(json.dumps(plan))
        whole_plan["semanticTarget"]["idMap"]["unique:C2"] = "C2"
        whole_plan["semanticTarget"]["components"].append({"key": "unique:C2", "ref": "C2", "uniqueId": "u2",
                                                               "pins": [{"number": "1", "state": "connected", "net": "VIN"}]})
        whole_path = self.write_json("whole-plan.json", whole_plan)
        self.add_item("SCH-WHOLE-EDIT", self.schematic_item(whole_path), change="C-WHOLE")
        whole = self.geometry_packet(whole_path, whole_plan, rules_mode="design",
                                     edit={"mode": "design", "primitiveIds": ["C1", "C2"], "complete": True,
                                           "expectedPins": {"C1.1": "VIN"}})
        whole["receipt"]["structuredContent"]["result"]["execution"]["structuredContent"]["result"]["rules"]["items"] = [
            {"id": "pin_nets", "severity": "error", "status": "pass", "coverage": "checked"}]
        self.write_json("whole-edit-missing-c2.json", whole)
        self.result(item="SCH-WHOLE-EDIT", receipt="whole-edit-missing-c2.json")
        self.assertEqual(self.state()["results"][-1]["open_import"]["requirementResults"][0]["status"], "unknown")

    def test_requirement_declarations_and_drc_counts_fail_closed(self):
        requirement = {"id": "DRC", "sourceId": "SRC-OPEN", "when": "delivery", "method": "native",
                       "check": "drc", "severity": "warn", "componentKeys": [], "netNames": []}
        plan_path, plan = self.open_plan(stage="final", steps=[], requirements=[requirement])
        self.add_item("SCH-DRC", self.schematic_item(plan_path), change="C-DRC")
        stage = {"id": "final", "check": "final", "completed": True}
        report = {"target": self.target, "sha256": self.digest(plan_path), "stage": stage, "execution": None,
                  "targetCheck": self.envelope({"status": "pass", "verified": True, "partial": False}, self.target),
                  "drcCheck": self.envelope({"countsAvailable": True, "passed": False, "partial": False,
                                               "summary": {"fatal": 0, "error": 0, "unknown": 0, "warn": 2}}, self.target)}
        packet = {"schemaVersion": "easyeda.execution/1", "dryRun": False, "state": "settled",
                  "startedAt": "2026-10-07T23:59:59Z", "completedAt": "2026-10-08T00:00:00Z", "planFile": str(plan_path),
                  "sha256": self.digest(plan_path), "revision": 1, "target": self.target, "sources": plan["sources"],
                  "stage": stage, "receipt": self.envelope(report, self.target)}
        self.write_json("drc-warn.json", packet)
        self.result(item="SCH-DRC", receipt="drc-warn.json")
        result = self.state()["results"][-1]["open_import"]["requirementResults"][0]
        self.assertEqual((result["status"], result["severity"]), ("pass", "error"))

        invalid = [{"id": "SRC", "sourceId": "missing", "when": "before_write", "method": "checker",
                    "check": "source_binding", "severity": "error", "componentKeys": [], "netNames": []}]
        bad_plan, _ = self.open_plan(requirements=invalid, name="invalid-requirements-plan.json")
        error = self.apply_error({"op": "propose", "id": "C-BAD", "reason": "fixture invalid source",
            "delta": [{"action": "ADDED", "id": "SCH-BAD", "item": self.schematic_item(bad_plan)}]})
        self.assertIn("invalid Open requirement declaration or reference", error)

        malformed_plan, malformed = self.open_plan(requirements=[requirement], name="malformed-plan.json")
        malformed["semanticTarget"]["idMap"] = []
        malformed_plan.write_text(json.dumps(malformed), encoding="utf-8")
        error = self.apply_error({"op": "propose", "id": "C-MALFORMED", "reason": "fixture malformed target",
            "delta": [{"action": "ADDED", "id": "SCH-MALFORMED", "item": self.schematic_item(malformed_plan)}]})
        self.assertIn("semantic target references", error)

    def test_final_unknown_error_requirement_blocks_before_legacy_gate(self):
        requirement = {"id": "REVIEW", "sourceId": "SRC-OPEN", "when": "delivery", "method": "manual_unknown",
                       "check": "visual_review", "severity": "error", "componentKeys": [], "netNames": []}
        plan_path, plan = self.open_plan(stage="final", steps=[], requirements=[requirement])
        self.add_item("SCH-FINAL", self.schematic_item(plan_path))
        stage = {"id": "final", "check": "final", "completed": True}
        report = {"target": self.target, "sha256": self.digest(plan_path), "stage": stage, "execution": None,
                  "targetCheck": self.envelope({"status": "pass", "verified": True, "partial": False}, self.target)}
        packet = {"schemaVersion": "easyeda.execution/1", "dryRun": False, "state": "settled",
                  "startedAt": "2026-10-07T23:59:59Z", "completedAt": "2026-10-08T00:00:00Z", "planFile": str(plan_path),
                  "sha256": self.digest(plan_path), "revision": 1, "target": self.target,
                  "sources": plan["sources"], "stage": stage, "receipt": self.envelope(report, self.target)}
        self.write_json("final-requirement.json", packet)
        error = self.apply_error({"op": "result", "item": "SCH-FINAL", "revision": 1, "status": "verified",
                                  "note": "final", "artifact": "fixture", "checks_passed": True, "saved": True,
                                  "open_receipt": "final-requirement.json"})
        self.assertIn("Open error requirements", error)

    def test_open_plan_changes_invalidate_dependents_but_not_unrelated_item(self):
        plan_path, _ = self.open_plan()
        open_item = self.schematic_item(plan_path)
        child = {"description": "follow Open item", "acceptance": "review receipt", "domain": "other",
                 "sources": ["DEC-1"], "depends_on": ["SCH-OPEN"], "objects": ["SCH-CHILD"]}
        other = {"description": "unrelated", "acceptance": "file", "domain": "other",
                 "sources": ["DEC-1"], "depends_on": [], "objects": ["OTHER"]}
        self.apply({"op": "propose", "id": "C-1", "reason": "fixture plan", "delta": [
            {"action": "ADDED", "id": "SCH-OPEN", "item": open_item},
            {"action": "ADDED", "id": "SCH-CHILD", "item": child},
            {"action": "ADDED", "id": "OTHER", "item": other},
        ]})
        self.apply({"op": "approve", "id": "C-1", "confirmation": "fixture approval"})
        artifact = self.root / "other.txt"
        artifact.write_text("unrelated", encoding="utf-8")
        self.apply({"op": "result", "item": "OTHER", "revision": 1, "status": "verified",
                    "note": "file readback", "artifact": str(artifact), "checks_passed": True,
                    "saved": True, "evidence": [str(artifact)]})
        plan_path.write_text(json.dumps({"changed": True}), encoding="utf-8")
        checked = self.cli("check")
        self.assertIn("SCH-OPEN", checked["open_plan_issues"])
        self.assertEqual(checked["items"]["SCH-OPEN"]["status"], "needs_review")
        self.assertEqual(checked["items"]["SCH-CHILD"]["status"], "needs_review")
        self.assertEqual(checked["items"]["OTHER"]["status"], "verified")

    def test_rejects_manual_open_checks_pcb_binding_and_verified_without_legacy_rule_checks(self):
        plan_path, plan = self.open_plan()
        self.add_item("SCH-OPEN", self.schematic_item(plan_path))
        self.write_json("open-receipt.json", self.geometry_packet(plan_path, plan))
        for check_id in ("open_stage_receipt", "open_target_check", "open_local_rules"):
            with self.subTest(manual=check_id):
                manual = {"op": "result", "item": "SCH-OPEN", "revision": 1, "status": "partial",
                          "note": "manual", "checks": [{"id": check_id, "status": "pass",
                          "note": "forged", "evidence": ["open-receipt.json"]}]}
                self.apply(manual, ok=False)

        no_receipt = {"op": "result", "item": "SCH-OPEN", "revision": 1, "status": "verified",
                      "note": "forged complete", "artifact": "fixture", "checks_passed": True, "saved": True}
        self.assertIn("actual final receipt", self.apply_error(no_receipt))

        forged_legacy = dict(no_receipt, open_receipt="open-receipt.json", checks=[
            {"id": "execution_rules", "status": "pass", "note": "forged", "evidence": ["open-receipt.json"]},
            {"id": "rule_application", "status": "pass", "note": "forged", "evidence": ["open-receipt.json"]},
        ])
        self.assertIn("Geometry receipts cannot verify", self.apply_error(forged_legacy))

        pcb = {"description": "invalid pcb Open plan", "acceptance": "never", "domain": "pcb",
               "sources": ["DEC-1"], "depends_on": [], "objects": ["PCB-1"],
               "required_checks": ["execution_rules", "rule_application"],
               "open_plan": {"path": str(plan_path), "sha256": self.digest(plan_path)},
               "pcb_constraint_ir": {"schemaVersion": "pcb-constraint/v0", "constraints": []}}
        self.apply({"op": "propose", "id": "C-PCB", "reason": "must reject", "delta": [
            {"action": "ADDED", "id": "PCB-OPEN", "item": pcb}
        ]}, ok=False)

    def test_final_open_pass_still_requires_legacy_rule_evidence(self):
        plan_path, plan = self.open_plan(stage="final", steps=[])
        self.add_item("SCH-FINAL", self.schematic_item(plan_path))
        stage = {"id": "final", "check": "final", "completed": True}
        report = {"target": self.target, "sha256": self.digest(plan_path), "stage": stage,
                  "execution": None, "targetCheck": self.envelope(
                      {"status": "pass", "verified": True, "partial": False}, self.target)}
        packet = {"schemaVersion": "easyeda.execution/1", "dryRun": False, "state": "settled",
                  "startedAt": "2026-10-07T23:59:59Z", "completedAt": "2026-10-08T00:00:00Z",
                  "planFile": str(plan_path), "sha256": self.digest(plan_path), "revision": 1,
                  "target": self.target, "sources": plan["sources"], "stage": stage,
                  "receipt": self.envelope(report, self.target)}
        self.write_json("final-pass.json", packet)
        error = self.apply_error({"op": "result", "item": "SCH-FINAL", "revision": 1,
                                  "status": "verified", "note": "Open final pass", "artifact": "fixture",
                                  "checks_passed": True, "saved": True, "open_receipt": "final-pass.json"})
        self.assertIn("execution_rules", error)


if __name__ == "__main__":
    unittest.main()
