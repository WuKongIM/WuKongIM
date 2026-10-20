"""Read saved evidence and Git objects; emit an independent provenance review."""
import datetime
import hashlib
import json
from pathlib import Path
import subprocess
import zlib

BASE = Path("/tmp/wukongim-performance-followup-20261020")
NEW = BASE / "reverse"
OLD = Path("/tmp/wukongim-v3-rc-20261020/qps-comparison")
REPO = Path("/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM")
CANDIDATE = "b469c6e5e2b4ff65611a8fe3389fb5034de3f152"
BETA = "4d44b39dda6fdaa7dafb637ca6cfb718e9530f77"


def read(path):
    return json.loads(path.read_text())


def sha(data):
    return hashlib.sha256(data).hexdigest()


def git_bytes(path):
    return subprocess.check_output(
        ["git", "show", f"{CANDIDATE}:{path}"], cwd=REPO
    )


def dt(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


plan = read(BASE / "reverse-order-plan.json")
corrected = read(BASE / "reverse-order-plan-corrected.json")
context = read(BASE / "source-context.json")
run = read(NEW / "run.json")
preflight = read(BASE / "reverse-first-run.json")
checks = []


def check(name, condition, details=None):
    checks.append({"name": name, "pass": bool(condition), "details": details})


check("exact_workflow_head", run["headSha"] == CANDIDATE, run["headSha"])
check("source_context_revision", context["source_revision"] == CANDIDATE)
frozen = []
for source, mapping in [
    ("source-context", context["frozen_files"]),
    ("corrected-plan", corrected["frozen_context_sha256"]),
]:
    for path, digest in mapping.items():
        actual = sha(git_bytes(path))
        frozen.append({"source": source, "path": path, "expected": digest,
                       "actual": actual, "match": digest == actual})
check("frozen_source_digests", all(f["match"] for f in frozen),
      {"files_checked": len(frozen)})

reports = {}
records = {}
for label, folder in [
    ("old_candidate_second_root", OLD),
    ("old_beta_first_comparison", OLD / "comparison"),
    ("new_candidate_first_comparison", NEW / "comparison"),
    ("new_beta_second_root", NEW),
]:
    report = read(folder / "report.json")
    reports[label] = report
    build = (folder / "build.txt").read_bytes().splitlines(keepends=True)
    digest, binary_path = (folder / "binary.sha256").read_text().split()
    settings = {}
    for line in build[1:]:
        text = line.decode().strip()
        if text.startswith("build\t"):
            key, sep, value = text[6:].partition("=")
            settings[key] = value if sep else ""
    expected_product = BETA if "beta" in label else CANDIDATE
    records[label] = {
        "source_folder": str(folder),
        "product_sha": report["product_sha"],
        "binary_sha256": digest,
        "binary_path_from_workflow_summary": binary_path,
        "build_first_line": build[0].decode().rstrip(),
        "build_metadata_normalized_sha256": sha(b"".join(build[1:])),
        "build_settings": settings,
        "harness_sha": report["harness_sha"],
        "harness_dirty": report["harness_dirty"],
        "profile_sha256": report["profile_sha256"],
        "driver_gomaxprocs": report["driver_gomaxprocs"],
        "node_gomaxprocs": report["node_gomaxprocs"],
        "config": report["config"],
        "windows": [
            {"start": w["start"], "end": w["end"],
             "runtime_loads": w["runtime_loads"],
             "membership_writes": w["membership_writes"],
             "phases": [
                 {"case": p["case"], "duration_seconds": p["duration_seconds"],
                  "driver_workers": p["driver_workers"], "nodes": p["nodes"]}
                 for p in w["phases"]
             ]}
            for w in report["windows"]
        ],
        "profile_start": report["profile_window"]["start"],
        "complete": report["complete"],
    }
    check(label + ":identity", report["product_sha"] == expected_product
          and settings["vcs.revision"] == expected_product
          and settings["vcs.modified"] == "false"
          and report["harness_sha"] == CANDIDATE
          and report["harness_dirty"] is False
          and report["binary_sha256"] == digest
          and build[0].decode().rstrip().endswith(": go1.25.11"))
    check(label + ":three_fixed_windows", len(report["windows"]) == 3
          and all(len(w["phases"]) == 2 and
                  all(p["duration_seconds"] == 60
                      and p["driver_workers"] == 8
                      and p["nodes"] == 3 for p in w["phases"])
                  for w in report["windows"])
          and report["complete"] is True)
    check(label + ":profiles_outside_windows",
          dt(report["profile_window"]["start"]) >
          max(dt(w["end"]) for w in report["windows"]))

reference = reports["old_candidate_second_root"]
profile_digest = sha(git_bytes("test/e2e/message/conversation_qps/profile.json"))
check("same_profile_and_config_all_four",
      all(r["profile_sha256"] == profile_digest
          and r["config"] == reference["config"]
          and r["driver_gomaxprocs"] == 4
          and r["node_gomaxprocs"] == 2 for r in reports.values())
      and sha((OLD / "profile.json").read_bytes()) == profile_digest,
      {"profile_sha256": profile_digest})
for product, old_label, new_label in [
    ("beta", "old_beta_first_comparison", "new_beta_second_root"),
    ("candidate", "old_candidate_second_root", "new_candidate_first_comparison"),
]:
    check(product + ":same_normalized_build_metadata",
          records[old_label]["build_metadata_normalized_sha256"] ==
          records[new_label]["build_metadata_normalized_sha256"])

new_first = reports["new_candidate_first_comparison"]
new_second = reports["new_beta_second_root"]
check("candidate_precedes_beta", max(dt(w["end"]) for w in new_first["windows"])
      < min(dt(w["start"]) for w in new_second["windows"]))
check("original_plan_predeclared",
      dt(plan["declared_at"]) < dt(preflight["jobs"][0]["startedAt"]))
check("corrected_plan_predeclared",
      dt(corrected["declared_at"]) < dt(run["jobs"][0]["startedAt"]))
changes = {key: {"old": plan.get(key), "new": corrected.get(key)}
           for key in sorted(set(plan) | set(corrected))
           if plan.get(key) != corrected.get(key)}
allowed_changes = {
    "declared_at", "first_expected_binary_sha256",
    "first_prior_role_binary_sha256", "second_expected_binary_sha256",
    "previous_preflight_run", "build_identity_correction",
}
check("plan_only_changes_identity_correction_and_timestamp",
      set(changes) == allowed_changes)
check("plan_invariants_hypotheses_unchanged",
      all(plan[key] == corrected[key]
          for key in ["invariants", "hypotheses", "limits", "frozen_context_sha256",
                      "bounded_runs", "run_release_matrix", "first_product_sha",
                      "second_product_sha", "expected_harness_sha"]))
check("corrected_beta_digest_matches_preflight_and_success",
      corrected["second_expected_binary_sha256"] ==
      (BASE / "reverse-first/binary.sha256").read_text().split()[0] ==
      records["new_beta_second_root"]["binary_sha256"])
preflight_steps = {s["name"]: s for s in preflight["jobs"][0]["steps"]}
measurement_steps = [
    "Collect comparison windows with the identical harness and preset",
    "Collect three fixed windows then separate profiles",
]
check("preflight_stopped_before_measurement",
      preflight_steps["Build product before adding diagnostic checkout"]["conclusion"] == "failure"
      and all(preflight_steps[s]["conclusion"] == "skipped" for s in measurement_steps))
check("release_matrix_skipped", next(
    s for s in run["jobs"][0]["steps"]
    if s["name"] == "Validate the complete unchanged release matrix without publication"
)["conclusion"] == "skipped")

downloads = {}
for label, root in [("old", OLD), ("new", NEW)]:
    manifest = read(root / "selective-download.json")
    files = []
    for entry in manifest["files"]:
        data = (root / entry["path"]).read_bytes()
        files.append({"path": entry["path"], "sha256": sha(data),
                      "size_matches": len(data) == entry["bytes"],
                      "zip_crc32_matches": f"{zlib.crc32(data):08x}" == entry["zip_crc32"]})
    downloads[label] = {
        "run_id": manifest["run_id"],
        "binary_downloaded": manifest["binary_downloaded"],
        "archive_digest_recomputed": manifest["archive_digest_recomputed"],
        "files": files,
    }
    check(label + ":downloaded_file_sizes_and_crc",
          all(f["size_matches"] and f["zip_crc32_matches"] for f in files))

hosts = {}
for label, root in [("old", OLD), ("new", NEW)]:
    hosts[label] = {v["field"]: v["data"] for v in read(root / "host.json")["lscpu"]
                    if v["field"] in ["Architecture:", "CPU(s):", "Model name:",
                                      "Hypervisor vendor:"]}

result = {
    "schema": "wukongim/reverse-provenance-review/v1",
    "reviewed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "new_run": 39407349500, "old_run": 39369947381, "preflight_run": 39406775641,
    "verdict": "consistent_workflow_evidence_with_limits"
               if all(c["pass"] for c in checks) else "inconsistency_found",
    "checks": checks, "records": records, "plan_changes": changes,
    "hosts": hosts, "frozen_context_checks": frozen, "downloads": downloads,
    "material_wording_corrections": [
        "The plan correction also unset the candidate's prior-role expected digest; it did not only change beta's digest.",
        "Different full binary hashes are consistent with changed build checkout roles and non-trimpath builds, not independently proven to be caused only by paths.",
        "Both runs are internally same-host comparisons; the old AMD and new Intel hosts are not the same hardware.",
    ],
    "limits": [
        "No new product binary or complete artifact archive was downloaded or independently rehashed locally.",
        "Product binary hashes are workflow evidence, corroborated by the fixture's own runtime binary hash in report.json.",
        "Normalized go version -m equality does not establish identical executable content.",
        "Three diagnostic windows and separate profiles do not qualify the unchanged full release gate or the migration-repaired candidate.",
        "No workflow dispatch, repository modification, product test or load was performed by this review.",
    ],
}
(BASE / "reverse-provenance-review.json").write_text(
    json.dumps(result, ensure_ascii=False, indent=2) + "\n"
)
print(json.dumps({"verdict": result["verdict"],
                  "checks": len(checks),
                  "failures": [c for c in checks if not c["pass"]],
                  "plan_changed_keys": sorted(changes),
                  "hosts": hosts}, ensure_ascii=False, indent=2))
