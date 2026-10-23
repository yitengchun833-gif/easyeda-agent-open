#!/usr/bin/env python3
"""Offline source-to-plan gate. Reads existing typed actions; never executes EDA."""
import argparse
import hashlib
import json
import math
from pathlib import Path
import re
import sys

EPS = 0.05
PCB_FIELDS = ("manufacturer", "mpn", "package", "pin_map", "dimensions")
RULE_FILES = {
    "management": Path(__file__).resolve().parents[2] / "evidence-to-delivery/references/rule-management.md",
    "drawing": Path(__file__).resolve().parents[1] / "references/schematic-layout-conventions.md",
}


def read(path):
    return json.loads(Path(path).read_text(encoding="utf-8-sig"))


def sha(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def rule_digest(requirements):
    """Bind confirmed semantics and evidence, not an evolving drawing action list."""
    rules = requirements.get("execution_rules", {})
    ids = {b.get("id") for c in rules.get("checks", []) for b in c.get("basis", [])}
    ids.update(c.get("decision", {}).get("source") for c in rules.get("checks", []))
    scope = rules.get("edit_scope", {})
    if isinstance(scope, dict):
        ids.add(scope.get("target_source"))
        authorization = scope.get("authorization")
        if isinstance(authorization, dict):
            ids.add(authorization.get("source"))
    value = {"rules": {k: v for k, v in rules.items() if k != "confirmation"},
             "target": {k: requirements.get("target", {}).get(k) for k in ("project", "doc")},
             "sources": {s["id"]: s.get("sha256") for s in requirements.get("sources", []) if s["id"] in ids}}
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True,
                                    separators=(",", ":")).encode()).hexdigest()


