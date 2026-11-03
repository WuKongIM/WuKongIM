#!/usr/bin/env python3
"""Retain bounded terminal documents and fixed, failure-anchored metric partitions."""
import datetime
from decimal import Decimal, ROUND_HALF_UP
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import selectors
import signal
import subprocess
import sys
import tempfile
import time

MAX_FILE_BYTES = 4 << 20
METRIC_TOTAL_BYTES = 24 << 20
METRIC_DEADLINE_SECONDS = 60
METRIC_PARTS_SCHEMA = "wukongim.chat_lifecycle.metric_parts/v1"
# Exact diagnostic families precede bounded runtime and registered resource families.
# This ordering preserves useful failure signals even when the shared budget expires.
METRIC_PARTITIONS = (
    ("presence", r"wukongim_presence_endpoint_lookup_(total|items_total|groups_total|duration_seconds_(bucket|sum|count))"),
    ("delivery", r"wukongim_delivery_(recipient_worker_(queue_depth|queue_capacity|inflight|capacity|admission_total|admission_wait_seconds_(bucket|sum|count)|process_total|process_duration_seconds_(bucket|sum|count)|process_recipients_(bucket|sum|count))|recipient_authority_resolve_(total|items_total|targets_total|duration_seconds_(bucket|sum|count))|push_rpc_(total|routes_total|duration_seconds_(bucket|sum|count)))"),
    ("up", r"up"),
    ("runtime", r"wukongim_runtime_.*"),
    ("channel_worker", r"wukongim_channel_worker_.*"),
    ("process", r"(wukongim_)?process_.*"),
    ("counters", r"wukongim_channel(v2)?_(meta_created|activation_rejected)_total"),
    ("go", r"go_.*"),
)


def require(condition):
    if not condition:
        raise ValueError("invalid terminal evidence")


def uint(value, minimum=0, maximum=(1 << 64)-1):
    require(type(value) is int and minimum <= value <= maximum)


def keys(value, required, optional=()):
    require(type(value) is dict and set(required) <= value.keys() <= set(required) | set(optional))


def strict_json(body):
    def pairs(items):
        result = {}
        for key, value in items:
            require(key not in result)
            result[key] = value
        return result
    return json.loads(body, object_pairs_hook=pairs, parse_float=Decimal,
                      parse_constant=lambda _: require(False))


def epoch(value):
    """Keep the report's nanoseconds; datetime alone truncates them to microseconds."""
    require(type(value) is str)
    match = re.fullmatch(r"(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d{1,9})?(Z|[+-]\d\d:\d\d)", value)
    require(match is not None)
    whole = datetime.datetime.fromisoformat(match[1]+match[3].replace("Z", "+00:00"))
    return Decimal(int(whole.timestamp())) + Decimal(match[2] or "0")


# Closed pairs emitted by the chat-lifecycle verdict and capacity controllers.
# This validates an evidence anchor, never reclassifies the workload verdict.
VERDICT_CAUSES = {
    "pass": {"completed"},
    "rehearsal_pass": {"rehearsal_completed"},
    "passed_with_capacity_warning": {"infrastructure_capacity"},
    "product_failure": {"message_loss", "message_duplicate", "message_corruption", "sequence_regression",
                        "terminal_send", "activation_rejection", "overall_first_attempt_rate", "minute_first_attempt_rate",
                        "server_crash", "hot_latency", "cold_latency", "sync_latency", "heap_growth", "goroutine_growth",
                        "queue_recovery", "worker_product_failure", "lifecycle_product_failure", "meta_create_product_failure",
                        "capacity_headroom_latency"},
    "harness_invalid": {"counter_regression", "queue_saturation", "observer_gap", "invalid_observation",
                        "worker_harness_invalid", "lifecycle_harness_invalid"},
    "infrastructure_failure": {"disk_exhausted", "budget_exhausted", "lease_expiry"},
    "operator_stop": {"operator_requested"},
    "insufficient_evidence": {"insufficient_evidence"},
}
FAILED_OUTCOMES = {"product_failure", "harness_invalid", "infrastructure_failure", "operator_stop", "insufficient_evidence"}


