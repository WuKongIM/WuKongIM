#!/usr/bin/env python3
"""Recompute historical causal bounds from archived raw reports, offline only."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path


def metric_delta(window, family, selected=None):
    selected = selected or {}
    total = 0
    for before, after in zip(window["metrics_before"], window["metrics_after"]):
        for key, value in after.items():
            name, _, suffix = key.partition("{")
            labels = dict(
                item.split("=", 1)
                for item in suffix.rstrip("}").split(",")
                if "=" in item
            )
            if name == family and all(labels.get(k) == v for k, v in selected.items()):
                total += value - before.get(key, 0)
    return total


def stage(window, scope, name):
    labels = {"scope": scope, "stage": name, "result": "ok"}
    family = "wukongim_conversation_read_stage_duration_seconds"
    count = metric_delta(window, family + "_count", labels)
    seconds = metric_delta(window, family + "_sum", labels)
    return {"count": count, "sum_seconds": seconds,
            "mean_ms": seconds * 1000 / count if count else None}


MODELS = {
    "slot message updates": (8, 24),
    "slot permission metadata batch": (8, 16),
    "slot runtime metadata": (0, 8),
    "channel committed reads": (0, 2),
    "channel conversation heads": (2, 2),
}


def analyze(path):
    archived = path.read_bytes()
    raw = gzip.decompress(archived)
    report = json.loads(raw)
    windows = []
    for i, window in enumerate(report["windows"]):
        phases = {p["case"]["endpoint"]: p for p in window["phases"]}
        list_count = phases["/conversation/list"]["completed"]
        sync_count = phases["/conversation/sync"]["completed"]
        checks = {}
        for service, (list_weight, sync_weight) in MODELS.items():
            observed = metric_delta(window, "wukongim_transport_rpc_client_total",
                                    {"service": service, "result": "ok"})
            predicted = list_weight * list_count + sync_weight * sync_count
            checks[service] = {
                "list_weight": list_weight, "sync_weight": sync_weight,
                "observed": observed, "mix_model": predicted,
                "residual": observed - predicted,
            }
        barrier = stage(window, "edit_slot", "barrier")
        checks["edit_slot barrier count"] = {
            "list_weight": 12, "sync_weight": 36,
            "observed": barrier["count"],
            "mix_model": 12 * list_count + 36 * sync_count,
            "residual": barrier["count"] - 12 * list_count - 36 * sync_count,
        }
        timing = {}
        for endpoint, phase in phases.items():
            timing[endpoint] = {
                key: phase[key] for key in (
                    "completed", "completed_in_window", "scheduled",
                    "driver_workers", "queue_capacity", "dropped", "errors",
                    "actual_qps", "driver_wait_p99_ms", "request_p99_ms",
                    "p50_ms", "p99_ms",
                )
            }
            timing[endpoint]["worker_occupancy_budget_ms_at_offered_rate"] = (
                phase["driver_workers"] / phase["case"]["offered_qps"] * 1000
            )
            timing[endpoint]["full_queue_service_estimate_ms_at_actual_rate"] = (
                phase["queue_capacity"] / phase["actual_qps"] * 1000
            )
        before, after = report["host_snapshots"][2 * i:2 * i + 2]
        resources = []
        for snapshot in (before, after):
            resources.append({
                "time": snapshot["time"],
                "actual_driver_membership": snapshot["/proc/self/cgroup"],
                "observed_cpu_stat_path": "/sys/fs/cgroup/cpu.stat",
                "observed_cpu_stat": snapshot["/sys/fs/cgroup/cpu.stat"],
                "observed_cpu_max_path": "/sys/fs/cgroup/cpu.max",
                "observed_cpu_max": snapshot["/sys/fs/cgroup/cpu.max"],
                "has_process_mountinfo": any("mountinfo" in k for k in snapshot),
                "has_resolved_service_controls": any(
                    "/system.slice/" in k for k in snapshot
                ),
            })
        stages = {
            scope + "/" + name: stage(window, scope, name)
            for scope, name in (
                ("list", "handler"), ("list", "response"),
                ("sync", "handler"), ("sync", "response"),
                ("persisted_heads", "metadata"), ("persisted_heads", "heads"),
                ("persisted_heads", "edit_overlay"),
                ("edit_slot", "barrier"), ("edit_slot", "storage"),
            )
        }
        windows.append({
            "window": i + 1, "start": window["start"], "end": window["end"],
            "list_completed": list_count, "sync_completed": sync_count,
            "timing": timing, "mix_model_checks": checks, "stages": stages,
            "server_cpu_seconds": window["cpu_seconds"],
            "server_allocated_bytes": window["allocated_bytes"],
            "runtime_loads": window["runtime_loads"],
            "membership_writes": window["membership_writes"],
            "gc_count": metric_delta(window, "go_gc_duration_seconds_count"),
            "gc_pause_sum_seconds": metric_delta(window, "go_gc_duration_seconds_sum"),
            "contains_storage_metrics": any(
                key.startswith("wukongim_storage_")
                for cut in window["metrics_before"] + window["metrics_after"]
                for key in cut
            ),
            "resource_cuts": resources,
        })
    return {
        "path": str(path),
        "archive_sha256": hashlib.sha256(archived).hexdigest(),
        "raw_sha256": hashlib.sha256(raw).hexdigest(),
        "product_sha": report["product_sha"],
        "harness_sha": report["harness_sha"],
        "binary_sha256": report["binary_sha256"],
        "windows": windows,
        "all_mix_residuals_exactly_zero": all(
            check["residual"] == 0
            for window in windows
            for check in window["mix_model_checks"].values()
        ),
        "profile_window": {
            "start": report["profile_window"]["start"],
            "end": report["profile_window"]["end"],
            "endpoint_outcomes": [
                {key: phase[key] for key in ("case", "dropped", "p99_ms", "errors")}
                for phase in report["profile_window"]["phases"]
            ],
        },
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    root = args.repo / "docs/reports/v3-rc-20261020"
    reports = {
        "original_b469_39368295521": root / "qps/report.json.gz",
        "matched_beta24_39369947381": root / "qps-comparison/comparison/report.json.gz",
        "matched_b469_39369947381": root / "qps-comparison/report.json.gz",
    }
    result = {
        "schema": "wukongim/historical-causal-boundary/v1",
        "scope": "Only reads archived raw report files; no product, network or workflow execution.",
        "notes": [
            "RPC weights are an exactly fitting completed-request mix model, not per-request tracing.",
            "Zero residual does not prove identical per-node routing, placement or CPU work.",
            "Root cgroup counters do not establish actual service or ancestor quota.",
            "Stages measure overlapping wall time with different populations, not additive CPU cost.",
            "Queue service estimates describe a full queue at observed throughput, not measured mean waits.",
        ],
        "reports": {name: analyze(path) for name, path in reports.items()},
    }
    encoded = json.dumps(result, indent=2) + "\n"
    if args.output:
        args.output.write_text(encoded)
        print(args.output)
    else:
        print(encoded, end="")


if __name__ == "__main__":
    main()
