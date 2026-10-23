#!/usr/bin/env python3
"""One runnable CLI scenario: approval, impact, evidence, recovery and scoped Git history."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import hashlib

CLI = Path(__file__).with_name("workflow.py")


def main():
    with tempfile.TemporaryDirectory(prefix="workflow-check-") as directory:
        root = Path(directory)

        def cli(*args, ok=True):
            cp = subprocess.run([sys.executable, str(CLI), "--root", str(root), *args],
                                capture_output=True, text=True, encoding="utf-8")
            assert (cp.returncode == 0) == ok, (args, cp.stdout, cp.stderr)
            return json.loads(cp.stdout) if cp.stdout.strip().startswith("{") else cp.stdout

        def state():
            return json.loads((root / ".workflow/state.json").read_text(encoding="utf-8"))

        def apply(op, ok=True):
            (root / "operation.json").write_text(json.dumps(op), encoding="utf-8")
            return cli("apply", str(root / "operation.json"),
                       "--expect-generation", str(state()["generation"]), ok=ok)

        def plan(text, deps=None, objects=None):
            return dict(description=text, domain="other", acceptance="saved file matches required text",
                        sources=["DEC-1"], depends_on=deps or [], objects=objects or [],
                        jev={"skip_reason": "Fixture writes exact approved text; no semantic choice"})

        def verified(key):
            p = root / (key + ".txt")
            p.write_text(state()["plan"][key]["description"], encoding="utf-8")
            apply(dict(op="result", item=key, revision=state()["plan"][key]["revision"],
                       status="verified", note="read back saved file", artifact=str(p),
                       checks_passed=True, saved=True, evidence=[str(p)]))

        cli("init", "--goal", "file-level closed loop")
        # A hand-written newer heading must not silently supersede or be erased.
        progress = root / "progress.md"
        progress.write_text(progress.read_text(encoding="utf-8") + "\nManual newer handoff\n", encoding="utf-8")
        before_state = (root / ".workflow/state.json").read_bytes()
        checked = cli("check")
        assert checked["recovery_blocked"] and "progress.md" in checked["view_issues"]
        assert cli("next", ok=False)["ready"] == []
        cli("render", ok=False)
        apply({"op": "context", "value": {"next": "must not overwrite"}}, ok=False)
        assert (root / ".workflow/state.json").read_bytes() == before_state
        assert "Manual newer handoff" in progress.read_text(encoding="utf-8")
        cli("render", "--reconcile-views", "fixture text has no real decision; safe to discard")
        audit = state()["changes"][-1]
        assert audit["reason"] == "fixture text has no real decision; safe to discard"
        assert audit["at"] and audit["views"]["progress.md"]["observed_hash"]
        assert audit["views"]["progress.md"]["observed_hash"] != audit["views"]["progress.md"]["pre_reconcile_expected_hash"]
        assert not cli("check")["view_issues"]
        progress.unlink()
        assert cli("next", ok=False)["view_issues"]["progress.md"] == "missing"
        cli("render", "--reconcile-views", "fixture deleted view; authoritative state intact")
        # Direct state edits with unchanged generation must also invalidate views.
        changed = state()
        changed["context"]["next"] = "direct edit fixture"
        (root / ".workflow/state.json").write_text(json.dumps(changed), encoding="utf-8")
        assert cli("check")["recovery_blocked"]
        cli("render", "--reconcile-views", "fixture direct edit verified against intended next")
        assert not cli("check")["recovery_blocked"]
        valid_state = (root / ".workflow/state.json").read_bytes()
        invalid = state()
        invalid["results"].append({"item": "legacy", "status": "partial"})
        (root / ".workflow/state.json").write_text(json.dumps(invalid), encoding="utf-8")
        cli("check", ok=False)
        (root / ".workflow/state.json").write_bytes(valid_state)
        apply({"op": "source", "source": {"id": "DEC-1", "kind": "decision",
                                         "text": "write the approved text", "locator": "test fixture decision"}})
        items = {"P-1": plan("alpha", objects=["COMMON"]),
                 "P-2": plan("second", ["P-1"]),
                 "P-3": plan("shared", objects=["COMMON"]),
                 "P-4": plan("unrelated")}
        apply({"op": "propose", "id": "C-1", "reason": "initial",
               "delta": [{"action": "ADDED", "id": k, "item": p} for k, p in items.items()]})
        assert not state()["plan"], "proposal must not activate"
        apply({"op": "approve", "id": "C-1", "confirmation": "fixture approval"})
        assert set(cli("next")["ready"]) == {"P-1", "P-3", "P-4"}
        verified("P-1")
        verified("P-2")
        verified("P-3")
        verified("P-4")
        apply({"op": "propose", "id": "C-2", "reason": "change a shared constraint",
               "delta": [{"action": "MODIFIED", "id": "P-1", "item": plan("beta", objects=["COMMON"])}]})
        assert cli("check")["items"]["P-1"]["status"] == "verified"
        apply({"op": "approve", "id": "C-2", "confirmation": "fixture approves beta"})
        checked = cli("check")
        assert set(checked["reviews"]) == {"P-1", "P-2", "P-3"}
        assert checked["items"]["P-4"]["status"] == "verified"
        assert state()["results"][0]["status"] == "verified", "history was rewritten"
        apply({"op": "result", "item": "P-1", "revision": 1, "status": "partial", "note": "old"}, ok=False)
        for key in ("P-1", "P-2"):
            apply({"op": "review", "item": key, "outcome": "reverify", "reason": "new requirement"})
            verified(key)
        apply({"op": "review", "item": "P-3", "outcome": "unchanged",
               "reason": "shared reference reviewed; this output remains applicable"})
        assert all(x["status"] == "verified" for x in cli("check")["items"].values())
        # Tampering with an evidence file invalidates its current verification and downstream.
        (root / "P-1.txt").write_text("outside edit", encoding="utf-8")
        assert cli("next", ok=False)["recovery_blocked"]
        cli("render", "--reconcile-views", "fixture external evidence changed; retain needs_review")
        assert cli("check")["items"]["P-2"]["status"] == "needs_review"
        apply({"op": "review", "item": "P-1", "outcome": "reverify", "reason": "outside edit"})
        verified("P-1")
        assert cli("check")["items"]["P-2"]["status"] != "verified"
        # Cycles and stale concurrent operations fail without changing the state.
        generation = state()["generation"]
        apply({"op": "propose", "id": "C-cycle", "reason": "invalid",
               "delta": [{"action": "MODIFIED", "id": "P-1",
                          "item": plan("cycle", ["P-2"])}]}, ok=False)
        assert state()["generation"] == generation
        cli("apply", str(root / "operation.json"), "--expect-generation", "-1", ok=False)
        apply({"op": "context", "value": {"stage": "execution", "next": "read back P-2",
                                        "authorization": "fixture only", "blockers": []}})
        assert cli("next")["context"]["next"] == "read back P-2"
        # Hardware gates cannot be replaced with one optimistic checks_passed flag.
        gate_plan = plan("review a drawing")
        gate_plan["required_checks"] = ["connectivity", "appearance"]
        apply({"op": "propose", "id": "C-gates", "reason": "require explicit coverage",
               "delta": [{"action": "ADDED", "id": "P-gates", "item": gate_plan}]})
        apply({"op": "approve", "id": "C-gates", "confirmation": "fixture approval"})
        report = root / "drawing-check.json"
        report.write_text(json.dumps({"passed": False}), encoding="utf-8")
        observation = root / "appearance.txt"
        observation.write_text("source and exported region compared", encoding="utf-8")
        gate_result = dict(op="result", item="P-gates", revision=1, status="verified",
                           artifact="fixture drawing", note="source/render comparison",
                           checks_passed=True, saved=True, evidence=[str(observation)])
        apply(gate_result, ok=False)
        gate_result["checks"] = [dict(id="connectivity", status="pass", note="actual report",
                                      report=str(report), evidence=[str(report)]),
                                 dict(id="appearance", status="unknown", note="not reviewed",
                                      evidence=[str(observation)])]
        apply(gate_result, ok=False)  # a failed report cannot be marked pass
        report.write_text(json.dumps({"passed": True}), encoding="utf-8")
        apply(gate_result, ok=False)  # unknown appearance blocks verification
        gate_result["checks"][1]["status"] = "pass"
        gate_result["checks"][1]["note"] = "fixture source and rendered crop match"
        apply(gate_result)
        assert cli("check")["items"]["P-gates"]["status"] == "verified"
        # A changed report input cannot be hidden behind a cached passed report.
        report.write_text(json.dumps({"passed": True, "inputs": {str(observation): "stale"}}), encoding="utf-8")
        cli("render", "--reconcile-views", "fixture changed report invalidates old verification")
        apply({"op": "review", "item": "P-gates", "outcome": "reverify", "reason": "report changed"})
        apply(gate_result, ok=False)
        report.write_text(json.dumps({"passed": True}), encoding="utf-8")
        apply(gate_result)
        # Rule confirmation is mandatory at start; applied-rule evidence at completion.
        rules_plan = plan("draw under confirmed rules")
        rules_plan["domain"] = "schematic"
        missing_rules = dict(rules_plan)
        apply({"op": "propose", "id": "C-missing-rules", "reason": "fixture missing rules",
               "delta": [{"action": "ADDED", "id": "P-missing-rules", "item": missing_rules}]}, ok=False)
        rules_plan["required_checks"] = ["execution_rules", "rule_application"]
        apply({"op": "propose", "id": "C-rules", "reason": "rule gates",
               "delta": [{"action": "ADDED", "id": "P-rules", "item": rules_plan}]})
        apply({"op": "approve", "id": "C-rules", "confirmation": "fixture rules approval"})
        start = dict(op="result", item="P-rules", revision=1, status="in_progress", note="start fixture")
        apply(start, ok=False)
        rule_gate = root / "rules-gate.json"
        bound = {"version": "fixture-r1", "sha256": "fixture-only-rule-fingerprint"}
        rule_gate.write_text(json.dumps({"passed": True,
            "inputs": {str(observation): hashlib.sha256(observation.read_bytes()).hexdigest()},
            "execution_rules": dict(bound, confirmed=True, effective_ids=["G1", "G2"])}), encoding="utf-8")
        start["checks"] = [dict(id="execution_rules", status="pass", note="fixture confirmed rules",
                                report=str(rule_gate), evidence=[str(rule_gate)])]
        valid_gate = json.loads(rule_gate.read_text(encoding="utf-8"))
        for invalid_ids in ("G1", ["G1", "G1"]):
            invalid_gate = dict(valid_gate, execution_rules=dict(valid_gate["execution_rules"], effective_ids=invalid_ids))
            rule_gate.write_text(json.dumps(invalid_gate), encoding="utf-8")
            apply(start, ok=False)
        rule_gate.write_text(json.dumps(valid_gate), encoding="utf-8")
        apply(start)
        # A fresh CLI process exposes recovery; failed resume leaves the authoritative state intact.
        assert cli("next")["recovery"]["P-rules"]["readback_required"]
        unchanged = (root / ".workflow/state.json").read_bytes()
        apply(start, ok=False)
        assert (root / ".workflow/state.json").read_bytes() == unchanged
        apply(dict(start, status="partial", note="fixture timeout: first action done, second absent"))
        readback = root / "recovery-readback.txt"
        readback.write_text("fixture target confirmed; first action exists; second remains", encoding="utf-8")
        recovery = dict(id="recovery_readback", status="pass", note="fixture readback and residual actions",
                        evidence=[str(readback)])
        for failed in (dict(recovery, status="unknown"), dict(recovery, evidence=["missing-readback.txt"])):
            apply(dict(start, checks=start["checks"] + [failed]), ok=False)
        start = dict(start, checks=start["checks"] + [recovery])
        apply(start)
        final = root / "applied-rules.json"
        rows = [dict(id="G1", status="pass", note="fixture actual observation", evidence=[str(observation)])]
        final.write_text(json.dumps({"passed": True, "rule_binding": bound, "checks": rows}), encoding="utf-8")
        done = dict(start, status="verified", checks_passed=True, saved=True, artifact="fixture",
                    checks=start["checks"] + [dict(id="rule_application", status="pass", note="fixture final check",
                                                   report=str(final), evidence=[str(final)])])
        apply(done, ok=False)  # Missing G2 cannot hide behind passed=true.
        rows.append(dict(rows[0], id="G2", status="unknown"))
        final.write_text(json.dumps({"passed": True, "rule_binding": bound, "checks": rows}), encoding="utf-8")
        apply(done, ok=False)
        rows[-1]["status"] = "pass"
        final.write_text(json.dumps({"passed": True, "rule_binding": dict(bound, version="old"), "checks": rows}), encoding="utf-8")
        apply(done, ok=False)
        final.write_text(json.dumps({"passed": True, "rule_binding": bound, "checks": rows}), encoding="utf-8")
        apply(done)
        # Report input changes invalidate the execution gate before another start.
        rule_gate_data = json.loads(rule_gate.read_text(encoding="utf-8"))
        rule_gate_data["inputs"][str(observation)] = "stale"
        rule_gate.write_text(json.dumps(rule_gate_data), encoding="utf-8")
        cli("render", "--reconcile-views", "fixture stale rule input")
        apply({"op": "review", "item": "P-rules", "outcome": "reverify", "reason": "rule input changed"})
        apply(start, ok=False)
        # Reclassification/revision must not erase an older unfinished operation's readback requirement.
        revised = dict(rules_plan, description="fixture revised residual plan")
        apply({"op": "propose", "id": "C-resume-revision", "reason": "fixture plan revision",
               "delta": [{"action": "MODIFIED", "id": "P-rules", "item": revised}]})
        apply({"op": "approve", "id": "C-resume-revision", "confirmation": "fixture revised approval"})
        # The last record here is verified; add a separate unfinished item through the same CLI.
        apply({"op": "propose", "id": "C-interrupted", "reason": "fixture interrupted hardware",
               "delta": [{"action": "ADDED", "id": "P-interrupted", "item": rules_plan}]})
        apply({"op": "approve", "id": "C-interrupted", "confirmation": "fixture interrupted approval"})
        apply(dict(op="result", item="P-interrupted", revision=1, status="partial", note="fixture first batch uncertain"))
        apply({"op": "propose", "id": "C-interrupted-v2", "reason": "fixture residual revision",
               "delta": [{"action": "MODIFIED", "id": "P-interrupted", "item": revised}]})
        apply({"op": "approve", "id": "C-interrupted-v2", "confirmation": "fixture residual approval"})
        apply({"op": "review", "item": "P-interrupted", "outcome": "reverify", "reason": "fixture review"})
        resumed = cli("next")
        assert resumed["recovery"]["P-interrupted"]["record_revision"] == 1
        assert resumed["recovery"]["P-interrupted"]["revision"] == 2
        assert "P-interrupted" not in resumed["ready"]
        apply(dict(op="result", item="P-interrupted", revision=2, status="in_progress",
                   note="fixture resume without readback"), ok=False)
        # Hooks route structured state without invoking a second PWF plan.
        dispatch = CLI.parents[3] / ".codex/hooks/plugin_dispatch.py"
        hook = subprocess.run([sys.executable, "-B", str(dispatch)],
                              input=json.dumps({"cwd": str(root), "hook_event_name": "SessionStart"}),
                              capture_output=True, text=True, encoding="utf-8",
                              env={**os.environ, "PWF_PLAN_ROOT": str(root)})
        assert hook.returncode == 0 and "Structured evidence workflow" in hook.stdout
        # A scoped commit does not include another staged file.
        def git(*args):
            return subprocess.run(["git", "-C", str(root), *args], check=True,
                                  capture_output=True, text=True).stdout
        git("init")
        git("config", "user.name", "Workflow test")
        git("config", "user.email", "test@example.invalid")
        (root / "unrelated.txt").write_text("not part of plan history")
        git("add", "unrelated.txt")
        cli("checkpoint", "--message", "approved plan")
        committed = git("show", "--pretty=", "--name-only", "HEAD").strip().splitlines()
        assert committed == [".workflow/plan-history.json"], committed
        assert "unrelated.txt" in git("diff", "--cached", "--name-only")
        history = json.loads((root / ".workflow/plan-history.json").read_text(encoding="utf-8"))
        assert "results" not in history and "sources" not in history
        print("PASS: real file writes/readback, proposal gate, shared/dependent impact, revision evidence, "
              "cycle/stale rejection, fresh-process recovery, hook routing, plan-only Git commit")


if __name__ == "__main__":
    main()
