#!/usr/bin/env python3
"""Verify merge acceptance receipts and their retained source/artifact identity."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", action="store_true")
    options = parser.parse_args()
    report = Path(__file__).resolve().parent
    root = report.parents[2]
    results = json.loads((report / "validation-results.json").read_text())
    assert results["status"] == "passed", "merge validation incomplete"
    for artifact in results["artifacts"]:
        path = (report / artifact["path"]).resolve()
        assert path.is_relative_to(report), "artifact escaped report"
        assert sha(path) == artifact["sha256"], str(path)
    expected = {f"mqtt-{kind}-{nodes}.json" for kind in ("interop", "will") for nodes in (1, 3)}
    expected |= {f"aggregate-storage-ingress-{nodes}.json" for nodes in (1, 3)}
    expected |= {"mqtt-storage-partition-accepted.json", "mqtt-storage-partition-unknown.json"}
    cases = {p.name: json.loads(p.read_text()) for p in (report / "cases").glob("*.json")}
    assert set(cases) == expected, "missing or extra final receipt"
    for name, receipt in cases.items():
        assert receipt["passed"] is True and receipt["hash_slots"] == 256, name
    for nodes in (1, 3):
        will = cases[f"mqtt-will-{nodes}.json"]
        assert will["abnormal_close_published"] and will["normal_close_cancelled"]
        capacity = cases[f"aggregate-storage-ingress-{nodes}.json"]
        assert capacity["independent_acks_and_retirement"] and capacity["http_denied"]
        assert capacity["rejected_producer_reconnected"]
        assert capacity["shared_message_id"] != capacity["will_reopened_message_id"]
    for name in ("mqtt-storage-partition-accepted.json", "mqtt-storage-partition-unknown.json"):
        receipt = cases[name]
        assert receipt["last_phase"] == "complete" and receipt["nodes"] == 3
        assert receipt["resubscriptions"] == 0 and receipt["surviving_slot_quorum"]
        assert receipt["reserved_after_retirement"] == receipt["final_reserved"] == [0, 0, 0]
        for direction in ("1->3", "2->3", "3->1", "3->2"):
            link = receipt["partition_links"][direction]
            assert link["forwarded"] > 0 and link["closed"] + link["refused"] > 0
    accepted = cases["mqtt-storage-partition-accepted.json"]
    assert accepted["partition_duration_ms"] >= 35000 and accepted["persistent_replay_preserved"]
    unknown = cases["mqtt-storage-partition-unknown.json"]
    assert unknown["physical_commit_witness_before_heal"] and unknown["charged_commits_before_heal"] == 1
    assert unknown["retained_unknown_observation_ms"] >= 10000 and unknown["physical_cut_hits"] == 1
    assert unknown["reserved_unknown"][2] > unknown["reserved_before_unknown"][2]
    assert unknown["unknown_source_quiet_before_retry"]
    frozen = json.loads((report / "frozen-context.json").read_text())
    for label, entries in frozen["source_context"].items():
        revision = frozen["source_revisions"][label]
        for item in entries:
            data = subprocess.check_output(["git", "show", revision + ":" + item["path"]], cwd=root)
            assert hashlib.sha256(data).hexdigest() == item["sha256"], item["path"]
    build = json.loads((report / "build-manifest.json").read_text())
    subprocess.check_call(["git", "merge-base", "--is-ancestor", frozen["source_revisions"]["main"], build["source_revision"]], cwd=root)
    if options.source:
        for path, digest in build["source_files"].items():
            assert sha(root / path) == digest, path
    print("PASS: eight final receipts, retained artifacts, both frozen parents" + (" and candidate source" if options.source else ""))


if __name__ == "__main__":
    main()
