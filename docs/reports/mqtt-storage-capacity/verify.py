#!/usr/bin/env python3
"""Verify retained acceptance artifacts; --source also checks this checkout."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def sha(data):
    return hashlib.sha256(data).hexdigest()


def source_digest(root, paths):
    h = hashlib.sha256()
    for path in sorted(set(paths)):
        h.update(path.encode() + b"\0" + sha((root / path).read_bytes()).encode() + b"\n")
    return h.hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", action="store_true")
    args = parser.parse_args()
    report = Path(__file__).resolve().parent
    manifest = json.loads((report / "validation-results.json").read_text())
    for item in manifest["artifacts"]:
        assert sha((report / item["path"]).read_bytes()) == item["sha256"], item["path"]
    for path in (report / "cases").glob("*.json"):
        case = json.loads(path.read_text())
        assert case["passed"] is True, path.name
        assert case["hash_slots"] == 256, path.name
    if args.source:
        root = report.parents[2]
        context = json.loads((report / "frozen-context.json").read_text())
        for item in context["files"]:
            raw = subprocess.check_output(["git", "show", context["source_revision"] + ":" + item["path"]], cwd=root)
            assert sha(raw) == item["sha256"], item["path"]
        for item in context["introduced_rules"]:
            assert sha((root / item["path"]).read_bytes()) == item["sha256"], item["path"]
        source = json.loads((report / "validation-source.json").read_text())
        for item in source["changed_go_files"] + source["module_files"]:
            assert sha((root / item["path"]).read_bytes()) == item["sha256"], item["path"]
        files = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=root).decode().split("\0")
        production = [p for p in files if p.endswith(".go") and p.split("/")[0] in ("cmd", "internal", "pkg")]
        e2e = [p for p in files if p.startswith("test/e2e/") and p.endswith(".go")]
        assert source_digest(root, production) == source["production_go_sha256"]
        assert source_digest(root, e2e) == source["e2e_go_sha256"]
    print("PASS: retained artifacts" + (", frozen context and candidate source" if args.source else ""))


if __name__ == "__main__":
    main()
