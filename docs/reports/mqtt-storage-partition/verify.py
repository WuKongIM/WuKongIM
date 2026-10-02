#!/usr/bin/env python3
"""Verify retained acceptance receipts and their exact source/artifact identity."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    args = argparse.ArgumentParser()
    args.add_argument("--source", action="store_true")
    options = args.parse_args()
    report = Path(__file__).resolve().parent
    root = report.parents[2]
    manifest = json.loads((report / "validation-results.json").read_text())
    assert manifest["status"] == "passed", "acceptance incomplete"
    for artifact in manifest["artifacts"]:
        path = (report / artifact["path"]).resolve()
        assert path.is_relative_to(report), "artifact escaped report"
        assert sha(path) == artifact["sha256"], str(path)
    expected = {"mqtt-storage-partition-accepted.json", "mqtt-storage-partition-unknown.json"}
    cases = {p.name: json.loads(p.read_text()) for p in (report / "cases").glob("*.json")}
    assert set(cases) == expected, "missing/extra final receipt"
    for name, receipt in cases.items():
        assert receipt["passed"] is True and receipt["last_phase"] == "complete", name
        assert receipt["nodes"] == 3 and receipt["hash_slots"] == 256, name
        assert receipt["resubscriptions"] == 0 and receipt["surviving_slot_quorum"] is True, name
        assert receipt["reserved_after_retirement"] == [0, 0, 0], name
        assert receipt["final_reserved"] == [0, 0, 0], name
        assert receipt["accepted"]["message_id"] != receipt["reopened"]["message_id"], name
        for direction in ("1->3", "2->3", "3->1", "3->2"):
            link = receipt["partition_links"][direction]
            assert link["forwarded"] > 0 and link["closed"] + link["refused"] > 0, name
        local = receipt["partition_links"].get("3->3", {})
        assert local.get("closed", 0) + local.get("refused", 0) == 0, name
    accepted = cases["mqtt-storage-partition-accepted.json"]
    assert accepted["partition_duration_ms"] >= 35000
    assert accepted["persistent_replay_preserved"] is True
    assert len(accepted["ingress_refusals"]) == 3
    unknown = cases["mqtt-storage-partition-unknown.json"]
    assert unknown["physical_commit_witness_before_heal"] is True
    assert unknown["charged_commits_before_heal"] == 1 and unknown["physical_cut_hits"] == 1
    assert unknown["retained_unknown_observation_ms"] >= 10000
    assert unknown["reserved_unknown"][2] > unknown["reserved_before_unknown"][2]
    assert unknown["unknown_source_quiet_before_retry"] is True
    frozen = json.loads((report / "frozen-context.json").read_text())
    for item in frozen["source_context"]:
        data = subprocess.check_output(["git", "show", frozen["base"] + ":" + item["path"]], cwd=root)
        assert hashlib.sha256(data).hexdigest() == item["sha256"], item["path"]
    for item in frozen["introduced_context"]:
        assert sha(root / item["path"]) == item["sha256"], item["path"]
    if options.source:
        source = json.loads((report / "validation-source.json").read_text())
        for item in source["changed_files"]:
            assert sha(root / item["path"]) == item["sha256"], item["path"]
        for name in ("go.mod", "go.sum"):
            assert sha(root / name) == source["module_files"][name], name
        files = sorted(p for directory in ("cmd", "internal", "pkg") for p in (root / directory).rglob("*.go"))
        entries = [{"path": str(p.relative_to(root)), "sha256": sha(p)} for p in files]
        aggregate = hashlib.sha256(json.dumps(entries, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        assert aggregate == source["production_go_sha256"], "product Go source changed"
    print("PASS: final receipts, retained artifacts, frozen context" + (" and source" if options.source else ""))


if __name__ == "__main__":
    main()
