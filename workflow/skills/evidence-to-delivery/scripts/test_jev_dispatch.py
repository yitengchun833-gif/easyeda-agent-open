"""Automatic dispatch regression: no network, no EDA; the provider boundary is mocked."""
import copy
from pathlib import Path
import tempfile
from unittest.mock import patch
import workflow as w

REQUEST = {"goal": "Choose next evidence", "stage": "research", "candidates": [
    {"id": "a", "label": "Datasheet", "evidence": "Exact manufacturer model"},
    {"id": "b", "label": "Catalog", "evidence": "Family only"}]}


def main():
    state = {"schema": 1, "generation": 0, "goal": "dispatch", "sources": {}, "plan": {},
             "pending": {}, "changes": [], "retired": [], "reviews": {}, "results": [], "context": {}}
    with tempfile.TemporaryDirectory() as d:
        root = Path(d)
        w.apply(root, state, {"op": "source", "source": {"id": "S", "kind": "decision",
                "text": "Choose evidence", "locator": "Test fixture"}})
        item = {"description": "Select evidence", "domain": "analysis", "acceptance": "Chosen evidence reviewed",
                "sources": ["S"], "depends_on": [], "objects": []}
        def propose(value, id):
            return w.apply(root, state, {"op": "propose", "id": id, "reason": "fixture",
                "delta": [{"action": "ADDED", "id": "P", "item": value}]})
        propose(item, "deterministic")
        with patch.object(w, "call_jev") as call:
            assert w.decisions_for({"plan":{"plain":item}}, "plain") == {}
            call.assert_not_called()
        item["jev"] = {"requests": {"choice": REQUEST}}
        propose(item, "C")
        w.apply(root, state, {"op": "approve", "id": "C", "confirmation": "fixture approval"})
        success = {"status": "recommended", "attempted": True, "selected": "a", "model": "mock", "fallback": None}
        op = {"op": "result", "item": "P", "revision": 1, "status": "in_progress", "note": "Start decision step"}
        (root / "proof.txt").write_text("Fixture independently checked evidence")
        done = dict(op, status="verified", artifact="fixture", saved=True, checks_passed=True, evidence=["proof.txt"])
        with patch.object(w, "call_jev", return_value=success) as call:
            w.apply(root, state, op)
            assert call.call_count == 1 and call.call_args.args[0] == REQUEST
            assert state["results"][-1]["jev"]["decisions"]["choice"]["report"]["attempted"]
            w.apply(root, state, done)
            assert state["results"][-1]["status"] == "blocked", "missing adoption must block"
            w.apply(root, state, dict(done, decision_selections={"choice": "a"}))
            assert state["results"][-1]["status"] == "verified" and call.call_count == 1
            # A fresh basis cannot reuse the old decision, even if a caller jumps directly to verified.
            state["plan"]["P"]["revision"] = 2
            w.apply(root, state, dict(done, revision=2, decision_selections={"choice": "a"}))
            assert call.call_count == 2
        state["plan"]["P"]["revision"] = 3
        failure = {"status": "needs_input", "attempted": True, "fallback": "jev_timeout"}
        with patch.object(w, "call_jev", return_value=failure) as call:
            w.apply(root, state, dict(op, revision=3))
            assert state["results"][-1]["status"] == "blocked"
            w.apply(root, state, dict(done, revision=3, decision_selections={"choice": "a"}))
            assert state["results"][-1]["status"] == "blocked" and call.call_count == 1
            w.apply(root, state, dict(op, revision=3, retry_decisions=True))
            assert call.call_count == 2
        with patch.object(w, "call_jev", return_value=success) as call:
            state["plan"]["P"]["revision"] = 4
            state["plan"]["P"]["jev"] = {"skip_reason": "Exact integer sum; no semantic choice"}
            w.apply(root, state, dict(op, revision=4))
            assert call.call_count == 0
            try:
                w.apply(root, state, dict(op, revision=4, jev={"fake": True}))
                raise AssertionError("caller-provided receipt accepted")
            except ValueError:
                pass
        print("PASS: deterministic omission without calls, automatic dispatch, receipt, adoption gate, cache, revision invalidation, failure gate, explicit retry, deterministic skip")


if __name__ == "__main__":
    main()
