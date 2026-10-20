"""Independent recomputation from original JSON receipts, never derived summaries."""
import datetime
import hashlib
import json
import math
import re
import statistics
from collections import defaultdict
from pathlib import Path

ROOT = Path("/tmp/wukongim-performance-followup-20261020")
OLD = Path("/tmp/wukongim-v3-rc-20261020/qps-comparison")
NEW = ROOT / "reverse"
CAND = "b469c6e5e2b4ff65611a8fe3389fb5034de3f152"
BASE = "4d44b39dda6fdaa7dafb637ca6cfb718e9530f77"
PROFILE = Path("/tmp/wukongim-v3-rc-20261020/qps/profile.json")
profile = json.loads(PROFILE.read_text())
profile_sha = hashlib.sha256(PROFILE.read_bytes()).hexdigest()


def instant(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def deltas(window):
    merged = defaultdict(float)
    for before, after in zip(window["metrics_before"], window["metrics_after"]):
        for key, value in after.items():
            parts = key.split("{", 1)
            label = dict(p.split("=", 1) for p in parts[1].rstrip("}").split(",") if "=" in p)
            for ignored in ("node_id", "node_name", "target_node"):
                label.pop(ignored, None)
            merged[(parts[0], tuple(sorted(label.items())))] += value - before.get(key, 0)
    return merged


def totals(merged, metric):
    return {",".join(f"{k}={v}" for k, v in labels): value
            for (name, labels), value in merged.items() if name == metric}


def means(merged, metric):
    counts, sums = totals(merged, metric+"_count"), totals(merged, metric+"_sum")
    return {label: {"count": count, "sum": sums[label], "mean": sums[label]/count}
            for label, count in counts.items() if count > 0}


def counter(raw, key):
    return float(next(line.split()[1] for line in raw.splitlines() if line.split()[0] == key))


def ticks(raw):
    f = raw.rsplit(")", 1)[1].split()
    return int(f[11]) + int(f[12])


def host(report, i):
    a, b = report["host_snapshots"][i*2:i*2+2]
    elapsed = (instant(b["time"])-instant(a["time"])).total_seconds()
    ca, cb = a["/sys/fs/cgroup/cpu.stat"], b["/sys/fs/cgroup/cpu.stat"]
    usage = (counter(cb, "usage_usec")-counter(ca, "usage_usec"))/1e6
    stats = [[int(x) for x in s["/proc/stat"].splitlines()[0].split()[1:9]] for s in (a, b)]
    sd = [y-x for x, y in zip(*stats)]
    psi = (int(b["/proc/pressure/cpu"].splitlines()[0].split("total=")[1]) -
           int(a["/proc/pressure/cpu"].splitlines()[0].split("total=")[1]))
    return {
        "bracket_seconds": elapsed,
        "cgroup_usage_seconds": usage,
        "cgroup_visible_four_cpu_utilization": usage/elapsed/4,
        "host_nonidle_fraction": (sum(sd)-sd[3]-sd[4])/sum(sd),
        "host_iowait_fraction": sd[4]/sum(sd),
        "host_steal_fraction": sd[7]/sum(sd),
        "cpu_psi_some_fraction": psi/1e6/elapsed,
        "driver_cpu_ticks": ticks(b["/proc/self/stat"])-ticks(a["/proc/self/stat"]),
        "throttled_usec_delta": counter(cb, "throttled_usec")-counter(ca, "throttled_usec"),
        "nr_throttled_delta": counter(cb, "nr_throttled")-counter(ca, "nr_throttled"),
        "cpu_max": a["/sys/fs/cgroup/cpu.max"],
    }


out = {"inputs": {}, "source_reports_only": True, "limitations": [
    "Median of three window percentiles is not a pooled P99 and has no confidence interval.",
    "Request time includes driver decode/validation and scheduling; stage sums overlap and are wall time, not CPU.",
    "Driver ticks not converted to seconds; remote CLK_TCK missing. Host brackets include warmup.",
    "Original 7763 failure retained; no new migration candidate or 20-endpoint publication gate qualified.",
]}
for key, directory, sha, run_dir in [
    ("forward_beta_first", OLD/"comparison", BASE, OLD),
    ("forward_candidate_second", OLD, CAND, OLD),
    ("reverse_candidate_first", NEW/"comparison", CAND, NEW),
    ("reverse_beta_second", NEW, BASE, NEW),
]:
    raw = (directory/"report.json").read_bytes()
    report = json.loads(raw)
    receipt = (directory/"binary.sha256").read_text().split()[0]
    build = (directory/"build.txt").read_text()
    valid = (report["product_sha"] == sha and report["harness_sha"] == CAND
             and not report["harness_dirty"] and report["binary_sha256"] == receipt
             and report["profile_sha256"] == profile_sha and report["config"] == profile["stress_gate"]
             and report["driver_gomaxprocs"] == 4 and report["node_gomaxprocs"] == 2
             and "vcs.revision="+sha in build and "vcs.modified=false" in build)
    windows, all_metrics = [], defaultdict(float)
    for i, w in enumerate(report["windows"]):
        merged = deltas(w)
        for k, v in merged.items():
            all_metrics[k] += v
        ph = []
        for q, (endpoint, rate) in zip(w["phases"], [("/conversation/list", 200), ("/conversation/sync", 60)]):
            checks = {
                "shape": q["case"] == {"endpoint": endpoint, "page_size": 100, "offered_qps": rate} and q["nodes"] == 3 and q["driver_workers"] == 8 and q["queue_capacity"] == max(8, math.ceil(rate*.5)),
                "duration": q["duration_seconds"] == 60 and abs((instant(w["end"])-instant(w["start"])).total_seconds()-60) < .001,
                "completion": q["scheduled"] == rate*60 and q["completed"] == q["scheduled"] and q["actual_qps"] >= rate*.95 and abs(q["actual_qps"]-q["completed_in_window"]/60) < .00001,
                "latency": 0 < q["p99_ms"] <= 500,
                "zero_failures_and_side_effects": all(q[k] == 0 for k in ["errors","unexpected_errors","dropped","runtime_loads","membership_writes","active_runtimes_before","active_runtimes_after"]),
                "reported_pass": q["verdict"] == "pass",
            }
            ph.append({**{k:q[k] for k in ["case","actual_qps","p50_ms","p95_ms","p99_ms","driver_wait_p99_ms","request_p99_ms","scheduled","completed","completed_in_window","errors","unexpected_errors","dropped","verdict"]}, "independent_checks":checks, "pass":all(checks.values())})
        done = sum(p["completed"] for p in ph)
        windows.append({
            "window": i+1, "start": w["start"], "end": w["end"], "phases": ph,
            "cpu_seconds": w["cpu_seconds"], "cpu_ms_per_request":w["cpu_seconds"]*1000/done,
            "allocated_bytes":w["allocated_bytes"],"allocated_bytes_per_request":w["allocated_bytes"]/done,
            "allocation_within_limit":w["allocated_bytes"]/done<=profile["stress_gate"]["max_allocated_bytes_per_request"]["mixed"],
            "host":host(report,i),
            "stages":means(merged, "wukongim_conversation_read_stage_duration_seconds"),
            "persisted_admission":totals(merged, "wukongim_conversation_persisted_admission_total"),
            "rpc_calls":totals(merged,"wukongim_transport_rpc_client_total"),
        })
    done = sum(p["completed"] for w in windows for p in w["phases"])
    endpoint_summary = {}
    endpoint_mean = {}
    for endpoint in ["/conversation/list","/conversation/sync"]:
        qs = [p for w in windows for p in w["phases"] if p["case"]["endpoint"] == endpoint]
        endpoint_summary[endpoint] = {k:statistics.median(p[k] for p in qs) for k in ["actual_qps","p50_ms","p95_ms","p99_ms","driver_wait_p99_ms","request_p99_ms"]}
        endpoint_mean[endpoint] = {k:statistics.mean(p[k] for p in qs) for k in ["actual_qps","p50_ms","p95_ms","p99_ms","driver_wait_p99_ms","request_p99_ms"]}
    run = json.loads((run_dir/"run.json").read_text())
    step_name = ("Collect comparison windows with the identical harness and preset" if directory.name == "comparison"
                 else "Collect three fixed windows then separate profiles")
    step = next(s for j in run["jobs"] for s in j["steps"] if s["name"] == step_name)
    test_duration = float(re.search(r"--- PASS: TestConversationQPSMixedAttribution \(([\d.]+)s\)", (directory/"test.log").read_text()).group(1))
    first, profile_end = instant(report["windows"][0]["start"]), instant(report["profile_window"]["end"])
    prep = {
        "step_start":step["startedAt"],"first_window_start":report["windows"][0]["start"],
        "step_to_first_seconds":(first-instant(step["startedAt"])).total_seconds(),
        "test_duration_seconds":test_duration,
        "test_pre_first_upper_bound_seconds":test_duration-(profile_end-first).total_seconds(),
        "test_pre_first_lower_bound_seconds_rounded_step_end":test_duration-(instant(step["completedAt"])-first).total_seconds(),
    }
    out["inputs"][key] = {
        "path":str(directory/"report.json"),"sha256":hashlib.sha256(raw).hexdigest(),"product_sha":sha,
        "binary_sha256_receipt":receipt,"identity_consistent":valid,
        "all_three_windows_independently_pass":valid and report["complete"] and len(windows)==3 and all(len(w["phases"])==2 and all(p["pass"] for p in w["phases"]) and w["allocation_within_limit"] for w in windows),
        "windows":windows,"median_window_percentiles":endpoint_summary,
        "mean_window_percentiles":endpoint_mean,
        "aggregate":{
            "completed":done,"cpu_seconds":sum(w["cpu_seconds"] for w in windows),
            "cpu_ms_per_request":sum(w["cpu_seconds"] for w in windows)*1000/done,
            "allocated_bytes_per_request":sum(w["allocated_bytes"] for w in windows)/done,
            "driver_ticks_per_request":sum(w["host"]["driver_cpu_ticks"] for w in windows)/done,
            "stages":means(all_metrics,"wukongim_conversation_read_stage_duration_seconds"),
            "rpc_calls":totals(all_metrics,"wukongim_transport_rpc_client_total"),
        },"preparation":prep,
        "profile_window":{k:report["profile_window"][k] for k in ["start","end","cpu_seconds","allocated_bytes"]},
    }
out["candidate_minus_beta_percent"] = {}
for direction, bk, ck in [("forward","forward_beta_first","forward_candidate_second"),("reverse","reverse_beta_second","reverse_candidate_first")]:
    b,c=(out["inputs"][k] for k in (bk,ck))
    out["candidate_minus_beta_percent"][direction] = {
        "cpu_ms_per_request":(c["aggregate"]["cpu_ms_per_request"]/b["aggregate"]["cpu_ms_per_request"]-1)*100,
        "allocated_bytes_per_request":(c["aggregate"]["allocated_bytes_per_request"]/b["aggregate"]["allocated_bytes_per_request"]-1)*100,
        "driver_ticks_per_request":(c["aggregate"]["driver_ticks_per_request"]/b["aggregate"]["driver_ticks_per_request"]-1)*100,
        "median_p99":{ep:(c["median_window_percentiles"][ep]["p99_ms"]/b["median_window_percentiles"][ep]["p99_ms"]-1)*100 for ep in b["median_window_percentiles"]},
        "mean_p99":{ep:(c["mean_window_percentiles"][ep]["p99_ms"]/b["mean_window_percentiles"][ep]["p99_ms"]-1)*100 for ep in b["mean_window_percentiles"]},
        "same_rpc_exact_call_totals":b["aggregate"]["rpc_calls"] == c["aggregate"]["rpc_calls"],
    }
(ROOT/"reverse-independent-check.json").write_text(json.dumps(out,indent=2)+"\n")
print(json.dumps({k:v for k,v in out.items() if k!="inputs"},indent=2))
for k,v in out["inputs"].items():
    print(k, json.dumps({"pass":v["all_three_windows_independently_pass"],"aggregate":v["aggregate"],"median":v["median_window_percentiles"],"preparation":v["preparation"]},indent=2))
