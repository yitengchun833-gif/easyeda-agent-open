#!/usr/bin/env python3
"""Local structured conversion; preserve original, report incomplete provenance."""
import argparse
import hashlib
import importlib.metadata
import json
from pathlib import Path
from datetime import datetime, timezone


def sha(path):
    with Path(path).open("rb") as f:
        return hashlib.file_digest(f, "sha256").hexdigest()


def dump(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")


def convert(src, out, document_id=None, version=None):
    src, out = Path(src).resolve(strict=True), Path(out).resolve()
    bundle = out.with_suffix(out.suffix + ".assets")
    if src == out or src.is_relative_to(bundle):
        raise ValueError("output and asset directory must not contain source")
    if out.suffix.lower() != ".md":
        raise ValueError("output must be .md")
    out.parent.mkdir(parents=True, exist_ok=True)
    bundle.mkdir(parents=True, exist_ok=True)
    source_hash = sha(src)
    manifest = {"schema": 1, "document_id": document_id or src.stem,
                "document_version": version or source_hash[:12],
                "source_path": str(src), "source_hash": source_hash,
                "parser": "docling", "parser_version": importlib.metadata.version("docling"),
                "parse_time": datetime.now(timezone.utc).isoformat(),
                "parse_status": "failed", "quality_status": "unreviewed",
                "analysis_status": "not_analyzed", "index_path": "index.json",
                "model_path": "document.json", "markdown_path": str(out), "issues": []}
    dest = bundle / "manifest.json"
    if dest.exists():
        previous = json.loads(dest.read_text(encoding="utf-8"))
        if previous.get("source_path") != str(src) or previous.get("document_id") != manifest["document_id"]:
            raise ValueError("output belongs to another document")
    dump(dest, manifest)
    try:
        from docling.datamodel.settings import settings
        settings.inference.compile_torch_models = False
        from docling.document_converter import DocumentConverter, PdfFormatOption
        from docling.datamodel.base_models import InputFormat
        from docling.datamodel.pipeline_options import PdfPipelineOptions
        from docling_core.types.doc import ImageRefMode
        options = PdfPipelineOptions()
        options.enable_remote_services = False
        options.generate_page_images = True
        options.generate_picture_images = True
        result = DocumentConverter(format_options={
            InputFormat.PDF: PdfFormatOption(pipeline_options=options)
        }).convert(str(src))
        doc = result.document
        images = bundle / "images"
        images.mkdir(exist_ok=True)
        doc.save_as_json(bundle / "document.json", artifacts_dir=images,
                         image_mode=ImageRefMode.REFERENCED)
        doc.save_as_markdown(out, artifacts_dir=images, image_mode=ImageRefMode.REFERENCED)
        index = {"schema": 1, "document_id": manifest["document_id"],
                 "document_version": manifest["document_version"],
                 "items": {}, "sections": [], "figures": [], "pages": {}}
        for item, depth in doc.iterate_items():
            anchor = item.self_ref
            label = getattr(getattr(item, "label", None), "value", "group")
            text = getattr(item, "text", "")
            if label == "table":
                text = item.export_to_html(doc=doc)
            locations = [p.model_dump(mode="json") for p in getattr(item, "prov", [])]
            entry = {"label": label, "depth": depth, "text": text,
                     "locations": locations, "model_ref": anchor}
            if label in ("picture", "table"):
                picture = item.get_image(doc)
                if picture is not None:
                    name = anchor.strip("#/").replace("/", "-") + ".png"
                    picture.save(images / name)
                    entry["asset"] = "images/" + name
                    entry["asset_hash"] = sha(images / name)
                else:
                    manifest["issues"].append(anchor + ": visual asset unavailable; inspect original")
                index["figures"].append(anchor)
            if label in ("section_header", "title"):
                index["sections"].append(anchor)
            if not locations:
                entry["location_note"] = "No reliable page/bbox; use section/model_ref or native locator"
            index["items"][anchor] = entry
        for page_no, page in doc.pages.items():
            if page.image is not None:
                name = "page-" + str(page_no) + ".png"
                page.image.pil_image.save(images / name)
                index["pages"][str(page_no)] = "images/" + name
        if not index["items"]:
            raise ValueError("no extracted items")
        if sha(src) != source_hash:
            raise ValueError("source changed during conversion")
        if not index["pages"]:
            manifest["issues"].append("Page images unavailable; do not invent page numbers")
        manifest["issues"].extend(str(e)[:500] for e in getattr(result, "errors", []))
        raw_status = str(getattr(result.status, "value", result.status))
        manifest["parse_status"] = "success" if raw_status == "success" else "partial"
        if any("visual asset unavailable" in issue for issue in manifest["issues"]):
            manifest["parse_status"] = "partial"
        dump(bundle / "index.json", index)
        nav = ["# " + manifest["document_id"], "", "来源：" + str(src),
               "版本：" + manifest["document_version"],
               "页码为文件页序，不等于印刷页码。", "", "## 目录"]
        nav += ["- " + index["items"][a]["text"] + " — " + a for a in index["sections"]]
        nav += ["", "## 图表索引"]
        nav += ["- " + a + " — " + index["items"][a].get("asset", "查看原件")
                for a in index["figures"]]
        nav += ["", "## 缺口"] + ["- " + x for x in manifest["issues"]]
        (bundle / "index.md").write_text("\n".join(nav), encoding="utf-8")
    except Exception as e:
        manifest["parse_status"] = "failed"
        manifest["issues"].append(type(e).__name__ + ": " + str(e)[:1000])
        raise
    finally:
        dump(dest, manifest)
    return manifest


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("input")
    p.add_argument("output")
    p.add_argument("--document-id")
    p.add_argument("--version")
    a = p.parse_args()
    print(json.dumps(convert(a.input, a.output, a.document_id, a.version), ensure_ascii=False))
