#!/usr/bin/env python3
"""Format independently audited observed results; does not change frozen screens."""
import argparse
import json
from pathlib import Path
from statistics import median

p = argparse.ArgumentParser()
p.add_argument("audit", type=Path)
p.add_argument("--artifact", type=Path, required=True)
args = p.parse_args()
a = json.loads(args.audit.read_text())
out = args.audit.parent
windows = [w for b in a["blocks"].values() for w in b["windows"]]
phases = [p for w in windows for p in w["phases"]]
roles = [r for w in windows for r in w["sampling"]["roles"].values()]
leaf = "/sys/fs/cgroup/system.slice/hosted-compute-agent.service/"
summary = {
    "run": 39467558163,
    "harness_sha": a["expected_harness_sha"],
    "evidence_valid": a["evidence_valid"],
    "independent_issues": a["issues"],
    "all_original_windows_numeric_pass": a["all_original_windows_numeric_pass"],
    "request_totals": {k: sum(p[k] for p in phases) for k in
        ("scheduled", "completed", "completed_in_window", "errors", "unexpected_errors", "dropped")},
    "pair_screen": a["predeclared_pair_screen"],
    "repeat_variation": a["repeat_variation"],
    "coverage": {
        "points_per_window": [w["sampling"]["points"] for w in windows],
        "matched_task_intervals": sum(r["matched_task_intervals"] for r in roles),
        "union_task_intervals": sum(r["union_task_intervals"] for r in roles),
        "new_task_lifetimes_after_first": sum(r["new_after_first"] for r in roles),
        "exited_or_unobserved": sum(r["exited_or_unobserved"] for r in roles),
        "minimum_role_count_coverage": min(r["matched_over_union_task_interval_ratio"] for r in roles),
        "note": "Count coverage only. Sampling starts/ends and unmatched birth edges do not become zero wait."
    },
    "observer_ranges": {k: [min(w["sampling"]["overhead"][k] for w in windows),
                             max(w["sampling"]["overhead"][k] for w in windows)] for k in
        ("sampler_cpu_ns", "read_bytes", "conservative_receipt_bytes", "sum_read_wall_ns",
         "max_read_wall_ns", "sampler_cpu_fraction_of_one_cpu_window")},
    "blocks": {},
}
for bid, b in a["blocks"].items():
    summary["blocks"][bid] = {
        "endpoint_window_medians": b["endpoint_window_medians"],
        "product_cpu_seconds_median": median(w["cpu_seconds_product_metrics"] for w in b["windows"]),
        "product_cpu_ms_per_completed_request_median":
            1000 * b["median_cpu_seconds_per_completed_request"],
        "role_window_medians": {role: {k: median(w["sampling"]["roles"][role][k] for w in b["windows"])
            for k in ("matched_runtime_seconds", "matched_runqueue_wait_seconds", "process_cpu_seconds_sampled_span")}
            for role in ("driver", "node1", "node2", "node3")},
        "nodes_summed_matched_runqueue_wait_seconds_window_median": median(
            sum(w["sampling"]["roles"][role]["matched_runqueue_wait_seconds"] for role in ("node1", "node2", "node3"))
            for w in b["windows"]),
        "leaf_cpu_pressure_some_seconds_window_median": median(
            w["sampling"]["scoped_counter_deltas"][leaf + "cpu.pressure"]["first_to_last_delta"]["some_total_usec"] / 1e6
            for w in b["windows"]),
    }
summary["visible_throttle_all_zero"] = all(
    data["first_to_last_delta"][name] == 0
    for w in windows for path, data in w["sampling"]["scoped_counter_deltas"].items()
    if path.endswith("cpu.stat")
    for name in ("nr_throttled", "throttled_usec"))
summary["host_steal_all_zero"] = all(
    w["sampling"]["scoped_counter_deltas"]["/proc/stat"]["first_to_last_delta"]["steal_ticks"] == 0 for w in windows)