def validate_terminal_phase(report):
    """An absent capacity report is exempt only with unambiguous phase evidence."""
    require(report["continue"] is False)
    outcome, cause = report["verdict"]["outcome"], report["verdict"]["cause"]
    require(type(outcome) is str and type(cause) is str and cause in VERDICT_CAUSES.get(outcome, ()))
    if outcome == "rehearsal_pass":
        require(report["stage"] == "rehearsal" and report["mode"] == "soak")
    if outcome in ("pass", "passed_with_capacity_warning", "insufficient_evidence"):
        require(report["stage"] == "formal")
    capacity = report["capacity"]
    require(type(capacity["attempted"]) is bool)
    if not capacity["attempted"]:
        for name in ("completed", "lower_bound", "recovery_passed"):
            require(capacity[name] is False)
        for name in ("maximum_passing_rate", "first_failing_rate"):
            uint(capacity[name], 0, 0)
        require(capacity.get("attribution", "") == "" and report["mode"] == "soak")
    if cause == "capacity_headroom_latency":
        require(report["mode"] == "capacity" and capacity.get("attribution") == "product_failure")
    if report["mode"] == "capacity" and outcome in ("insufficient_evidence", "passed_with_capacity_warning"):
        require(capacity.get("attribution") == cause)


def read_report(path, directory):
    require(not path.is_symlink())
    with path.open("rb") as source:
        body = source.read(MAX_FILE_BYTES+1)
    require(0 < len(body) <= MAX_FILE_BYTES)
    report = strict_json(body)
    # Legacy v7 remains readable as archived diagnostics, not current terminal proof.
    # Only the current v8 report may anchor this evidence handoff.
    require(report["schema_version"] == "wukongim/chat-lifecycle-report/v8" and
            report["threshold_version"] == "wukongim/chat-lifecycle-thresholds/v2" and
            report["kind"] == "final" and report["final"] is True)
    require(report["stage"] == ("rehearsal" if directory == "rehearsal" else "formal"))
    require(report["mode"] == ("capacity" if directory == "capacity" else "soak") and report["verdict"]["terminal"] is True)
    qualification = report["qualification"]
    require(type(qualification) is dict and
            qualification["scope"] == ("rehearsal" if directory == "rehearsal" else "formal_protocol") and
            qualification["external_linux_evidence_required"] is True)
    validate_terminal_phase(report)
    start, end = epoch(report["window"]["start"]), epoch(report["window"]["end"])
    require(0 < start <= end <= Decimal(str(time.time())))
    require(re.fullmatch(r"sha256:[0-9a-f]{64}", report["fence"]["run_hash"]) is not None)
    uint(report["fence"]["generation"], 1)
    workers = report["worker_generations"]
    require(type(workers) is list and len(workers) == 3)
    require({w["worker_index"] for w in workers} == {0, 1, 2})
    for item in workers:
        uint(item["worker_index"], 0, 2)
        uint(item["generation"], 1)
        uint(item["snapshot_sequence"], 1)
        require(item["generation"] == report["fence"]["generation"])
    # Prometheus v3.13.1 numeric query times have nearest-millisecond precision.
    # Send canonical bounds and validate that same closed interval; keep the raw cut.
    query_end = end.quantize(Decimal(".001"), rounding=ROUND_HALF_UP)
    return report, {"end": report["window"]["end"], "epoch_end": str(query_end), "epoch_start": str(query_end-900),
                    "query_precision": "nearest_millisecond",
                    "source": directory+"/final.json", "report_sha256": hashlib.sha256(body).hexdigest()}


def identity(document, report, timestamp):
    run = document["run_id"]
    require(type(run) is str and 0 < len(run) <= 1024)
    require("sha256:"+hashlib.sha256(run.encode()).hexdigest() == report["fence"]["run_hash"])
    require(epoch(document[timestamp]) == epoch(report["window"]["end"]))


