#!/usr/bin/env python3
"""Offline module retrieval and draft reuse; uses the local module-card conditions."""
import argparse
import hashlib
import json
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
import module_conditions as rules


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def local_file(root, relative):
    if not isinstance(relative, str) or not relative.strip():
        raise ValueError("non-empty relative path required")
    path = (root / relative).resolve()
    if Path(relative).is_absolute() or not path.is_relative_to(root.resolve()):
        raise ValueError("reference escapes module library")
    if not path.is_file():
        raise ValueError(f"missing reference: {relative}")
    return path


def evaluate(request, root=ROOT):
    root = Path(root)
    rules.require_object(request, "request")
    query = rules.require_nonempty_string(request.get("query"), "query").strip().casefold()
    profile = rules.validate_profile(request.get("profile"))
    # Fresh disk reads are intentional: the next task must see reviewed revisions.
    catalog_path = root / "library/modules/catalog.json"
    catalog = rules.read_json(catalog_path)
    candidates = []
    for module in catalog["modules"]:
        names = [module["id"], module["title"], *module.get("aliases", [])]
        if any(query in name.casefold() for name in names):
            candidates.append(module)
    exact = [m for m in candidates if query == m["id"].casefold()]
    candidates = exact or candidates
    result = {"target_id": profile["target_id"], "query": request["query"],
              "candidates": [m["id"] for m in candidates], "jev_calls": 0,
              "catalog_sha256": digest(catalog_path), "draft": None}
    if len(candidates) != 1:
        return {**result, "status": "needs_input", "label": "需要补信息",
                "reason": "未找到词条" if not candidates else "多个候选，请明确模块身份"}
    module = candidates[0]
    if not module.get("knowledge"):
        return {**result, "status": "needs_input", "label": "需要补信息",
                "reason": "该模块尚无适用条件词条"}
    card_path = local_file(root / "library/modules", module["knowledge"])
    card = rules.read_json(card_path)
    if card.get("schema_version") != 1 or card.get("module_id") != module["id"]:
        raise ValueError("knowledge identity/version mismatch")
    rules.require_nonempty_string(card.get("revision"), "knowledge.revision")
    for key in ("review_notes", "required_checks", "tool_references"):
        if not isinstance(card.get(key), list) or not card[key]:
            raise ValueError(f"knowledge.{key}: non-empty array required")
        for value in card[key]:
            rules.require_nonempty_string(value, key)
    entry = rules.validate_catalog({"schema_version": 1, "entries": [card["applicability"]]})[0]
    if entry["rule_ids"] != [module["id"]]:
        raise ValueError("applicability references another module")
    sources = []
    for source in card["sources"]:
        path = local_file(root, source["path"])
        locator = rules.require_nonempty_string(source.get("locator"), "source.locator")
        if locator not in path.read_text(encoding="utf-8-sig"):
            raise ValueError(f"source locator missing: {locator}")
        sources.append({**source, "sha256": digest(path)})
    if set(entry["sources"]) != {s["id"] for s in sources}:
        raise ValueError("source IDs do not match applicability")
    for reference in card["tool_references"]:
        local_file(root, reference)
    facts = profile["facts"]
    for field in ("input_voltage_v", "output_voltage_v", "load_current_a", "ambient_temperature_c"):
        value = facts.get(field, {}).get("value")
        if value is not None:
            rules.finite_number(value, field)
            if field != "ambient_temperature_c" and value < 0:
                raise ValueError(f"{field}: negative value")
    selection = rules.select_entry(entry, facts)
    baseline = card["comparison_baseline"]
    if baseline.get("kind") != "synthetic_example_not_rating":
        raise ValueError("comparison baseline must not be presented as a rating")
    differences = []
    for field, expected in baseline["facts"].items():
        state, actual = rules.fact_state(facts, field)
        if state != "known" or not rules.json_equal(actual, expected):
            differences.append({"field": field, "baseline": expected, "requested": actual,
                                "fact_state": state, "action": "重新核实该参数及受影响的检查"})
    result.update({"module_id": module["id"], "revision": card["revision"],
                   "knowledge_sha256": digest(card_path), "scope": card["scope"],
                   "selection": selection, "sources": sources, "differences": differences,
                   "comparison_basis": baseline["kind"], "maturity": module["maturity"],
                   "status": selection["status"],
                   "label": {"candidate": "可复用草案，待工程审查", "needs_input": "需要补信息",
                             "excluded": "不适用"}[selection["status"]],
                   "verification": {"electrical": "unknown", "pcb": "not_tested",
                                    "hardware": "not_tested", "engineer_review": "pending"}})
    if selection["status"] == "candidate":
        asset_name = module.get("layoutInput")
        if module["maturity"] not in {"compose_ready", "verified"} or not asset_name:
            return {**result, "status": "needs_input", "label": "需要补信息",
                    "reason": "尚无可复用的局部布局输入"}
        asset_path = local_file(root / "library/modules", asset_name)
        asset = rules.read_json(asset_path)
        result["draft"] = {"status": "review_only", "asset": asset_name,
                           "asset_sha256": digest(asset_path),
                           "components": asset["connectivity"]["components"],
                           "nets": asset["connectivity"]["nets"],
                           "requested_parameters": facts,
                           "review_notes": card["review_notes"],
                           "required_checks": card["required_checks"],
                           "tool_references": card["tool_references"],
                           "execution": "未绑定真实工程；不得将示例 projectId/documentId 直接用于 Apply"}
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("request", type=Path, help="JSON query + existing target profile")
    parser.add_argument("--library-root", type=Path, default=ROOT)
    args = parser.parse_args()
    sys.stdout.reconfigure(encoding="utf-8")
    try:
        result = evaluate(rules.read_json(args.request), args.library_root)
        print(json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False))
        return 0
    except (ValueError, OSError, KeyError, TypeError) as exc:
        print(json.dumps({"status": "error", "error": str(exc)}, ensure_ascii=False))
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