# Additional diagnosis requested after results: all three S1 windows are retained.
raw = json.loads((args.artifact / "blocks/01-S/report.json").read_text())
s1 = []
for number, (w, original) in enumerate(zip(a["blocks"]["01-S"]["windows"], raw["windows"]), 1):
    aligned = []
    for sec in range(60):
        row = {"scheduled_arrival_second": sec}
        for phase in w["phases"]:
            endpoint = phase["endpoint"].split("/")[-1]
            source = phase["arrival_timeline_by_scheduled_second"][sec]
            row.update({endpoint + "_" + key: source.get(key) for key in
                ("max_latency_ms", "max_request_ms", "max_producer_delay_ms", "max_queued_worker_wait_ms")})
        for role, data in w["sampling"]["roles"].items():
            current = data["samples"][sec + 1]
            row[role + "_matched_wait_s"] = current["matched_runqueue_wait_ns"] / 1e9
            row[role + "_matched_runtime_s"] = current["matched_runtime_ns"] / 1e9
        row["nodes_matched_wait_s"] = sum(row[role + "_matched_wait_s"] for role in ("node1", "node2", "node3"))
        row["four_process_matched_runtime_s"] = sum(row[role + "_matched_runtime_s"] for role in ("driver", "node1", "node2", "node3"))
        scoped = w["sampling"]["scoped_counter_deltas"]
        row["leaf_cpu_s"] = scoped[leaf + "cpu.stat"]["intervals"][sec]["delta"]["usage_usec"] / 1e6
        row["leaf_cpu_minus_four_pid_matched_runtime_s"] = row["leaf_cpu_s"] - row["four_process_matched_runtime_s"]
        row["leaf_pressure_some_s"] = scoped[leaf + "cpu.pressure"]["intervals"][sec]["delta"]["some_total_usec"] / 1e6
        row["host_cpu_delta"] = scoped["/proc/stat"]["intervals"][sec]["delta"]
        row["sampler_read_ms"] = original["cpu_samples"]["points"][sec + 1]["read_duration_ns"] / 1e6
        aligned.append(row)
    pauses = sum(v["go_gc_duration_seconds_sum{}"] for v in original["metrics_after"]) - sum(
        v["go_gc_duration_seconds_sum{}"] for v in original["metrics_before"])
    s1.append({"window": number, "product_cpu_s": w["cpu_seconds_product_metrics"],
               "product_allocated_bytes": w["allocated_bytes_product_metrics"], "product_gc_pause_sum_seconds": pauses,
               "role_matched_wait_seconds": {role: data["matched_runqueue_wait_seconds"] for role, data in w["sampling"]["roles"].items()},
               "rows": aligned})
summary["s1_all_windows_local_alignment"] = s1
summary["s1_alignment_limits"] = (
    "Planned arrival bucket maxima are aligned to adjacent sample points, not actual request execution. "
    "Process task reads and cgroup reads are staggered: the CPU difference is a scope discrepancy, "
    "not an exact conservation identity or identification of an external process. "
    "The last three slow buckets coincide with local runqueue/visible CPU increases; neither confirms a unique cause.")