def validate_worker(body, report):
    value = strict_json(body)
    keys(value, ("schema", "run_id", "at", "workers"))
    # Preserve archived vocabularies. V3 appends closed CONNECT diagnostics
    # without changing the five redacted fields retained in each example.
    maximum_code = {"wukongim/chat-lifecycle-worker-evidence/v1": 36,
                    "wukongim/chat-lifecycle-worker-evidence/v2": 46,
                    "wukongim/chat-lifecycle-worker-evidence/v3": 53}.get(value["schema"])
    require(maximum_code is not None)
    identity(value, report, "at")
    expected = {w["worker_index"]: w for w in report["worker_generations"]}
    require(type(value["workers"]) is list and len(value["workers"]) == 3)
    seen = set()
    for worker in value["workers"]:
        keys(worker, ("worker_id", "generation", "snapshot_sequence", "evidence"))
        index = worker["worker_id"]
        uint(index, 0, 2)
        require(index not in seen)
        seen.add(index)
        for name in ("generation", "snapshot_sequence"):
            uint(worker[name], 1)
            require(worker[name] == expected[index][name])
        evidence = worker["evidence"]
        keys(evidence, ("classes",), ("classification",))
        require(evidence.get("classification", "") in ("", "product_failure", "harness_invalid"))
        classes = [] if evidence["classes"] is None else evidence["classes"]
        require(type(classes) is list and len(classes) <= 4)
        seen_classes = set()
        for row in classes:
            keys(row, ("class", "count", "first", "last"))
            uint(row["class"], 1, 4)
            require(row["class"] not in seen_classes)
            seen_classes.add(row["class"])
            uint(row["count"], 1)
            examples = []
            for side in ("first", "last"):
                items = [] if row[side] is None else row[side]
                require(type(items) is list and len(items) <= 64)
                examples.extend(items)
            require(len(examples) <= row["count"])
            for item in examples:
                keys(item, ("stage", "code", "sample_index", "fingerprint", "value"))
                uint(item["stage"], 1, 9)
                uint(item["code"], 1, maximum_code)
                uint(item["sample_index"])
                uint(item["value"])
                if item["code"] >= 47:
                    require(row["class"] == 4 and item["stage"] == 8)
                    if item["code"] == 52:
                        # Current WKProto reason bytes end at SystemBusy=30;
                        # zero is unknown, while success=1 cannot be a rejection.
                        uint(item["value"], 0, 30)
                        require(item["value"] != 1)
                    else:
                        uint(item["value"], 0, 10)
                require(type(item["fingerprint"]) is list and len(item["fingerprint"]) == 16)
                for byte in item["fingerprint"]:
                    uint(byte, 0, 255)


CONNECTION_KEYS = ("target", "online", "starting", "closing", "traffic_ready")
CLOSE_KEYS = ("expired", "heartbeat_failed", "remote_terminal", "read_failed", "generation_stop", "explicit_logout", "transport_close_failed")
REJECTION_KEYS = ("missing_timer", "invalid_shape", "timer_fence", "activity_fence", "invalidated", "fence_exhausted", "deadline", "replay_expired", "replay_mismatch")


def counters(value, names):
    keys(value, names)
    for item in value.values():
        uint(item)


def connections(value):
    counters(value, CONNECTION_KEYS)
    require(value["target"] > 0 and value["traffic_ready"] <= value["online"])
    require(value["online"]+value["starting"]+value["closing"] <= value["target"])


def harness(value):
    names = ("failures", "command_saturation", "offered_underdelivery", "planned_cancellations")
    keys(value, names+("lifecycle_approval_rejections", "drain_timed_out", "unexpected_exit"), ("classification",))
    for name in names:
        uint(value[name])
    require(type(value["drain_timed_out"]) is bool and type(value["unexpected_exit"]) is bool)
    require(value.get("classification", "") in ("", "product_failure", "harness_invalid"))
    counters(value["lifecycle_approval_rejections"], REJECTION_KEYS)


def validate_status(body, report):
    value = strict_json(body)
    keys(value, ("schema", "run_id", "state", "stage", "started_at", "updated_at", "cut", "totals", "close_reasons", "harness", "workers", "recent_events"))
    require(value["schema"] == "wukongim/chat-lifecycle-diagnostic-status/v1" and value["cut"] == "terminal")
    require(value["state"] == "running" and value["stage"] == "measured")
    identity(value, report, "updated_at")
    require(epoch(value["started_at"]) == epoch(report["window"]["start"]))
    connections(value["totals"])
    counters(value["close_reasons"], CLOSE_KEYS)
    harness(value["harness"])
    expected = {w["worker_index"]: w for w in report["worker_generations"]}
    require(type(value["workers"]) is list and len(value["workers"]) == 3)
    seen = set()
    for worker in value["workers"]:
        keys(worker, ("worker_id", "phase", "snapshot_sequence", "connections", "close_reasons", "harness"))
        index = worker["worker_id"]
        uint(index, 0, 2)
        require(index not in seen and worker["phase"] in ("running", "final"))
        seen.add(index)
        uint(worker["snapshot_sequence"], 1)
        require(worker["snapshot_sequence"] == expected[index]["snapshot_sequence"])
        connections(worker["connections"])
        counters(worker["close_reasons"], CLOSE_KEYS)
        harness(worker["harness"])
    events = [] if value["recent_events"] is None else value["recent_events"]
    require(type(events) is list and len(events) <= 64)
    for event in events:
        keys(event, ("at", "kind", "worker_id", "connections", "close_reasons"))
        require(event["kind"] in ("worker_connections_changed", "worker_close_reasons_changed"))
        require(epoch(value["started_at"]) <= epoch(event["at"]) <= epoch(value["updated_at"]))
        uint(event["worker_id"], 0, 2)
        connections(event["connections"])
        counters(event["close_reasons"], CLOSE_KEYS)