def check_rules(requirements, plan, sources, dependencies, fail, rule_contents):
    rules = requirements.get("execution_rules")
    if not isinstance(rules, dict):
        fail("execution_rules_missing", "execution_rules", "confirmed task rules are required")
        return {}

    def proof(value):
        return (isinstance(value, dict) and value.get("source") in sources and
                isinstance(value.get("locator"), str) and bool(value["locator"].strip()))

    for name, path in RULE_FILES.items():
        actual = dependencies[str(path)]
        if rules.get("loaded", {}).get(name) != actual:
            fail("rule_source_changed", name, "load the current canonical rule source")
    expected = set(re.findall(r"^\| (SCH-(?:G|L)\d+) \|", rule_contents["drawing"].decode("utf-8"), re.M))
    if not expected:
        fail("internal_rule_catalog_empty", "drawing", "canonical rule IDs are missing")
    checks = rules.get("checks", [])
    if "edit_scope" in rules:
        scope = rules["edit_scope"]
        kinds = {"components", "wires", "buses", "arcs", "circles", "rectangles", "polygons", "texts", "attributes", "objects"}
        if (not isinstance(scope, dict) or not re.fullmatch(r"[0-9a-f]{64}", str(scope.get("baseline_sha256", ""))) or
                scope.get("mode", "layout") not in ("layout", "design") or
                scope.get("acceptance", "page") not in ("local", "page") or
                not isinstance(scope.get("mutable_ids"), dict) or set(scope["mutable_ids"]) - kinds or
                any(not isinstance(ids, list) or any(not isinstance(i, str) or not i for i in ids) or len(set(ids)) != len(ids)
                    for ids in scope["mutable_ids"].values())):
            fail("edit_scope_invalid", "edit_scope", "baseline fingerprint and typed object ID allowlist required")
        elif scope.get("mode") == "design":
            if scope.get("target_source") not in sources and not (plan.get("rewire") is True and isinstance(scope.get("rewire"), dict)):
                fail("design_target_missing", "edit_scope", "independent canonical target source required")
            if not proof(scope.get("authorization")):
                fail("design_authorization_missing", "edit_scope", "explicit new design/circuit-change authorization required")
        if isinstance(scope, dict) and scope.get("rewire") is not None:
            if (scope.get("mode") not in ("layout", "design") or scope.get("acceptance") != "page" or
                    not isinstance(scope["rewire"], dict) or not proof(scope["rewire"].get("authorization"))):
                fail("rewire_authorization_missing", "edit_scope", "whole-page rewire needs page scope and source-backed authorization")
    if not isinstance(checks, list) or not checks:
        fail("rules_empty", "checks", "effective rules and exclusions must be recorded")
        checks = []
    seen, defaults, adopted, protected = set(), set(), [], False
    for check in checks:
        key = check.get("id")
        if not isinstance(key, str) or not key.strip() or key in seen:
            fail("rule_id_invalid", key, "unique nonempty instance ID required")
        seen.add(key)
        rid = check.get("rule_id")
        if not isinstance(rid, str) or not rid.strip():
            fail("rule_id_invalid", key, "stable rule_id required")
        elif check.get("origin") == "internal" and rid.startswith("SCH-") and rid not in expected:
            fail("internal_rule_unknown", key, "unknown canonical rule ID; check source version and spelling")
        if rid in expected:
            defaults.add(rid)
            if check.get("origin") != "internal":
                fail("internal_rule_origin", key, "default rules must retain internal provenance")
        if (check.get("origin") not in ("internal", "external") or
                check.get("category") not in ("scope", "process", "electrical", "schematic", "pcb", "tool", "evidence") or
                check.get("strength") not in ("hard", "required", "advice") or
                check.get("disposition") not in ("apply", "exclude", "override", "conflict", "needs_input")):
            fail("rule_classification_invalid", key, "explicit type, source, strength and disposition required")
        for field in ("target", "acceptance_criterion", "method"):
            if not isinstance(check.get(field), str) or not check[field].strip():
                fail("rule_requirement_missing", key, field)
        bases = check.get("basis", [])
        if not isinstance(bases, list) or not bases or any(
                b.get("id") not in sources or not b.get("locator") for b in bases):
            fail("rule_basis_missing", key, "hash-pinned basis and locator required")
        disposition = check.get("disposition")
        if disposition in ("conflict", "needs_input"):
            fail("rule_unresolved", key, disposition)
        if disposition in ("exclude", "override"):
            if not check.get("reason"):
                fail("rule_exclusion_unexplained", key, "explicit applicability/override reason required")
            if rid in expected or disposition == "override":
                if not proof(check.get("decision")):
                    fail("rule_override_unconfirmed", key, "actual user decision evidence required")
            if disposition == "override" and check.get("strength") == "hard":
                fail("hard_rule_override", key, "report conflicting facts instead of overriding them")
        if disposition == "apply":
            adopted.append(key)
            protected |= rid == "SCH-G08"
    if expected - defaults:
        fail("internal_rules_missing", "checks", sorted(expected - defaults))
    fingerprint = rule_digest(requirements)
    confirmation = rules.get("confirmation", {})
    version = rules.get("version")
    confirmed = (isinstance(version, str) and bool(version.strip()) and
                 confirmation.get("version") == version and confirmation.get("sha256") == fingerprint and
                 proof(confirmation.get("evidence")))
    if not confirmed:
        fail("rules_not_confirmed", "confirmation", "current rule version/hash needs actual user confirmation")
    if plan.get("rule_binding") != {"version": version, "sha256": fingerprint}:
        fail("plan_rules_stale", "rule_binding", "execution plan must bind the confirmed rule revision")
    if protected:
        fields = {"addintopcb", "addintobom", "locked", "islocked", "selectable", "visible", "enabled", "disabled"}
        for i, action in enumerate(plan.get("actions", [])):
            if action.get("action") == "schematic.component.modify":
                changed = fields & {k.lower() for k in action.get("payload", {}).get("patch", {})}
                if changed:
                    fail("component_property_protected", i, sorted(changed))
    return {"version": version, "sha256": fingerprint, "confirmed": confirmed,
            "effective_ids": adopted,
            "limits": "Rule registration is not observed electrical, graphical or persistence compliance."}


