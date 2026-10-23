#!/usr/bin/env python3
"""Catalog all declared sources and search structured local extracts."""
import argparse
import json
from pathlib import Path
import subprocess
import sys


def read(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def main():
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="cmd", required=True)
    batch = sub.add_parser("catalog")
    batch.add_argument("list", type=Path, help="JSON list: id, input, output, version optional")
    batch.add_argument("--report", required=True, type=Path)
    find = sub.add_parser("find")
    find.add_argument("manifest", type=Path)
    find.add_argument("query")
    find.add_argument("--limit", type=int, default=12)
    show = sub.add_parser("show")
    show.add_argument("manifest", type=Path)
    show.add_argument("anchor")
    a = p.parse_args()
    if a.cmd == "catalog":
        records = read(a.list)
        if not isinstance(records, list) or not records:
            raise ValueError("nonempty catalog list required")
        ids = [x["id"] for x in records]
        inputs = {Path(x["input"]).resolve() for x in records}
        outputs = [Path(x["output"]).resolve() for x in records]
        if len(set(ids)) != len(ids) or len(set(outputs)) != len(outputs):
            raise ValueError("duplicate ID/output")
        if a.report.resolve() in inputs | set(outputs) | {a.list.resolve()}:
            raise ValueError("report overlaps source/catalog/output")
        for out in outputs:
            if a.report.resolve().is_relative_to(out.with_suffix(out.suffix + ".assets")):
                raise ValueError("report overlaps extracted assets")
            if out in inputs or any(src.is_relative_to(out.with_suffix(out.suffix + ".assets"))
                                    for src in inputs):
                raise ValueError("output overlaps a catalog source")
        results = []
        a.report.parent.mkdir(parents=True, exist_ok=True)
        for item in records:
            command = [sys.executable, str(Path(__file__).with_name("intake.py")),
                       item["input"], item["output"], "--document-id", item["id"]]
            if item.get("version"):
                command += ["--version", item["version"]]
            if item.get("mode"):
                command += ["--mode", item["mode"]]
            cp = subprocess.run(command, capture_output=True, text=True,
                                encoding="utf-8", errors="replace")
            row = {"id": item["id"], "input": item["input"], "output": item["output"],
                   "status": "failed", "error": cp.stderr[-1500:] if cp.returncode else ""}
            manifest = Path(item["output"]).with_suffix(Path(item["output"]).suffix + ".assets") / "manifest.json"
            if cp.returncode == 0:
                fallback = item.get("mode") in ("markitdown", "normal")
                row["status"] = read(manifest)["parse_status"] if manifest.exists() and not fallback else "partial"
                row["manifest"] = str(manifest) if manifest.exists() and not fallback else None
            results.append(row)
            # Persist each outcome; interrupted rows remain explicitly not_processed.
            pending = [{"id": x["id"], "status": "not_processed"}
                       for x in records[len(results):]]
            a.report.write_text(json.dumps(results + pending, ensure_ascii=False, indent=2),
                                encoding="utf-8")
        print(json.dumps(results, ensure_ascii=False, indent=2))
        return int(any(x["status"] == "failed" for x in results))
    m = read(a.manifest)
    if m["parse_status"] not in ("success", "partial"):
        raise ValueError("extract is failed; do not read stale output")
    if not m.get("index_path"):
        raise ValueError("text fallback has no structured index")
    index = read(a.manifest.parent / m["index_path"])
    if a.cmd == "show":
        result = {"anchor": a.anchor, **index["items"][a.anchor]}
    else:
        result = [{"anchor": key, **value} for key, value in index["items"].items()
                  if a.query.casefold() in value["text"].casefold()][:max(1, min(a.limit, 100))]
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ValueError, OSError, KeyError, TypeError) as e:
        raise SystemExit(str(e))