def capture(command, timeout, limit):
    """Bound local reads as well as the remote head, and always join the SSH process."""
    args = ["ssh", "-F", os.environ["WK_CLOUD_SSH_CONFIG"], "wukong-load", command]
    body = bytearray()
    deadline = time.monotonic()+timeout
    try:
        with subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, start_new_session=True) as process:
            def stop():
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                process.wait()
            with selectors.DefaultSelector() as readable:
                readable.register(process.stdout, selectors.EVENT_READ)
                while True:
                    remaining = deadline-time.monotonic()
                    if remaining <= 0 or not readable.select(remaining):
                        stop()
                        return "timeout", bytes(body)
                    chunk = os.read(process.stdout.fileno(), min(65536, limit+1-len(body)))
                    if not chunk:
                        break
                    body.extend(chunk)
                    if len(body) > limit:
                        stop()
                        return "oversized", bytes(body)
            try:
                code = process.wait(timeout=max(0.001, deadline-time.monotonic()))
            except subprocess.TimeoutExpired:
                stop()
                return "timeout", bytes(body)
            if code != 0:
                return {3: "missing", 124: "timeout"}.get(code, "unavailable"), bytes(body)
            return ("collected" if body else "empty"), bytes(body)
    except OSError:
        return "unavailable", bytes(body)


def atomic_retain(path, body):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".terminal-", delete=False) as target:
        temporary = Path(target.name)
        try:
            os.chmod(temporary, 0o600)
            target.write(body)
            target.flush()
            os.fsync(target.fileno())
            os.replace(temporary, path)
        finally:
            temporary.unlink(missing_ok=True)


def document_capture(output, directory, report, name, limit, validator, timeout):
    remote = "/var/lib/wukongim-cloud/reports/"+directory+"/"+name+".json"
    command = "if sudo test -s "+shlex.quote(remote)+"; then sudo head -c "+str(limit+1)+" -- "+shlex.quote(remote)+"; else exit 3; fi"
    state, body = capture(command, timeout, limit)
    row = {"class": "workload", "name": directory+"-"+name, "state": state, "bytes": len(body), "max_bytes": limit}
    if state == "collected":
        try:
            validator(body, report)
        except (ValueError, KeyError, TypeError, RecursionError, OverflowError):
            row["state"] = "invalid"
        else:
            path = Path("workload")/directory/(name+".json")
            atomic_retain(output/path, body)
            row.update(path=str(path), sha256=hashlib.sha256(body).hexdigest())
    return row


def validate_metric(body, pattern, start, end):
    value = strict_json(body)
    require(value["status"] == "success" and value["data"]["resultType"] == "matrix")
    result = value["data"]["result"]
    require(type(result) is list)
    timestamps = []
    for series in result:
        require(type(series["metric"]) is dict and re.fullmatch(pattern, series["metric"]["__name__"]) is not None)
        require(all(type(k) is str and type(v) is str for k, v in series["metric"].items()))
        require(type(series["values"]) is list and len(series["values"]) <= 61)
        previous = None
        for sample in series["values"]:
            require(type(sample) is list and len(sample) == 2 and type(sample[0]) in (int, Decimal) and type(sample[1]) is str)
            instant = Decimal(sample[0])
            require(instant.is_finite() and Decimal(start) <= instant <= Decimal(end))
            require(instant == instant.quantize(Decimal(".001")))
            require(previous is None or instant > previous)
            require(re.fullmatch(r"[-+]?(?:\d+(?:\.\d*)?(?:[eE][-+]?\d+)?|Inf)|NaN", sample[1]) is not None)
            previous = instant
            timestamps.append(instant)
    return {"series_count": len(result), "sample_count": len(timestamps),
            "observed_start": str(min(timestamps)) if timestamps else None,
            "observed_end": str(max(timestamps)) if timestamps else None}


