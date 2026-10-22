import datetime
import hashlib
import json
import os
from pathlib import Path
import resource
import secrets
import signal
import socket
import subprocess
import sys
import time
import urllib.request
from exact_build_guard import verify_exact_build, PRODUCT_BINARIES

ROOT = Path("/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-exact-source-d349-diagnostics")
OUT = Path(__file__).resolve().parent
SOURCE = "d349a7bdd2ca8f6bd4683fbccbf83010d3f9cd3e"
RUN_ID = "chat-20261021T214505Z-00299e37-rehearsal-1"
BASE = 25000
TOTAL_SECONDS = 600
TRAFFIC_SECONDS = 120
owned = None
started_mono = time.monotonic()
started_wall = datetime.datetime.now(datetime.timezone.utc)
events = []
stop_sent = False
stop_at = None
barrier = None
diagnostic_seen = None
terminal_seen = False
reason = "not_started"
primary_error = None
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def now():
    return datetime.datetime.now(datetime.timezone.utc)


def write(name, data):
    destination = OUT / name
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(destination.name + ".next")
    temporary.write_text(json.dumps(data, indent=2) + "\n")
    temporary.replace(destination)


def event(code, **fields):
    row = {"event": code, "elapsed_seconds": time.monotonic() - started_mono, "at_utc": now().isoformat(), **fields}
    events.append(row)
    write("supervisor-events.json", events)
    print(json.dumps(row), flush=True)


def capture(command, timeout=10):
    value = subprocess.run(command, cwd=ROOT, text=True, capture_output=True, timeout=timeout)
    return {"exit_status": value.returncode, "stdout": value.stdout[:65536], "stderr": value.stderr[:4096]}


def group_members(group):
    rows = subprocess.run(["ps", "-axo", "pid=,pgid=,comm="], capture_output=True, text=True).stdout.splitlines()
    members = []
    for row in rows:
        fields = row.strip().split(None, 2)
        if len(fields) == 3 and int(fields[1]) == group:
            members.append({"pid": int(fields[0]), "process": Path(fields[2]).name})
    return members


def coordinator_pid():
    path = OUT / "pids" / "coordinator.pid"
    if not path.exists():
        return None
    value = int(path.read_text().strip())
    if owned is None or os.getpgid(value) != owned.pid:
        raise RuntimeError("coordinator ownership mismatch")
    return value


def stop(code):
    global stop_sent, stop_at, reason
    if stop_sent:
        return
    reason, stop_sent, stop_at = code, True, time.monotonic()
    (OUT / "evidence/profile-stop").write_text(code + "\n")
    pid = coordinator_pid()
    if pid is None:
        os.killpg(owned.pid, signal.SIGTERM)
        event("startup_stop_group", reason=code)
    else:
        os.kill(pid, signal.SIGTERM)
        event("coordinator_term_once", reason=code)


def samples():
    raw = subprocess.run(["ps", "-axo", "pid=,pgid=,pcpu=,rss=,comm="], capture_output=True, text=True).stdout
    row = {"at_utc": now().isoformat(), "elapsed_seconds": time.monotonic() - started_mono,
        "owned": [], "other_wukongim_processes": [], "aggregate_process_cpu_percent": 0}
    for line in raw.splitlines():
        fields = line.strip().split(None, 4)
        if len(fields) != 5:
            continue
        pid, group, cpu, rss, name = int(fields[0]), int(fields[1]), float(fields[2]), int(fields[3]), Path(fields[4]).name
        row["aggregate_process_cpu_percent"] += cpu
        safe = {"pid": pid, "process": name, "cpu_percent": cpu, "rss_bytes": rss * 1024}
        if owned and group == owned.pid:
            row["owned"].append(safe)
        elif name in ("wukongim", "wkcli"):
            row["other_wukongim_processes"].append(safe)
    with (OUT / "host-process-samples.jsonl").open("a") as stream:
        stream.write(json.dumps(row) + "\n")
    return row


def check_ports():
    listeners = []
    for port in range(BASE, BASE + 64):
        with socket.socket() as conn:
            conn.settimeout(0.05)
            if conn.connect_ex(("127.0.0.1", port)) == 0:
                listeners.append(port)
    return listeners


