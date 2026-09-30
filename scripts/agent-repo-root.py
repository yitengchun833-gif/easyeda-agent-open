#!/usr/bin/env python3
"""Locate this checkout through a repository Skill's installed symlink."""

from pathlib import Path
import sys


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    manifest = root / "go.mod"
    if not (root / "AGENTS.md").is_file() or not manifest.is_file():
        sys.exit("error: Skill is not linked to an easyeda-agent checkout")
    if "module github.com/zhoushoujianwork/easyeda-agent" not in manifest.read_text():
        sys.exit("error: linked checkout is not easyeda-agent")
    print(root)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