def metric_capture(name, query, start, end, timeout, limit):
    parameters = ["query="+query, "start="+start, "end="+end, "step=15"]
    curl = "curl --get --fail --silent --show-error --max-time "+str(timeout)
    curl += "".join(" --data-urlencode "+shlex.quote(value) for value in parameters)
    curl += " http://127.0.0.1:9090/api/v1/query_range | head -c "+str(limit+1)
    return capture("bash -o pipefail -c "+shlex.quote(curl), timeout, limit)


def clear_metric(output, name):
    """Remove only this fixed partition's old response, index and owned leaves."""
    directory = output/"metrics"
    prefix = "range-"+name
    (directory/(prefix+".json")).unlink(missing_ok=True)
    (directory/(prefix+".index.json")).unlink(missing_ok=True)
    for path in directory.glob(prefix+".part-[0-9][0-9][0-9]"):
        path.unlink()


def retain_metric(output, name, body, remaining_bytes):
    """Publish validated bytes; a multipart index commits the ordered raw leaves."""
    prefix = Path("metrics")/("range-"+name)
    digest = hashlib.sha256(body).hexdigest()
    if len(body) <= MAX_FILE_BYTES:
        if len(body) > remaining_bytes:
            return None
        path = prefix.with_suffix(".json")
        atomic_retain(output/path, body)
        return {"path": str(path), "bytes": len(body), "sha256": digest}, len(body)

    # Parts are byte slices, not independent JSON documents. Reassembly retains
    # every original label, timestamp and numeric string, including UTF-8 cuts.
    view = memoryview(body)
    parts = []
    for ordinal, offset in enumerate(range(0, len(body), MAX_FILE_BYTES)):
        leaf = view[offset:offset+MAX_FILE_BYTES]
        parts.append({"path": str(prefix)+".part-"+str(ordinal).zfill(3),
                      "bytes": len(leaf), "sha256": hashlib.sha256(leaf).hexdigest()})
    index = {"schema": METRIC_PARTS_SCHEMA, "response_bytes": len(body),
             "response_sha256": digest, "parts": parts}
    index_body = json.dumps(index, separators=(",", ":")).encode()+b"\n"
    retained_bytes = len(body)+len(index_body)
    if len(index_body) > MAX_FILE_BYTES or retained_bytes > remaining_bytes:
        return None
    try:
        for ordinal, part in enumerate(parts):
            offset = ordinal*MAX_FILE_BYTES
            atomic_retain(output/part["path"], view[offset:offset+MAX_FILE_BYTES])
        path = prefix.with_suffix(".index.json")
        # No index is visible until every leaf is durably retained.
        atomic_retain(output/path, index_body)
    except OSError:
        clear_metric(output, name)
        raise
    return {"storage": "parts", "format": METRIC_PARTS_SCHEMA, "path": str(path),
            "bytes": len(index_body), "sha256": hashlib.sha256(index_body).hexdigest(),
            "response_bytes": len(body), "response_sha256": digest}, retained_bytes


