"""Verify retained bytes and recompute four explicitly named offline analyses."""

import gzip
import hashlib
import json
import pathlib
import subprocess
import sys
import tempfile


HERE = pathlib.Path(__file__).resolve().parent
OLD = HERE.parent / "v3-rc-20261020"
ORIGINAL_OLD = "/tmp/wukongim-v3-rc-20261020"
ORIGINAL_NEW = "/tmp/wukongim-performance-followup-20261020"


def sha256(raw):
    return hashlib.sha256(raw).hexdigest()


def read_retained(path):
    if path.exists():
        return path.read_bytes()
    return gzip.decompress(path.with_name(path.name + ".gz").read_bytes())


manifest = json.loads((HERE / "manifest.json").read_text())
for item in manifest["files"]:
    relative = pathlib.PurePosixPath(item["stored_path"])
    assert not relative.is_absolute() and ".." not in relative.parts
    stored = (HERE / relative).read_bytes()
    assert len(stored) == item["stored_bytes"], relative
    assert sha256(stored) == item["stored_sha256"], relative
    raw = gzip.decompress(stored) if item["encoding"] == "gzip" else stored
    assert len(raw) == item["original_bytes"], relative
    assert sha256(raw) == item["original_sha256"], relative

old_reports = (
    "qps/report.json",
    "qps-comparison/report.json",
    "qps-comparison/comparison/report.json",
)
new_reports = ("reverse/report.json", "reverse/comparison/report.json")
analyses = (
    ("cost-analyze.py", "cost-data.json"),
    ("latency-analyze.py", "latency-data.json"),
    ("reverse-cost-analyze.py", "reverse-cost-data.json"),
    ("reverse-latency-analyze.py", "reverse-latency-data.json"),
)

with tempfile.TemporaryDirectory(prefix="wukongim-perf-recompute-") as temporary:
    base = pathlib.Path(temporary)
    old, new = base / "old", base / "new"
    for source, destination, reports in (
        (OLD, old, old_reports),
        (HERE, new, new_reports),
    ):
        for name in reports:
            target = destination / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(read_retained(source / name))
    for script, output in analyses:
        source = (HERE / script).read_text()
        source = source.replace(ORIGINAL_OLD, str(old)).replace(ORIGINAL_NEW, str(new))
        executable = new / script
        executable.write_text(source)
        result = subprocess.run(
            [sys.executable, str(executable)],
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
        if result.returncode:
            raise RuntimeError(f"{script} failed: {result.stderr}")
        actual = (new / output).read_text()
        # Only ephemeral evidence paths change; all numeric results and input
        # digests must match the retained analysis exactly.
        actual = actual.replace(str(old), ORIGINAL_OLD).replace(str(new), ORIGINAL_NEW)
        assert json.loads(actual) == json.loads(read_retained(HERE / output)), output

print(json.dumps({
    "verified_retained_files": len(manifest["files"]),
    "exact_recomputed_analyses": [output for _, output in analyses],
    "network_or_product_processes_started": False,
}))