def point(value):
    if (not isinstance(value, list) or len(value) != 2 or
            any(isinstance(v, bool) or not isinstance(v, (int, float)) or
                not math.isfinite(v) for v in value)):
        raise ValueError("point must contain two finite numbers")
    return tuple(value)


def segments(points):
    if not isinstance(points, list) or not points:
        raise ValueError("wire has no points")
    if isinstance(points[0], list):
        pts = [point(p) for p in points]
    else:
        if len(points) % 2:
            raise ValueError("wire has odd coordinate count")
        pts = [point(points[i:i+2]) for i in range(0, len(points), 2)]
    if len(pts) < 2:
        raise ValueError("wire needs two points")
    result = []
    for a, b in zip(pts, pts[1:]):
        if a == b or (a[0] != b[0] and a[1] != b[1]):
            raise ValueError("zero-length or diagonal wire")
        result.append((a, b))
    return result


def on_segment(p, a, b):
    return (min(a[0], b[0])-EPS <= p[0] <= max(a[0], b[0])+EPS and
            min(a[1], b[1])-EPS <= p[1] <= max(a[1], b[1])+EPS and
            (abs(a[0]-b[0]) < EPS and abs(p[0]-a[0]) < EPS or
             abs(a[1]-b[1]) < EPS and abs(p[1]-a[1]) < EPS))


def directions_at(p, wires, net):
    result = set()
    for _, candidate, a, b in wires:
        if candidate not in (None, "", net) or not on_segment(p, a, b):
            continue
        for q in (a, b):
            if q[0] < p[0]-EPS: result.add("left")
            if q[0] > p[0]+EPS: result.add("right")
            if q[1] < p[1]-EPS: result.add("down")
            if q[1] > p[1]+EPS: result.add("up")
    return result


def declared_text(value):
    # Presence in a typed payload is only a planning check, not proof of visibility.
    result = set()
    if isinstance(value, dict):
        for k, v in value.items():
            if k in ("name", "designator", "text", "net", "value") and isinstance(v, str):
                result.add(v)
            elif isinstance(v, (dict, list)):
                result.update(declared_text(v))
    elif isinstance(value, list):
        for v in value:
            result.update(declared_text(v))
    return result