def collect_ranges(output, anchor, capture_range=metric_capture):
    """Bound response payloads (including failures) and retained files separately."""
    deadline = time.monotonic()+METRIC_DEADLINE_SECONDS
    rows = []
    budget = {"max_requests": len(METRIC_PARTITIONS), "requests": 0, "max_total_bytes": METRIC_TOTAL_BYTES,
              "observed_bytes": 0, "retained_bytes": 0, "deadline_seconds": METRIC_DEADLINE_SECONDS}
    for name, pattern in METRIC_PARTITIONS:
        row = {"class": "metrics", "name": "range-"+name, "state": "budget_exhausted", "bytes": 0,
               "query": "up" if name == "up" else '{__name__=~"'+pattern+'"}', "window": dict(anchor, step_seconds=15)}
        rows.append(row)
        try:
            clear_metric(output, name)
        except OSError:
            row["state"] = "unavailable"
            continue
        remaining_seconds = deadline-time.monotonic()
        remaining_bytes = METRIC_TOTAL_BYTES-budget["observed_bytes"]
        if remaining_seconds <= 0 or remaining_bytes <= 1:
            continue
        # One complete response can span several files. Keep the sentinel read
        # inside the shared payload budget without changing the eight queries.
        limit = remaining_bytes-1
        timeout = min(30, remaining_seconds)
        budget["requests"] += 1
        state, body = capture_range(name, row["query"], anchor["epoch_start"], anchor["epoch_end"], timeout, limit)
        require(len(body) <= limit+1)
        row.update(state=state, bytes=len(body), max_bytes=limit)
        budget["observed_bytes"] += len(body)
        if state != "collected":
            continue
        try:
            coverage = validate_metric(body, pattern, anchor["epoch_start"], anchor["epoch_end"])
        except (ValueError, KeyError, TypeError, RecursionError, OverflowError):
            row["state"] = "invalid"
            continue
        try:
            retained = retain_metric(output, name, body, METRIC_TOTAL_BYTES-budget["retained_bytes"])
        except OSError:
            row["state"] = "unavailable"
            continue
        if retained is None:
            row["state"] = "budget_exhausted"
            continue
        metadata, retained_bytes = retained
        budget["retained_bytes"] += retained_bytes
        row.update(coverage, **metadata)
    return rows, budget


def main(stage, output, timeout):
    rows = []
    reports = []
    root = output.parent
    primary = root/"final.json" if stage == "rehearsal" else root/"formal/final.json"
    budget = {"max_requests": len(METRIC_PARTITIONS), "requests": 0, "max_total_bytes": METRIC_TOTAL_BYTES,
              "observed_bytes": 0, "retained_bytes": 0, "deadline_seconds": METRIC_DEADLINE_SECONDS}
    try:
        report, anchor = read_report(primary, stage)
        reports.append((stage, report))
    except (OSError, ValueError, KeyError, TypeError, RecursionError, OverflowError):
        rows.append({"class": "workload", "name": "terminal-anchor", "state": "invalid", "bytes": 0})
        return {"captures": rows, "metrics_budget": budget}
    if stage == "formal":
        capacity = root/"capacity/final.json"
        if capacity.exists():
            try:
                cap_report, cap_anchor = read_report(capacity, "capacity")
                require(cap_report["fence"] == report["fence"])
                require(epoch(cap_report["window"]["end"]) >= epoch(report["window"]["end"]))
                reports.append(("capacity", cap_report))
                anchor = cap_anchor
            except (OSError, ValueError, KeyError, TypeError, RecursionError, OverflowError):
                rows.append({"class": "workload", "name": "capacity-terminal-anchor", "state": "invalid", "bytes": 0})
        elif report["verdict"]["outcome"] not in FAILED_OUTCOMES or report["capacity"]["attempted"]:
            rows.append({"class": "workload", "name": "capacity-terminal-anchor", "state": "missing", "bytes": 0})
        else:
            rows.append({"class": "workload", "name": "capacity-documents", "state": "not_applicable", "bytes": 0,
                         "reason": "formal_failed_before_capacity", "report_sha256": anchor["report_sha256"],
                         "phase_evidence": {"stage": report["stage"], "mode": report["mode"],
                                            "outcome": report["verdict"]["outcome"], "cause": report["verdict"]["cause"],
                                            "capacity_attempted": False}})
    for directory, report in reports:
        for name, limit, validator in (("worker-evidence", 1 << 20, validate_worker), ("diagnostic-status", 64 << 10, validate_status)):
            rows.append(document_capture(output, directory, report, name, limit, validator, min(timeout, 15)))
    metrics, budget = collect_ranges(output, anchor)
    return {"captures": rows+metrics, "metrics_budget": budget}


if __name__ == "__main__":
    # Keep arbitrary input, SSH errors, and remote response bodies out of diagnostics.
    try:
        require(len(sys.argv) == 4 and sys.argv[1] in ("rehearsal", "formal"))
        timeout = int(sys.argv[3])
        require(0 < timeout <= 120)
        result = main(sys.argv[1], Path(sys.argv[2]), timeout)
        print(json.dumps(result, separators=(",", ":")))
    except (OSError, ValueError, KeyError, TypeError, RecursionError, OverflowError):
        print('{"captures":[{"class":"workload","name":"terminal-collector","state":"unavailable","bytes":0}],"metrics_budget":null}')
        sys.exit(1)
