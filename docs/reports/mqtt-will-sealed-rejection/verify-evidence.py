#!/usr/bin/env python3
"""Verify this slice's manifest and lossless archives from files or Git blobs."""

import argparse
import gzip
import hashlib
import json
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
source = parser.add_mutually_exclusive_group()
source.add_argument("--index", action="store_true")
source.add_argument("--git-ref")
args = parser.parse_args()
report = Path(__file__).resolve().parent
repo = report.parents[2]
prefix = report.relative_to(repo).as_posix()


def read(path):
    relative = Path(path)
    if relative.is_absolute() or ".." in relative.parts:
        raise ValueError("invalid manifest path")
    if args.index or args.git_ref:
        ref = "" if args.index else args.git_ref
        return subprocess.check_output(
            ["git", "cat-file", "blob", ref + ":" + path], cwd=repo
        )
    return (repo / relative).read_bytes()


manifest = json.loads(read(prefix + "/manifest.json"))
seen = set()
for item in manifest["files"]:
    path = item["path"]
    if path in seen:
        raise ValueError("duplicate manifest path")
    seen.add(path)
    data = read(path)
    assert len(data) == item["bytes"], path
    assert hashlib.sha256(data).hexdigest() == item["sha256"], path

archives = json.loads(read(prefix + "/raw-archives.json"))["files"]
for item in archives:
    data = read(prefix + "/" + item["archive_path"])
    assert len(data) == item["archive_bytes"], item["archive_path"]
    assert hashlib.sha256(data).hexdigest() == item["archive_sha256"]
    original = gzip.decompress(data)
    assert len(original) == item["original_bytes"], item["original_path"]
    assert hashlib.sha256(original).hexdigest() == item["original_sha256"]

print(f"verified {len(seen)} manifest files and {len(archives)} lossless archives")