def audit(requirements, plan, root, stage="schematic", phase="complete"):
    findings, sources, dependencies, gaps = [], {}, {}, []
    # Read the two canonical documents once; their hash and ID catalog share these bytes.
    rule_contents = {name: path.read_bytes() for name, path in RULE_FILES.items()}
    dependencies.update({str(RULE_FILES[name]): hashlib.sha256(data).hexdigest()
                         for name, data in rule_contents.items()})
    source_hashes = dict(dependencies)

    def fail(code, item, detail):
        findings.append(dict(code=code, item=item, detail=detail))

    if phase not in ("placement", "complete") or (stage == "pcb" and phase != "complete"):
        fail("invalid_phase", "phase", phase)

    for src in requirements.get("sources", []):
        key = src["id"]
        path = (root / src["path"]).resolve()
        if key in sources:
            fail("duplicate_source", key, "source IDs must be unique")
        actual = source_hashes.get(str(path))
        if actual is None and path.is_file():
            actual = source_hashes[str(path)] = sha(path)
        if actual != src.get("sha256") or actual is None:
            fail("source_changed", key, str(path))
        else:
            sources[key] = src
            dependencies[str(path)] = actual
    if not sources:
        fail("no_sources", "sources", "hash-pinned original evidence is required")

    effective_rules = check_rules(requirements, plan, sources, dependencies, fail, rule_contents)

    def evidence(proof):
        return (isinstance(proof, dict) and proof.get("source") in sources and
                isinstance(proof.get("locator"), str) and bool(proof["locator"].strip()))

    target = requirements.get("target", {})
    if target.get("axis") != "y-up" or target.get("units") != "raw":
        fail("coordinate_contract", "target", "declare y-up and raw units")
    route = plan.get("route", {})
    if (not target.get("project") or not target.get("doc") or
            any(route.get(k) != target.get(k) for k in ("project", "doc"))):
        fail("target_mismatch", "route", "exact project and document required")
    sheet = target.get("sheet")
    if (not isinstance(sheet, list) or len(sheet) != 4 or
            not all(isinstance(x, (float, int)) and not isinstance(x, bool) and
                    math.isfinite(x) for x in sheet) or
            sheet[0] >= sheet[2] or sheet[1] >= sheet[3]):
        fail("sheet_unknown", "target", "measured [minX,minY,maxX,maxY] required")
        sheet = None

    wires, texts, named = [], set(), {}
    actions = plan.get("actions")
    if not isinstance(actions, list) or not actions:
        fail("empty_plan", "actions", "existing typed action array required")
        actions = []
    for i, action in enumerate(actions):
        if stage == "schematic" and not str(action.get("action", "")).startswith("schematic."):
            fail("action_scope_mismatch", i, "only schematic actions belong in this drawing plan")
        if phase == "placement" and action.get("action") != "schematic.component.place":
            fail("placement_action_only", i, action.get("action"))
        payload = action.get("payload", {})
        texts.update(declared_text(payload))
        action_route = {k: action.get(k, route.get(k)) for k in ("project", "doc")}
        if any(action_route[k] != target.get(k) for k in ("project", "doc")):
            fail("action_target_mismatch", i, "action overrides the pinned route")
        points = []
        try:
            if action.get("action") == "schematic.wire.create":
                segs = segments(payload.get("points"))
                wires.extend((i, payload.get("net"), a, b) for a, b in segs)
                points = [p for a, b in segs for p in (a, b)]
                if payload.get("net"):
                    named[i] = (payload["net"], segs)
            elif "x" in payload and "y" in payload:
                points = [point([payload["x"], payload["y"]])]
            elif action.get("action") == "schematic.component.modify":
                patch = payload.get("patch", {})
                if "x" in patch or "y" in patch:
                    points = [point([patch.get("x"), patch.get("y")])]
            if sheet and any(not (sheet[0] <= x <= sheet[2] and
                                  sheet[1] <= y <= sheet[3]) for x, y in points):
                fail("outside_sheet", i, "planned coordinate outside measured sheet")
        except (ValueError, TypeError) as exc:
            fail("invalid_geometry", i, str(exc))

    assertions = requirements.get("depiction", [])
    if not assertions:
        fail("no_depiction_checks", "depiction", "source-specific checks are required")
    seen = set()
    for assertion in (assertions if phase != "placement" else []):
        key, kind = assertion["id"], assertion["kind"]
        if key in seen:
            fail("duplicate_assertion", key, "assertion IDs must be unique")
        seen.add(key)
        if not evidence(assertion.get("evidence")):
            fail("missing_evidence", key, "source and page/region locator required")
        if kind == "junction":
            wanted = set(assertion["directions"])
            if not wanted or not wanted <= {"left", "right", "up", "down"}:
                fail("invalid_directions", key, "explicit nonempty directions required")
            actual = directions_at(point(assertion["at"]), wires, assertion["net"])
            if not wanted <= actual:
                fail("missing_branch", key, sorted(wanted-actual))
        elif kind == "text":
            if not isinstance(assertion.get("text"), str) or not assertion["text"]:
                fail("invalid_text", key, "nonempty exact text required")
            elif assertion["text"] not in texts:
                fail("missing_text", key, assertion["text"])
        elif kind == "named_wire_budget":
            low, high = point(assertion["bounds"][:2]), point(assertion["bounds"][2:])
            if low[0] > high[0] or low[1] > high[1]:
                fail("invalid_bounds", key, "ordered bounds required")
            ids = [i for i, (net, segs) in named.items() if net == assertion["net"]
                   and any(max(a[0], b[0]) >= low[0] and min(a[0], b[0]) <= high[0]
                           and max(a[1], b[1]) >= low[1] and min(a[1], b[1]) <= high[1]
                           for a, b in segs)]
            maximum = assertion["maximum"]
            if isinstance(maximum, bool) or not isinstance(maximum, int) or maximum < 0:
                fail("invalid_budget", key, "nonnegative integer required")
            elif len(ids) > maximum:
                fail("repeated_net_assignment", key, dict(count=len(ids), maximum=maximum))
        else:
            fail("unsupported_assertion", key, kind)

    parts = requirements.get("parts", [])
    if not parts:
        fail("no_part_review", "parts", "record even schematic-only provisional selection")
    refs = set()
    for item in parts:
        ref = item["ref"]
        if ref in refs:
            fail("duplicate_ref", ref, "one review per designator")
        refs.add(ref)
        required, selected = item.get("required", {}), item.get("selected", {})
        proofs = item.get("evidence", {})
        if not required:
            fail("no_part_constraints", ref, "source-backed part constraints required")
        for field, value in required.items():
            if value in (None, "", [], {}):
                fail("unknown_part_constraint", ref + "." + field, "unknown is not a confirmed value")
            if not evidence(proofs.get(field)):
                fail("part_evidence_missing", ref + "." + field, "source locator required")
            if selected.get(field) != value:
                fail("part_mismatch", ref + "." + field,
                     dict(required=value, selected=selected.get(field)))
        missing = [k for k in PCB_FIELDS if k not in required or
                   not evidence(proofs.get(k)) or required[k] in (None, "", [], {})]
        if missing:
            gaps.append(dict(ref=ref, missing_for_pcb=missing))
            if stage == "pcb":
                fail("pcb_evidence_missing", ref, missing)
    return dict(scope="schematic-preflight", stage=stage, phase=phase, passed=not findings,
                execution_rules=effective_rules,
                complete_plan_checked=phase == "complete",
                deferred_assertions=[a.get("id") for a in assertions] if phase == "placement" else [],
                findings=findings, pcb_gaps=gaps, evidence_files=dependencies,
                coverage={"typed_actions": len(actions), "depiction_assertions": len(assertions) if phase == "complete" else 0,
                          "parts_reviewed": len(parts)},
                limits=["Source interpretation and actual label visibility require source/render review.",
                        "Anchor bounds only; body/text extents require existing layout checks.",
                        "No electrical, package drawing, DRC, persistence or manufacturing approval.",
                        "Named-wire count is a planning risk check, not observed label count."])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--requirements", type=Path, required=True)
    parser.add_argument("--plan", type=Path, required=True)
    parser.add_argument("--stage", choices=("schematic", "pcb"), default="schematic")
    parser.add_argument("--out", type=Path)
    parser.add_argument("--phase", choices=("placement", "complete"), default="complete")
    args = parser.parse_args()
    protected = {args.requirements.resolve(), args.plan.resolve(), *RULE_FILES.values()}
    try:
        requirements, plan = read(args.requirements), read(args.plan)
        for source in requirements.get("sources", []):
            source_path = (args.requirements.resolve().parent / source["path"]).resolve()
            if source_path in {args.requirements.resolve(), args.plan.resolve()}:
                raise ValueError("source cannot be the generated requirements or execution plan")
            protected.add(source_path)
        if args.out and args.out.resolve() in protected:
            raise ValueError("report output cannot overwrite an input or source")
        report = audit(requirements, plan, args.requirements.resolve().parent, args.stage, args.phase)
        report["inputs"] = {str(p.resolve()): sha(p) for p in (args.requirements, args.plan)}
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as exc:
        report = dict(scope="schematic-preflight", passed=False, error=str(exc))
    output = json.dumps(report, ensure_ascii=False, indent=2)
    if args.out and args.out.resolve() not in protected:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(output+"\n", encoding="utf-8")
    print(output)
    return 0 if report["passed"] else 2


if __name__ == "__main__":
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    sys.exit(main())
