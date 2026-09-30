#!/usr/bin/env python3
"""Publish verified local assets through a resumable GitHub draft release."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile
from typing import Optional


def run(*args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(args, capture_output=True, text=True)
    if check and result.returncode:
        raise ValueError(f"{' '.join(args)} failed: {result.stderr.strip()}")
    return result


def release_state(tag: str) -> Optional[dict]:
    result = run("gh", "release", "view", tag, "--json", "isDraft,assets", check=False)
    if result.returncode:
        return None
    state = json.loads(result.stdout)
    if not isinstance(state, dict) or not isinstance(state.get("isDraft"), bool):
        raise ValueError("GitHub returned an invalid release state")
    return state


def checked_assets(tag: str, dist: Path) -> tuple[list[Path], dict[str, int], dict[str, str]]:
    checksums = dist / "checksums.txt"
    if not checksums.is_file() or checksums.is_symlink():
        raise ValueError("checksums.txt is missing or symlinked")
    paths = []
    sizes = {}
    digests = {}
    for line in checksums.read_text(encoding="utf-8").splitlines():
        digest, name = line.split()
        if (not re.fullmatch(r"[0-9a-f]{64}", digest) or Path(name).name != name
                or name in sizes):
            raise ValueError(f"invalid checksums.txt entry: {line}")
        path = dist / name
        if not path.is_file() or path.is_symlink() or hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise ValueError(f"release asset missing or changed after build: {path}")
        paths.append(path)
        sizes[name] = path.stat().st_size
        digests[name] = digest
    match = re.fullmatch(r"v(\d+)\.(\d+)\.0", tag)
    if match and int(match.group(2)) > 0 and (int(match.group(1)), int(match.group(2))) >= (1, 6):
        if "test-evidence.zip" not in sizes:
            raise ValueError("minor release is missing test-evidence.zip in checksums.txt")
    paths.append(checksums)
    sizes["checksums.txt"] = checksums.stat().st_size
    digests["checksums.txt"] = hashlib.sha256(checksums.read_bytes()).hexdigest()
    return paths, sizes, digests


def check_remote_assets(state: dict, sizes: dict[str, int], digests: dict[str, str]) -> list[str]:
    assets = state.get("assets")
    if not isinstance(assets, list) or {item.get("name") for item in assets} != set(sizes):
        raise ValueError("GitHub draft is missing or has unexpected release assets")
    missing_digests = []
    for item in assets:
        name = item["name"]
        if item.get("size") != sizes[name]:
            raise ValueError(f"GitHub asset size differs: {name}")
        digest = item.get("digest")
        if digest is None or digest == "":
            missing_digests.append(name)
        elif digest != f"sha256:{digests[name]}":
            raise ValueError(f"GitHub asset digest differs: {name}")
    return missing_digests


def verify_downloaded_assets(tag: str, names: list[str], digests: dict[str, str]) -> None:
    if not names:
        return
    with tempfile.TemporaryDirectory(prefix="easyeda-release-verify-") as temporary:
        directory = Path(temporary)
        for name in names:
            run("gh", "release", "download", tag, "--pattern", name, "--dir", str(directory))
            path = directory / name
            if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != digests[name]:
                raise ValueError(f"downloaded GitHub asset SHA256 differs: {name}")


def ensure_tag(tag: str) -> None:
    head = run("git", "rev-parse", "HEAD").stdout.strip()
    remote = run("git", "ls-remote", "--tags", "origin", f"refs/tags/{tag}").stdout.strip()
    local = run("git", "rev-parse", "--verify", f"refs/tags/{tag}", check=False)
    if local.returncode:
        if remote:
            raise ValueError(f"remote tag {tag} exists but local annotated tag is missing; fetch and inspect it")
        run("git", "tag", "-a", tag, "-m", f"Release {tag}")
    if run("git", "cat-file", "-t", f"refs/tags/{tag}").stdout.strip() != "tag":
        raise ValueError(f"{tag} must be an annotated tag")
    if run("git", "rev-parse", f"refs/tags/{tag}^{{commit}}").stdout.strip() != head:
        raise ValueError(f"{tag} does not point to current HEAD")
    local_object = run("git", "rev-parse", f"refs/tags/{tag}").stdout.strip()
    if remote:
        if remote.split()[0] != local_object:
            raise ValueError(f"remote tag {tag} differs from local tag")
    else:
        run("git", "push", "origin", f"refs/tags/{tag}")


def publish(tag: str, dist: Path) -> None:
    paths, sizes, digests = checked_assets(tag, dist)
    if not (dist / "release-notes.md").is_file():
        raise ValueError("release-notes.md is missing")
    state = release_state(tag)
    if state and not state["isDraft"]:
        raise ValueError(f"{tag} is already published; release assets cannot be replaced")
    ensure_tag(tag)
    if state is None:
        run("gh", "release", "create", tag, *map(str, paths), "--draft", "--verify-tag",
            "--title", f"easyeda-agent {tag}", "--notes-file", str(dist / "release-notes.md"))
    else:
        run("gh", "release", "upload", tag, *map(str, paths), "--clobber")
        run("gh", "release", "edit", tag, "--title", f"easyeda-agent {tag}",
            "--notes-file", str(dist / "release-notes.md"))
    state = release_state(tag)
    if not state or not state["isDraft"]:
        raise ValueError("GitHub release must remain a draft until asset verification passes")
    verify_downloaded_assets(tag, check_remote_assets(state, sizes, digests), digests)
    run("gh", "release", "edit", tag, "--draft=false")
    state = release_state(tag)
    if not state or state["isDraft"]:
        raise ValueError("GitHub release publication was not confirmed")
    verify_downloaded_assets(tag, check_remote_assets(state, sizes, digests), digests)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tag")
    parser.add_argument("dist", type=Path)
    args = parser.parse_args()
    try:
        publish(args.tag, args.dist)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.exit(1, f"release publication failed: {error}\n")


if __name__ == "__main__":
    main()
