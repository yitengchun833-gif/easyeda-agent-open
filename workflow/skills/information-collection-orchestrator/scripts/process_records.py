#!/usr/bin/env python3
"""Normalize, deduplicate, and score locally collected records. No network calls."""
import argparse, hashlib, json, re
from datetime import datetime, timezone
from pathlib import Path
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

TRACKING = {"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "fbclid", "gclid"}
PRIMARY = {
    "official": 0.35,
    "government": 0.35,
    "standard": 0.35,
    "official_documentation": 0.35,
    "peer_reviewed": 0.35,
    "primary_data": 0.35,
    "company_site": 0.35,
    "reputable_news": 0.25,
    "academic_index": 0.25,
    "repository": 0.25,
    "job_board": 0.20,
    "community": 0.10,
    "social": 0.05,
}
VALID_COLLECTORS = {
    "anysearch", "agent-reach", "firecrawl", "firecrawl-search", "crawl4ai",
    "playwright", "chrome-control", "mediacrawler", "manual",
}

def load(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))

def canonicalize(url):
    parts = urlsplit(url.strip())
    query = [(k, v) for k, v in parse_qsl(parts.query, keep_blank_values=True) if k.lower() not in TRACKING]
    return urlunsplit((parts.scheme.lower(), parts.netloc.lower(), parts.path.rstrip("/") or "/", urlencode(sorted(query)), ""))

def clean(value):
    return re.sub(r"\s+", " ", str(value or "")).strip()

def score(record):
    value, reasons = 0.0, []
    source_type = clean(record.get("source_type")).lower()
    base = PRIMARY.get(source_type, 0.10)
    value += base
    reasons.append(f"source_type:{source_type or 'unknown'}={base:.2f}")
    if record.get("published_at"):
        value += 0.15; reasons.append("published_at=0.15")
    if len(clean(record.get("evidence", {}).get("excerpt"))) >= 40:
        value += 0.20; reasons.append("evidence=0.20")
    if clean(record.get("organization")):
        value += 0.10; reasons.append("organization=0.10")
    if clean(record.get("location")):
        value += 0.05; reasons.append("location=0.05")
    if record.get("collector") in VALID_COLLECTORS:
        value += 0.10; reasons.append("known_collector=0.10")
    return min(round(value, 2), 1.0), reasons

def key(record):
    if record.get("canonical_url"):
        return "url:" + record["canonical_url"]
    fields = "|".join(clean(record.get(x)).casefold() for x in ("organization", "title", "location", "published_at"))
    return "field:" + hashlib.sha256(fields.encode()).hexdigest()

def normalize(raw, run_id, now):
    url = clean(raw.get("source_url"))
    if not url.startswith(("http://", "https://")):
        raise ValueError("source_url must be an http(s) URL")
    title = clean(raw.get("title"))
    if not title:
        raise ValueError("title is required")
    collector = clean(raw.get("collector", "manual")).lower()
    if collector not in VALID_COLLECTORS:
        raise ValueError(f"unsupported collector: {collector}")
    record = {"record_kind": clean(raw.get("record_kind", "other")), "title": title, "organization": clean(raw.get("organization")), "location": clean(raw.get("location")), "published_at": raw.get("published_at") or None, "source_url": url, "canonical_url": canonicalize(url), "source_name": clean(raw.get("source_name", urlsplit(url).netloc)), "source_type": clean(raw.get("source_type")).lower(), "retrieved_at": raw.get("retrieved_at") or now, "collector": collector, "discovered_by": [collector], "evidence": {"excerpt": clean(raw.get("evidence", {}).get("excerpt")), "raw_path": clean(raw.get("evidence", {}).get("raw_path"))}, "metadata": raw.get("metadata", {}), "run_id": run_id}
    record["quality_score"], record["quality_reasons"] = score(record)
    record["record_id"] = hashlib.sha256((run_id + "|" + key(record)).encode()).hexdigest()[:16]
    return record

def main():
    p = argparse.ArgumentParser()
    p.add_argument("--task", required=True); p.add_argument("--config", required=True); p.add_argument("--input", required=True); p.add_argument("--output", required=True)
    args = p.parse_args(); task, config, raw = load(args.task), load(args.config), load(args.input)
    items = raw["records"] if isinstance(raw, dict) else raw
    max_records = min(task.get("limits", {}).get("max_records", len(items)), config["limits"]["max_records_per_run"])
    now = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
    accepted, low_quality, duplicates, failures, selected = [], [], [], [], {}
    minimum_quality = float(config.get("quality", {}).get("minimum_quality_score", 0.0))
    for index, item in enumerate(items[:max_records]):
        try:
            record = normalize(item, task["run_id"], now); fingerprint = key(record)
            if fingerprint in selected:
                existing = selected[fingerprint]
                discovered_by = sorted(set(existing["discovered_by"] + record["discovered_by"]))
                if record["quality_score"] > existing["quality_score"]:
                    record["discovered_by"] = discovered_by
                    selected[fingerprint] = record
                    duplicates.append({"record_index": index, "duplicate_of": existing["record_id"], "reason": "replaced_by_higher_quality_record"})
                else:
                    existing["discovered_by"] = discovered_by
                    duplicates.append({"record_index": index, "duplicate_of": existing["record_id"], "reason": "canonical_url_or_key"})
                continue
            selected[fingerprint] = record
        except (ValueError, TypeError, KeyError) as exc:
            failures.append({"record_index": index, "status": "invalid", "reason": str(exc)})
    for record in selected.values():
        if record["quality_score"] >= minimum_quality:
            accepted.append(record)
        else:
            low_quality.append({
                "record": record,
                "reason": f"quality_score_below_threshold:{record['quality_score']:.2f}<{minimum_quality:.2f}",
            })
    accepted.sort(key=lambda item: item["quality_score"], reverse=True)
    result = {"schema_version": "1.0", "run_id": task["run_id"], "generated_at": now, "task": {"objective": task.get("objective", ""), "record_kind": task.get("record_kind", "")}, "results": accepted, "low_quality": low_quality, "duplicates": duplicates, "failures": failures, "stats": {"input_records": len(items), "processed_records": min(len(items), max_records), "accepted_records": len(accepted), "low_quality_records": len(low_quality), "duplicate_records": len(duplicates), "failed_records": len(failures), "unprocessed_due_to_limit": max(0, len(items) - max_records)}}
    target = Path(args.output); target.parent.mkdir(parents=True, exist_ok=True); target.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(result["stats"], ensure_ascii=False))

if __name__ == "__main__": main()