(out / "independent-summary.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False) + "\n")

lines = [
    "# Run 39467558163 独立复核",
    "",
    "冻结产品 C=`ef798352c40a94e57f768f76b2f4713712f827fe`，测试驱动 "
    "`07ed85a22bdd2ebf75b776a92e71f21c02d2623f`。实际二进制、profile、全部身份/掩码/原始"
    " schedstat 差分核对通过；独立审计 0 issue。完整验算见 `audit.json`。",
    "",
    "**原数值门禁全部通过，但预声明的双端点 CPU 竞争筛选未通过。** "
    "18 个窗口、36 个端点共计划/完成 280,800 次请求，窗口内完成 280,727 次，"
    "errors、unexpected_errors、drop 均为 0。这里未重新执行完整 20 项发布门禁。",
    "",
    "以下均为三个窗口的 P99 中位数，单位 ms；不是 pooled P99。",
    "",
    "| 配对 | 端点 | O→D driver-wait | 降幅 | O→D total | 预筛选 |",
    "|---|---|---:|---:|---:|---|",
]
for pair in summary["pair_screen"]:
    for ep, v in pair["endpoints"].items():
        m, n = v["O_medians"], v["D_medians"]
        lines.append(f"| {pair['O']}/{pair['D']} | {ep.split('/')[-1]} | "
                     f"{m['driver_wait_p99_ms']:.3f}→{n['driver_wait_p99_ms']:.3f} | "
                     f"{100*v['driver_wait_decrease_fraction']:.3f}% / {v['driver_wait_decrease_ms']:.3f} ms | "
                     f"{m['total_p99_ms']:.3f}→{n['total_p99_ms']:.3f} | {'达到' if v['arithmetic_screen_met'] else '未达到'} |")
lines += [
    "",
    "list 的两组局部效果符合逻辑 CPU 竞争假设；sync 未满足 driver-wait 同时下降≥25%且≥1ms。"
    "不能删除 sync 条件、挑窗口或把本轮 green 改写为根因全部确认。",
    "",
    "| 模式轮次 | list producer / queued-worker P99 | sync producer / queued-worker P99 |",
    "|---|---:|---:|",
]
for bid, b in summary["blocks"].items():
    l, s = (b["endpoint_window_medians"][ep] for ep in ("/conversation/list", "/conversation/sync"))
    lines.append(f"| {bid} | {l['producer_p99_ms']:.3f} / {l['queued_worker_p99_ms']:.3f} | "
                 f"{s['producer_p99_ms']:.3f} / {s['queued_worker_p99_ms']:.3f} |")
lines += [
    "",
    "O→D 的 list queued-worker 中位由 7.501/5.378 ms 降到 0.051/0.044 ms；"
    "sync 的 driver 等待主要表现为 producer 延后，queued-worker 约 0.016–0.020 ms。"
    "不同人口的分位数不可相加，也不可用分位数差直接分配每个请求的因果贡献。",
    "",
    "| 轮次 | driver matched CPU / runqueue wait (s) | 三节点合计 matched wait 中位 (s) | 产品 CPU/request (ms) | leaf PSI some 中位 (s) |",
    "|---|---:|---:|---:|---:|",
]
for bid, b in summary["blocks"].items():
    role = b["role_window_medians"]["driver"]
    lines.append(f"| {bid} | {role['matched_runtime_seconds']:.3f} / {role['matched_runqueue_wait_seconds']:.3f} | "
                 f"{b['nodes_summed_matched_runqueue_wait_seconds_window_median']:.3f} | "
                 f"{b['product_cpu_ms_per_completed_request_median']:.4f} | "
                 f"{b['leaf_cpu_pressure_some_seconds_window_median']:.3f} |")
lines += [
    "",
    "O/D 的 driver matched wait 两次均约减半，节点总等待也下降，而完成请求混合比例固定，"
    "产品 CPU/request 并未同步下降。四角色完整逐窗数据在 `roles.csv`；task wait 是跨线程累加，"
    "不是墙钟等待或 goroutine 等待，不能把 S 与 D 当作单变量对比。",
    "",
    "| 重复 | list total P99 中位相对差 | sync total P99 中位相对差 |",
    "|---|---:|---:|",
]
for repeat in summary["repeat_variation"]:
    l, s = (repeat["metrics"][ep]["total_p99_ms"]["relative_absolute_difference_over_smaller"]
            for ep in ("/conversation/list", "/conversation/sync"))
    lines.append(f"| {repeat['first']}/{repeat['last']} | {100*l:.3f}% | {100*s:.3f}% |")
lines += [
    "",
    "重复差异分母为两个正中位数中较小者；这是描述性结果，没有临时追加重复性通过阈值。"
    "O/O 的 list queued-worker 中位仍有约39.48%变化，S1第三窗也必须保留。",
    "",
    f"每窗均61点，共1098点，没有漏采；matched task intervals="
    f"{summary['coverage']['matched_task_intervals']}/{summary['coverage']['union_task_intervals']}。"
    f"共{summary['coverage']['new_task_lifetimes_after_first']}个首点之后的新任务生命周期、"
    f"{summary['coverage']['exited_or_unobserved']}个退出/未观察区间；最低角色计数覆盖率"
    f"{100*summary['coverage']['minimum_role_count_coverage']:.4f}%。这不是时间覆盖率，出生边缘和测量首尾仍未知。",
    "",
    "采集器每窗 CPU 259.4–355.6 ms，占一颗逻辑 CPU ×60s 的0.432–0.593%；读取7.93–8.21MB，"
    "累计读操作墙钟309.3–494.6ms，单次最大6.49–21.08ms。采集 CPU 已包含在 driver CPU 中。"
    "观测成本低于整窗 CPU 并不能证明它没有影响尾延迟，本轮没有 observer-off 对照。",
    "",
    "所有可见 cgroup 的 nr_throttled/throttled_usec 差分为0，host steal ticks 为0。"
    "leaf CPU PSI some 在 O 约40–41s、D 约32–33s；祖先范围重叠，未相加，"
    "这些观测不能排除不可见宿主竞争。",
    "",
    "## S1第三窗的局部边界",
    "",
    "| 窗口 | list / sync P99 (ms) | 产品 CPU (s) | 分配 (GB，十进制) | GC pause 总和 (s) | driver / 三节点 matched wait (s) |",
    "|---|---:|---:|---:|---:|---:|",
]
for v, w in zip(s1, a["blocks"]["01-S"]["windows"]):
    waits = v["role_matched_wait_seconds"]
    lines.append(f"| {v['window']} | {w['phases'][0]['total_p99_ms']:.3f} / {w['phases'][1]['total_p99_ms']:.3f} | "
                 f"{v['product_cpu_s']:.3f} | {v['product_allocated_bytes']/1e9:.3f} | {v['product_gc_pause_sum_seconds']:.6f} | "
                 f"{waits['driver']:.3f} / {sum(waits[k]for k in ('node1','node2','node3')):.3f} |")
lines += [
    "",
    "第三窗尾延迟约翻倍，但 CPU、分配、GC总暂停和整体 task wait 未同比翻倍。"
    "最后57–59秒出现局部 runqueue 峰，下面是按计划到达秒与邻接采样区间的粗粒度对齐。",
    "",
    "| 秒 | list / sync 最大延迟 (ms) | driver / nodes wait (s) | 四 PID matched CPU / leaf CPU (s) | host user / idle ticks |",
    "|---|---:|---:|---:|---:|",
]
for row in s1[2]["rows"][-4:]:
    lines.append(f"| {row['scheduled_arrival_second']} | {row['list_max_latency_ms']:.2f} / {row['sync_max_latency_ms']:.2f} | "
                 f"{row['driver_matched_wait_s']:.3f} / {row['nodes_matched_wait_s']:.3f} | "
                 f"{row['four_process_matched_runtime_s']:.3f} / {row['leaf_cpu_s']:.3f} | "
                 f"{row['host_cpu_delta']['user_ticks']} / {row['host_cpu_delta']['idle_ticks']} |")
lines += [
    "",
    "57–59秒同时出现更高可见 user CPU 和较低 idle；leaf CPU 与四被测 PID matched CPU 的差额扩大，"
    "与额外可见 CPU 活动和调度竞争相符。读取时点不同，差额不是精确守恒，也不能据此命名其他进程。"
    "尚未捕获请求级调度因果链；O/D 的 list 局部机制不能解释全部 S 波动或旧主机失败。",
    "",
    "结论边界：可复验地观测到 O/D 绑定对 list 排队及 driver/node 调度等待的影响；"
    "预声明的双端点整体筛选失败，历史根因未唯一确认，也没有无性能回退证明。"
    "本次未改产品、未重跑负载、未发布版本。",
]
(out / "independent-summary.md").write_text("\n".join(lines) + "\n")
print(out / "independent-summary.md")