def bounded_metrics(label):
    destinations = []
    for node in (1, 2, 3):
        path = OUT / "evidence" / ("metrics-%s-node%d.prom" % (label, node))
        try:
            req = urllib.request.Request("http://127.0.0.1:%d/metrics" % (BASE + node), headers={"Accept-Encoding": "identity"})
            with opener.open(req, timeout=3) as response:
                body = response.read((4 << 20) + 1)
                if len(body) > 4 << 20:
                    raise ValueError("metric bound exceeded")
            path.write_bytes(body)
            destinations.append({"node": node, "complete": True, "bytes": len(body), "sha256": hashlib.sha256(body).hexdigest()})
        except Exception as error:
            destinations.append({"node": node, "complete": False, "error_class": type(error).__name__})
    write("evidence/metrics-%s-receipt.json" % label, destinations)


try:
    exact_build = verify_exact_build()
    assert hashlib.sha256((OUT / "original-distribution-local.yaml").read_bytes()).hexdigest() == "308ecc916ed701b1d7b2f47e15c32f2bd8aa1b1ec43b42ec98dfe58a95a52694", "controlled_config_hash_mismatch"
    assert not any((OUT / name).exists() for name in ("data", "workers", "pids", "report"))
    assert resource.getrlimit(resource.RLIMIT_NOFILE)[0] >= 30000
    assert not check_ports()
    # A held local bind tests the entire selected range before releasing it.
    reservations = []
    try:
        for port in range(BASE, BASE + 64):
            listener = socket.socket()
            listener.bind(("127.0.0.1", port))
            reservations.append(listener)
    finally:
        for listener in reservations:
            listener.close()
    (OUT / "evidence").mkdir(exist_ok=True)
    (OUT / "logs").mkdir(exist_ok=True)
    env = {key: value for key, value in os.environ.items() if not key.startswith("WK_") and key not in ("GOFAIL_HTTP", "GOFAIL_FAILPOINTS", "GOMAXPROCS", "GODEBUG")}
    env.update({"GOWORK": "off", "GOMAXPROCS": "1"})
    assert time.monotonic() - started_mono <= 60
    initial = samples()
    assert not initial["other_wukongim_processes"]
    preflight = {"at_utc": now().isoformat(), "source_revision": SOURCE, "os": sys.platform, "fd_limit": resource.getrlimit(resource.RLIMIT_NOFILE),
        "exact_build": exact_build, "memory": capture(["memory_pressure", "-Q"]), "swap": capture(["sysctl", "vm.swapusage"]),
        "capabilities": capture(["sysctl", "hw.memsize", "hw.ncpu", "hw.model"]), "disk": capture(["df", "-Pk", str(OUT)]),
        "binary_sha256": {name: hashlib.sha256(PRODUCT_BINARIES[name].read_bytes()).hexdigest() for name in ("wukongim", "wkcli")},
        "run_id": RUN_ID, "profile": "local", "original_workload_unchanged": False, "controlled_send_rate_per_second": 500, "derived_max_global_burst": 1000, "burst_credit_seconds": 2, "formal_or_performance_acceptance": False, "cloud_capacity_qualified": False,
        "service_gomaxprocs": 2, "worker_coordinator_observer_gomaxprocs": 1, "all_observers_share_one_host_and_disk": True}
    write("preflight.json", preflight)
    env.update({"WK_BENCH_API_TOKEN": secrets.token_urlsafe(32), "WK_BENCH_WORKER_TOKEN": secrets.token_urlsafe(32),
        "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_CLUSTER_ID": "local-repro-full-20261023-v1",
        "LOCAL_REPRO_DEADLINE_MONO": str(started_mono + 570)})
    with (OUT / "logs/launcher.log").open("w") as log:
        owned = subprocess.Popen(["bash", str(OUT / "launcher.sh")], cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        event("launcher_started", pgid=owned.pid)
        last_sample = 0
        while owned.poll() is None:
            elapsed = time.monotonic() - started_mono
            terminal_path = OUT / "evidence/coordinator-exit-status"
            terminal = terminal_path.exists()
            if terminal and not terminal_seen:
                terminal_seen = True
                event("coordinator_terminal_services_held" if terminal_path.read_text().strip() == "7" else "coordinator_terminal",
                    coordinator_exit_status=int(terminal_path.read_text().strip()))
                bounded_metrics("terminal")
            if abs((now() - started_wall).total_seconds() - elapsed) > 1:
                stop("clock_confounded")
            status_path = OUT / "report/diagnostic-status.json"
            if status_path.exists() and not terminal:
                body = status_path.read_bytes()
                if len(body) > 64 << 10:
                    stop("diagnostic_status_bound_exceeded")
                status = json.loads(body)
                assert status["schema"] == "wukongim/chat-lifecycle-diagnostic-status/v1" and status["run_id"] == RUN_ID
                if barrier is None:
                    assert status["stage"] == "measured" and status["totals"]["target"] == status["totals"]["online"] == status["totals"]["traffic_ready"] == 10000
                    began = datetime.datetime.fromisoformat(status["started_at"].replace("Z", "+00:00"))
                    barrier = started_mono + (began - started_wall).total_seconds()
                    assert started_mono <= barrier <= time.monotonic()
                    write("evidence/first-measured-status.json", status)
                    event("all_user_first_grant_barrier", original_started_at=status["started_at"], target=10000)
                    bounded_metrics("first-measured")
                if status.get("updated_at") != diagnostic_seen:
                    diagnostic_seen = status.get("updated_at")
                    with (OUT / "diagnostic-cuts.jsonl").open("a") as stream:
                        stream.write(json.dumps(status) + "\n")
                if not stop_sent and time.monotonic() - barrier >= TRAFFIC_SECONDS:
                    stop("diagnostic_120_seconds_elapsed")
                    bounded_metrics("after-stop-request")
            if not terminal and barrier is None and elapsed >= 360:
                stop("all_user_barrier_budget_exhausted")
            if not terminal and elapsed >= 480:
                stop("measured_window_budget_exhausted")
            if elapsed - last_sample >= 5:
                cut = samples()
                if cut["other_wukongim_processes"]:
                    stop("foreign_workload_host_confounded")
                last_sample = elapsed
            if stop_sent and time.monotonic() - stop_at >= 90:
                event("graceful_stop_budget_exhausted")
                os.killpg(owned.pid, signal.SIGTERM)
                break
            if elapsed >= 570:
                event("hard_cleanup_boundary")
                os.killpg(owned.pid, signal.SIGTERM)
                break
            time.sleep(0.25)
        try:
            owned.wait(timeout=min(20, max(1, 595 - (time.monotonic() - started_mono))))
        except subprocess.TimeoutExpired:
            event("owned_group_kill")
            os.killpg(owned.pid, signal.SIGKILL)
            owned.wait(timeout=5)
    if reason == "not_started":
        reason = "coordinator_or_launcher_exited_naturally"
except Exception as error:
    primary_error = type(error).__name__
    reason = "launcher_or_evidence_failure"
    event("supervisor_error", error_class=primary_error)
finally:
    if owned is not None:
        remaining = group_members(owned.pid)
        if remaining:
            try:
                os.killpg(owned.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            deadline = min(started_mono + 595, time.monotonic() + 15)
            while group_members(owned.pid) and time.monotonic() < deadline:
                time.sleep(0.25)
            if group_members(owned.pid):
                try:
                    os.killpg(owned.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            try:
                owned.wait(timeout=3)
            except subprocess.TimeoutExpired:
                pass
        remaining = group_members(owned.pid)
    else:
        remaining = []
    listeners = check_ports()
    cleanup = {"at_utc": now().isoformat(), "owned_pgid": None if owned is None else owned.pid,
        "remaining_processes": remaining, "remaining_listeners": listeners, "tested_ports": list(range(BASE, BASE + 64)),
        "complete": not remaining and not listeners, "elapsed_seconds": time.monotonic() - started_mono}
    write("cleanup-receipt.json", cleanup)
    final_path = OUT / "report/final.json"
    try:
        final = json.loads(final_path.read_text()) if final_path.exists() else None
    except Exception as error:
        final = None
        primary_error = primary_error or type(error).__name__
    result = {"schema": "wukongim/original-distribution-native-diagnostic/v1", "source_revision": SOURCE,
        "started_at_utc": started_wall.isoformat(), "finished_at_utc": now().isoformat(),
        "reason": reason, "supervisor_error_class": primary_error, "launcher_exit_status": None if owned is None else owned.returncode,
        "measured_barrier_reached": barrier is not None, "requested_traffic_seconds": TRAFFIC_SECONDS,
        "observed_traffic_seconds": None if final is None else final.get("window", {}).get("elapsed", 0) / 1e9,
        "supervisor_stop_requested_after_barrier_seconds": None if barrier is None or stop_at is None else max(0, stop_at - barrier),
        "local_profile_only": True, "cloud_capacity_qualified": False, "product_source_changed": True,
        "controlled_send_rate_per_second": 500, "derived_max_global_burst": 1000, "formal_or_performance_acceptance": False,
        "final_report_present": final is not None, "original_verdict": None if final is None else final.get("verdict"),
        "messages": None if final is None else final.get("messages"), "correlation": None if final is None else final.get("correlation"),
        "cleanup": cleanup}
    write("diagnostic-result.json", result)
    print(json.dumps(result), flush=True)
