#!/usr/bin/env python3
"""Bounded offline runtime scenarios and actual file-read measurements; no EDA."""
import argparse
from collections import Counter
import contextlib
import copy
import importlib.util
import io
import json
from pathlib import Path
import sys
import tempfile
from time import perf_counter
from unittest.mock import patch


def load(path, name):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


HERE = Path(__file__).resolve().parent
PACKAGE = HERE.parents[2]
w = load(HERE / "workflow.py", "workflow_runtime")


def fixture(root):
    original = root / "original.txt"
    original.write_text("Independent offline source; no real design or user approval.", encoding="utf-8")
    w.write(root / "index.json", {"items": {"A": {
        "text": "shared source", "label": "A", "locations": ["fixture page 1"]}}})
    w.write(root / "manifest.json", {"parse_status": "success", "source_path": "original.txt",
        "source_hash": w.file_hash(original), "index_path": "index.json", "document_version": "fixture-1"})
    source = w.source_value(root, {"id": "S", "kind": "document", "manifest": "manifest.json", "anchors": ["A"]})
    artifact = root / "observed.txt"
    artifact.write_text("fixture readback: first action present; second action absent", encoding="utf-8")
    state = {"schema": 1, "generation": 1, "goal": "offline runtime fixture", "sources": {"S": source},
        "plan": {}, "results": [], "pending": {}, "changes": [], "retired": [], "reviews": {}, "context": {}}
    for key in ("P1", "P2", "P3"):
        state["plan"][key] = dict(description="fixture " + key, acceptance="fixture file observation",
            domain="other", sources=["S"], depends_on=[], objects=[], revision=1, approval="fixture only",
            jev={"skip_reason": "Exact file contents and IDs; no semantic candidates"})
    for key in ("P1", "P2"):
        state["results"].append(dict(item=key, revision=1, basis=w.basis(state, key), status="verified",
            note="offline fixture readback", evidence=[{"path": str(artifact), "hash": w.file_hash(artifact)}]))
    state["plan"]["P3"].update(domain="schematic", required_checks=["execution_rules", "rule_application"])
    return state


def measure(root, state, command):
    w.write(root / ".workflow/state.json", state)
    w.render(root, state)
    opened = Counter()
    native_open = Path.open

    def counted(path, mode="r", *args, **kwargs):
        if "r" in mode:
            opened[str(path.resolve())] += 1
        return native_open(path, mode, *args, **kwargs)

    output = io.StringIO()
    started = perf_counter()
    with patch.object(Path, "open", counted), patch.object(sys, "argv", [str(HERE / "workflow.py"),
            "--root", str(root), command]), contextlib.redirect_stdout(output):
        w.main()
    elapsed = (perf_counter() - started) * 1000
    report = json.loads(output.getvalue())
    relative = {str(Path(p).relative_to(root)): n for p, n in opened.items()}
    return report, {"command": command, "program_calls": 1, "file_reads": sum(opened.values()),
        "unique_files": len(opened), "repeated_reads": sum(n - 1 for n in opened.values()),
        "bytes_read": sum(Path(p).stat().st_size * n for p, n in opened.items()),
        "ms": round(elapsed, 3), "reads_by_file": relative, "eda_calls": 0, "jev_calls": 0}


def measure_rules():
    tests = load(PACKAGE / "skills/easyeda-agent/scripts/test_schematic_preflight.py", "rule_fixtures")
    sample = tests.PreflightTests()
    sample.setUp()
    opened = Counter()
    native_open = Path.open

    def counted(path, mode="r", *args, **kwargs):
        if "r" in mode:
            opened[str(path.resolve())] += 1
        return native_open(path, mode, *args, **kwargs)

    try:
        started = perf_counter()
        with patch.object(Path, "open", counted):
            report = sample.run_gate()
        elapsed = (perf_counter() - started) * 1000
        assert report["passed"] and len(report["execution_rules"]["effective_ids"]) == 16
        result = {"command": "schematic_preflight.audit", "program_calls": 1,
            "file_reads": sum(opened.values()), "unique_files": len(opened),
            "repeated_reads": sum(n - 1 for n in opened.values()),
            "bytes_read": sum(Path(p).stat().st_size * n for p, n in opened.items()), "ms": round(elapsed, 3),
            "eda_calls": 0, "jev_calls": 0}
        return result
    finally:
        sample.doCleanups()


def run(label, verify):
    metrics = {"scope": "offline program/file fixtures; excludes model latency and EDA runtime",
        "entry_bytes": {str(p.relative_to(PACKAGE)): p.stat().st_size for p in (
            PACKAGE / "skills/evidence-to-delivery/SKILL.md", PACKAGE / "skills/easyeda-agent/SKILL.md")},
        "scenarios": {"rule_preflight": measure_rules()}}
    with tempfile.TemporaryDirectory(prefix="runtime-fixture-") as tmp:
        root = Path(tmp).resolve()
        state = fixture(root)
        result, metrics["scenarios"]["shared_evidence"] = measure(root, state, "check")
        assert not result["source_issues"] and not result["view_issues"]
        state["results"].append(dict(item="P3", revision=1, basis=w.basis(state, "P3"), status="partial",
            note="fixture timeout; first action observed, second action not executed", evidence=[]))
        result, metrics["scenarios"]["partial_recovery"] = measure(root, state, "next")
        assert "P3" not in result["ready"]
        if verify:
            assert result["recovery"]["P3"]["readback_required"] is True
            attempt = dict(op="result", item="P3", revision=1, status="in_progress",
                note="fixture retry without readback", checks=[])
            # The existing result entry must reject before any Jev call or state mutation.
            try:
                w.apply(root, copy.deepcopy(state), attempt)
            except ValueError as exc:
                assert "recovery_readback" in str(exc), str(exc)
            else:
                raise AssertionError("unfinished hardware operation resumed without readback")
            assert metrics["scenarios"]["shared_evidence"]["reads_by_file"]["observed.txt"] == 1
            assert metrics["scenarios"]["shared_evidence"]["reads_by_file"]["original.txt"] == 1
            # No cache survives a new check: modified evidence becomes review, not pass.
            (root / "observed.txt").write_text("changed fixture evidence", encoding="utf-8")
            changed = w.status(root, state)
            assert all(changed["items"][key]["status"] == "needs_review" for key in ("P1", "P2"))
    return {label: metrics}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--label", default="after")
    parser.add_argument("--measure-only", action="store_true")
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    result = run(args.label, not args.measure_only)
    if args.out:
        saved = w.read(args.out) if args.out.exists() else {}
        saved.update(result)
        w.write(args.out, saved)
    print(json.dumps(result, ensure_ascii=False, indent=2))
