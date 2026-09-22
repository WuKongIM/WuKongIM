#!/usr/bin/env python3
"""Build a common probe against one clean, committed transport source tree."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--os", choices=["linux", "darwin"], default="linux")
    parser.add_argument("--arch", choices=["amd64", "arm64"], default="amd64")
    args = parser.parse_args()
    root, output = args.source.resolve(), args.output.resolve()
    git = lambda *a: subprocess.check_output(["git", *a], cwd=root, text=True).strip()
    if git("status", "--porcelain", "--untracked-files=all"):
        parser.error("source worktree must be clean; commit the candidate and tool first")
    if output.exists() or output.with_suffix(output.suffix + ".json").exists():
        parser.error("refuse to overwrite an existing binary or build manifest")
    if output.is_relative_to(root):
        parser.error("build artifacts must be outside the source worktree")
    source = Path(__file__).resolve().parent / "probe" / "main.go"
    raw = source.read_bytes()
    revision = git("rev-parse", "HEAD")
    output.parent.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOOS=args.os, GOARCH=args.arch, CGO_ENABLED="0", GOWORK="off", GOTOOLCHAIN="go1.25.11")
    # The same probe source is copied into each target, independent of its version.
    # Keep the import path identical across builds; a random package path would
    # add an unrelated binary-layout variable. Exclusive creation prevents reuse.
    directory = root / "scripts" / ".rpc-probe-build"
    directory.mkdir(mode=0o700)
    try:
        (directory / "main.go").write_bytes(raw)
        command = ["go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-X main.sourceRevision=" + revision,
                   "-o", str(output), "./" + str(directory.relative_to(root))]
        try:
            subprocess.run(command, cwd=root, env=env, check=True)
        except BaseException:
            output.unlink(missing_ok=True)
            raise
    finally:
        shutil.rmtree(directory)
    manifest = {"schema": "wkrpc-probe-build/v1", "source_revision": revision,
                "probe_source_sha256": hashlib.sha256(raw).hexdigest(),
                "binary_sha256": hashlib.sha256(output.read_bytes()).hexdigest(),
                "binary": str(output), "goos": args.os, "goarch": args.arch,
                "toolchain": "go1.25.11", "command": command, "cgo_enabled": False}
    output.with_suffix(output.suffix + ".json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps(manifest, indent=2))


if __name__ == "__main__":
    main()
