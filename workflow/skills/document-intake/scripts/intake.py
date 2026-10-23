#!/usr/bin/env python3
import argparse, hashlib, json, os, shutil, subprocess
from pathlib import Path

TOOLS_ROOT=Path.home()/".codex"/"tools"/"evidence-to-delivery"
MARKITDOWN=Path(os.environ.get("EVIDENCE_MARKITDOWN") or shutil.which("markitdown") or TOOLS_ROOT/"markitdown"/".venv"/"Scripts"/"markitdown.exe")
DOCLING_PYTHON=Path(os.environ.get("EVIDENCE_DOCLING_PYTHON") or TOOLS_ROOT/"docling"/".venv"/"Scripts"/"python.exe")
DOCLING_HELPER=Path(__file__).with_name("docling_convert.py")

def sha256(p):
    h=hashlib.sha256()
    with p.open("rb") as f:
        for c in iter(lambda:f.read(1048576),b""): h.update(c)
    return h.hexdigest().upper()

p=argparse.ArgumentParser(); p.add_argument("input"); p.add_argument("output"); p.add_argument("--mode",choices=["docling","markitdown","normal","complex"],default="docling"); p.add_argument("--log"); p.add_argument("--document-id"); p.add_argument("--version"); a=p.parse_args()
src=Path(a.input).resolve(strict=True); out=Path(a.output).resolve(); before=sha256(src); out.parent.mkdir(parents=True,exist_ok=True)
if out==src or src.is_relative_to(out.with_suffix(out.suffix + ".assets")): raise SystemExit("output must differ from input and assets")
if a.log and Path(a.log).resolve() in (src, out): raise SystemExit("log overlaps input/output")
manifest=out.with_suffix(out.suffix + ".assets")/"manifest.json"
if manifest.exists() and json.loads(manifest.read_text(encoding="utf-8")).get("source_path") != str(src):
    raise SystemExit("output belongs to another source")
log={"input":str(src),"input_sha256_before":before,"mode":a.mode,"markitdown_called":False,"docling_called":False}
if a.mode in ("normal", "markitdown"):
    if not MARKITDOWN.exists(): raise SystemExit(f"MarkItDown not found: {MARKITDOWN}")
    log["markitdown_called"]=True
    cp=subprocess.run([str(MARKITDOWN),str(src),"-o",str(out)],text=True,capture_output=True)
    if manifest.exists():
        data=json.loads(manifest.read_text(encoding="utf-8"))
        data.update(parse_status="partial" if cp.returncode==0 else "failed",
                    parser="markitdown", index_path=None,
                    issues=["Text fallback: previous structured index is not current"])
        manifest.write_text(json.dumps(data,ensure_ascii=False,indent=2),encoding="utf-8")
else:
    if not DOCLING_PYTHON.exists(): raise SystemExit(f"Docling runtime not found: {DOCLING_PYTHON}")
    log["docling_called"]=True
    env=os.environ.copy(); env.update({"PYTHONUTF8":"1","PYTHONIOENCODING":"utf-8"})
    command=[str(DOCLING_PYTHON),str(DOCLING_HELPER),str(src),str(out)]
    if a.document_id: command += ["--document-id", a.document_id]
    if a.version: command += ["--version", a.version]
    cp=subprocess.run(command,text=True,capture_output=True,env=env,encoding="utf-8",errors="replace")
log.update({"returncode":cp.returncode,"stdout":cp.stdout[-2000:],"stderr":cp.stderr[-2000:],"input_sha256_after":sha256(src),"output":str(out),"output_nonempty":out.exists() and out.stat().st_size>0})
log["source_preserved"]=log["input_sha256_before"]==log["input_sha256_after"]
rendered=json.dumps(log,indent=2)
if a.log: Path(a.log).resolve().write_text(rendered,encoding="utf-8")
print(rendered); raise SystemExit(0 if cp.returncode==0 and log["output_nonempty"] and log["source_preserved"] else 1)
