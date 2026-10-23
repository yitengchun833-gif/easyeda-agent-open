#!/usr/bin/env python3
"""Offline checks for the consolidated entry points; does not contact EDA or modify sources."""
import ast
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
from urllib.parse import unquote

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--changed', nargs='+', type=Path,
                        help='Only check selected local Markdown/Python files; leave external baselines alone')
    args = parser.parse_args()
    selected = None
    if args.changed:
        selected = [(ROOT / p).resolve() for p in args.changed]
        if any(not p.is_relative_to(ROOT) or not p.is_file() or p.suffix not in ('.md', '.py') for p in selected):
            parser.error('--changed requires existing Markdown/Python files inside this workspace')
        selected = list(dict.fromkeys(selected))
    errors = []
    if selected is None:
        manifest = json.loads((ROOT / '.codex-plugin/plugin.json').read_text(encoding='utf-8'))
        if not (ROOT / manifest['skills']).is_dir():
            errors.append('Plugin skills entry is missing')
    skills = list((ROOT / 'skills').glob('*/SKILL.md')) if selected is None else [p for p in selected if p.name == 'SKILL.md']
    names = []
    for skill in skills:
        match = re.search(r'^name:\s*([\w-]+)', skill.read_text(encoding='utf-8'), re.M)
        if not match:
            errors.append(f'Missing skill name: {skill.relative_to(ROOT)}')
        else:
            names.append(match[1])
    if len(set(names)) != len(names):
        errors.append('Duplicate bundled skill names')
    # Check the integration surface and all top-level skill entry links; upstream
    # topic catalog has its own validator. Code snippets are not Markdown links.
    pages = [ROOT / 'README.md', ROOT / 'AGENTS.md', *skills,
             ROOT / 'skills/easyeda-agent/references/schematic-replication.md',
             *(ROOT / 'docs').glob('*.md'),
             *(ROOT / 'skills/evidence-to-delivery/references').glob('*.md')]
    if selected is not None:
        pages = [p for p in selected if p.suffix == '.md']
    links = 0
    for page in pages:
        text = re.sub(r'```.*?```', '', page.read_text(encoding='utf-8'), flags=re.S)
        for target in re.findall(r'\[[^\]\n]*\]\(([^)\n]+)\)', text):
            target = target.split('#', 1)[0].split(' "', 1)[0].strip('<>')
            if not target or re.match(r'^[a-zA-Z][\w+.-]*:', target) or '${' in target:
                continue
            links += 1
            if not (page.parent / unquote(target)).exists():
                errors.append(f'Broken link: {page.relative_to(ROOT)} -> {target}')
    python_files = [*(ROOT / '.codex/hooks').glob('*.py'),
                 ROOT / 'skills/easyeda-agent/scripts/schematic_preflight.py',
                 ROOT / 'skills/easyeda-agent/scripts/test_schematic_preflight.py',
                 *(ROOT / 'skills/evidence-to-delivery/scripts').glob('*.py'),
                 *(ROOT / 'skills/document-intake/scripts').glob('*.py')]
    if selected is not None:
        python_files = [p for p in selected if p.suffix == '.py']
    for path in python_files:
        ast.parse(path.read_text(encoding='utf-8-sig'), filename=str(path))
    if selected is None and (ROOT / 'scripts').exists():
        errors.append('Root scripts duplicates canonical skill scripts')
    baseline = ROOT / '.local/source-baseline.json'
    original_count = None
    if selected is None and baseline.exists():
        originals = json.loads(baseline.read_text(encoding='utf-8'))
        original_count = len(originals)
        for source, digest in originals.items():
            path = Path(source)
            if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
                errors.append(f'Original source changed: {source}')
    report = {'passed': not errors, 'skills': len(skills), 'local_links_checked': links,
              'original_files_checked': original_count, 'errors': errors,
              'scope': ('selected Markdown links/skill names and Python syntax; external baselines not checked'
                        if selected is not None else 'structure, Python syntax and optional original-source hashes; no EDA or model calls')}
    print(json.dumps(report, ensure_ascii=False, indent=2))
    return int(bool(errors))


if __name__ == '__main__':
    sys.exit(main())
