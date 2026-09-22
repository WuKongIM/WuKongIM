#!/usr/bin/env python3
"""Bounded Linux /proc sidecar; diagnostic evidence, never a timing qualification."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import resource
import signal
import subprocess
import sys
import threading
import time

NS = 1_000_000_000
READ_CAP = 256 * 1024
OUTPUT_CAP = 32 * 1024 * 1024
THREAD_CAP = 256
SAMPLE_CAP = 901
SCHEMA = "wkrpc-host-sample/v1"
TCP = ("InSegs", "OutSegs", "RetransSegs", "InErrs", "OutRsts")
TCP_EXT = ("TCPLostRetransmit", "TCPTimeouts", "TCPSynRetrans", "TCPBacklogDrop",
           "TCPRcvQDrop", "TCPMemoryPressures", "ListenOverflows", "ListenDrops")


class Invalid(ValueError):
    pass


def read(path, cap=READ_CAP):
    with open(path, "rb") as stream:
        raw = stream.read(cap + 1)
    if len(raw) > cap:
        raise Invalid("read_cap")
    return raw


def natural(value):
    n = int(value)
    if n < 0:
        raise Invalid("negative_counter")
    return n


def parse_stat(raw):
    # comm may contain spaces and parentheses; fields after its LAST ')' are fixed.
    text = raw.decode()
    end = text.rfind(")")
    if end < 0:
        raise Invalid("stat_comm")
    fields = text[end + 1:].split()
    if len(fields) < 22:
        raise Invalid("short_stat")
    return {"start_ticks": natural(fields[19]), "state": fields[0],
            "counters": {"minflt": natural(fields[7]), "majflt": natural(fields[9]),
                         "utime_ticks": natural(fields[11]), "stime_ticks": natural(fields[12])},
            "threads": natural(fields[17])}


def parse_cpu(raw):
    rows = [line.split() for line in raw.decode().splitlines()]
    cpu = next(r for r in rows if r[0] == "cpu")
    # guest fields are already included in user/nice; do not double count them.
    if len(cpu) < 9:
        raise Invalid("short_cpu")
    result = dict(zip(("user", "nice", "system", "idle", "iowait", "irq", "softirq", "steal"),
                      map(natural, cpu[1:9])))
    result["ctxt"] = natural(next(r[1] for r in rows if r[0] == "ctxt"))
    return result


def parse_psi(raw):
    result = {}
    for line in raw.decode().splitlines():
        fields = line.split()
        if fields and fields[0] in ("some", "full"):
            result[fields[0] + "_us"] = natural(dict(x.split("=", 1) for x in fields[1:])["total"])
    if "some_us" not in result:
        raise Invalid("psi_missing_some")
    return result


def parse_pairs(raw, group, names):
    rows = [line.split() for line in raw.decode().splitlines()]
    for i in range(0, len(rows) - 1, 2):
        head, values = rows[i:i + 2]
        if head[0] == group + ":":
            if values[0] != head[0] or len(head) != len(values):
                raise Invalid("counter_columns")
            all_values = dict(zip(head[1:], values[1:]))
            return {k: natural(all_values[k]) for k in names if k in all_values}
    raise Invalid("counter_group_missing")


def parse_dev(raw):
    result = {}
    for line in raw.decode().splitlines()[2:]:
        name, values = line.split(":", 1)
        name, values = name.strip(), values.split()
        if len(values) != 16 or not name:
            raise Invalid("interface_columns")
        result[name] = {k: natural(values[i]) for k, i in
                        (("rx_bytes", 0), ("rx_packets", 1), ("rx_errors", 2), ("rx_dropped", 3),
                         ("tx_bytes", 8), ("tx_packets", 9), ("tx_errors", 10), ("tx_dropped", 11))}
    if not result:
        raise Invalid("interfaces_missing")
    if len(result) > 64:
        raise Invalid("interface_cap")
    return result


def parse_softnet(raw):
    rows = raw.decode().splitlines()
    if not rows:
        raise Invalid("softnet_missing")
    if len(rows) > 1024:
        raise Invalid("cpu_cap")
    result = {}
    for line in rows:
        values = line.split()
        if len(values) < 13:
            raise Invalid("softnet_columns")
        # Refuse older layouts without CPU identity: hotplug can shift row indexes.
        cpu = int(values[12], 16)
        if cpu in result:
            raise Invalid("duplicate_cpu")
        result[cpu] = dict(zip(("processed", "dropped", "time_squeeze"),
                              (natural(str(int(v, 16))) for v in values[:3])))
    return result


def parse_switches(raw):
    values = {}
    for line in raw.decode().splitlines():
        key, _, value = line.partition(":")
        if key in ("voluntary_ctxt_switches", "nonvoluntary_ctxt_switches"):
            values[key] = natural(value.strip())
    if len(values) != 2:
        raise Invalid("switches_missing")
    return values


def parse_schedstat(raw):
    fields = raw.split()
    if len(fields) != 3:
        raise Invalid("schedstat_columns")
    return dict(zip(("runtime_ns", "runqueue_ns", "timeslices"), map(natural, fields)))


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def reason(exc):
    # No paths, command lines, environment, or file contents in error records.
    return "errno_" + str(exc.errno) if isinstance(exc, OSError) else type(exc).__name__


def time_namespace(pid):
    try:
        return os.readlink(f"/proc/{pid}/ns/time")
    except FileNotFoundError:
        if Path("/proc/self/ns/time").exists():
            raise
        return "unavailable_on_kernel"


def identity(pid):
    base = Path("/proc") / str(pid)
    proc = parse_stat(read(base / "stat"))
    exe = os.stat(base / "exe")
    if proc["state"] == "Z":
        raise ProcessLookupError(3, "zombie")
    return (proc["start_ticks"], exe.st_dev, exe.st_ino,
            os.readlink(base / "ns/net"), time_namespace(pid))


def metadata(pid, token):
    try:
        machine = read("/etc/machine-id")
    except FileNotFoundError:
        machine = b""
    if not machine:
        machine = os.uname().nodename.encode()
    # Different time namespaces can share boot IDs but not monotonic origins.
    if time_namespace("self") != token[4]:
        raise Invalid("different_time_namespace")
    binary_hash = hashlib.sha256()
    binary_bytes = 0
    with open(f"/proc/{pid}/exe", "rb") as binary:
        while chunk := binary.read(1024 * 1024):
            binary_bytes += len(chunk)
            if binary_bytes > 256 * 1024 * 1024:
                raise Invalid("binary_cap")
            binary_hash.update(chunk)
    if identity(pid) != token:
        raise Invalid("identity_changed")
    return {"HostID": digest(machine), "BootID": digest(read("/proc/sys/kernel/random/boot_id")),
            "PID": pid, "StartTicks": token[0], "BinarySHA256": binary_hash.hexdigest(),
            "network_namespace": token[3], "time_namespace": token[4],
            "clock_ticks_per_second": os.sysconf("SC_CLK_TCK")}


def snapshot(pid, token):
    """Read fixed, bounded sources; optional failures remain explicit missing data."""
    if identity(pid) != token:
        raise Invalid("identity_changed")
    counters, missing, threads = {}, {}, {}

    def collect(label, path, parser):
        try:
            return parser(read(path))
        except (OSError, ValueError, KeyError, StopIteration, IndexError) as exc:
            if isinstance(exc, Invalid) and str(exc).endswith("_cap"):
                raise
            missing[label] = reason(exc)
            return {}

    def add(prefix, values):
        counters.update({prefix + "." + str(k): v for k, v in values.items()})

    proc = parse_stat(read(f"/proc/{pid}/stat"))
    add("process", proc["counters"])
    add("cpu", collect("cpu", "/proc/stat", parse_cpu))
    for kind in ("cpu", "memory", "io"):
        add("psi." + kind, collect("psi." + kind, "/proc/pressure/" + kind, parse_psi))
    for group, filename, names in (("Tcp", "snmp", TCP), ("TcpExt", "netstat", TCP_EXT)):
        values = collect(group, f"/proc/{pid}/net/{filename}", lambda raw: parse_pairs(raw, group, names))
        add(group, values)
        for name in names:
            if name not in values:
                missing[group + "." + name] = "unavailable"
    for iface, values in collect("interfaces", f"/proc/{pid}/net/dev", parse_dev).items():
        add("interface." + iface, values)
    for cpu, values in collect("softnet", "/proc/net/softnet_stat", parse_softnet).items():
        add("softnet." + str(cpu), values)
    sched = collect("sched_enabled", "/proc/sys/kernel/sched_schedstats",
                    lambda raw: {"enabled": natural(raw.strip())}).get("enabled")
    with os.scandir(f"/proc/{pid}/task") as entries:
        tids = []
        for entry in entries:
            if entry.name.isdigit():
                tids.append(entry.name)
                if len(tids) > THREAD_CAP:
                    raise Invalid("thread_cap")
    for tid in tids:
        base = f"/proc/{pid}/task/{tid}/"
        t = collect("thread." + tid, base + "stat", parse_stat)
        if not t:
            continue
        c = collect("switches." + tid, base + "status", parse_switches)
        if sched == 1:
            c.update(collect("schedstat." + tid, base + "schedstat", parse_schedstat))
        after = collect("thread_after." + tid, base + "stat", parse_stat)
        if not after or after["start_ticks"] != t["start_ticks"]:
            missing["thread." + tid] = "changed_during_sample"
            continue
        threads[tid] = {"start_ticks": t["start_ticks"], "counters": c}
    if identity(pid) != token:
        raise Invalid("identity_changed")
    return {"counters": counters, "threads": threads, "missing": missing,
            "gauges": {"thread_count": proc["threads"], "schedstats_enabled": sched}}


class Writer:
    def __init__(self, path):
        self.stream = os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w")
        self.bytes = 0

    def emit(self, record):
        raw = json.dumps(record, separators=(",", ":"), allow_nan=False) + "\n"
        size = len(raw.encode())
        if size > READ_CAP:
            raise Invalid("record_cap")
        # Reserve enough room to record a terminal failure if the sample cap hits.
        cap = OUTPUT_CAP if record["kind"] == "end" else OUTPUT_CAP - 4096
        if self.bytes + size > cap:
            raise Invalid("output_cap")
        self.stream.write(raw)
        self.stream.flush()
        self.bytes += size


def run(args):
    """Own at most one child; collect until its exit, a signal, or the time bound."""
    if sys.platform != "linux":
        raise Invalid("Linux_required")
    writer = Writer(args.output)  # Reserve evidence before starting any child.
    child, token, pid = None, None, args.pid
    stopped = threading.Event()
    previous_handlers = {}
    start, cpu_start = time.monotonic_ns(), time.process_time_ns()
    count, result, outcome, detail = 0, 1, "failed", None
    try:
        for sig in (signal.SIGTERM, signal.SIGINT):
            previous_handlers[sig] = signal.signal(sig, lambda *_: stopped.set())
        if args.command:
            child = subprocess.Popen(args.command, start_new_session=True)
            pid = child.pid
        token = identity(pid)
        meta = metadata(pid, token)
        writer.emit({"kind": "header", "schema": SCHEMA, "identity": meta,
                     "interval_ns": NS, "duration_limit_seconds": args.duration,
                     "diagnostic_only": True, "limits": {"read_bytes": READ_CAP, "threads": THREAD_CAP,
                     "samples": SAMPLE_CAP, "output_bytes": OUTPUT_CAP},
                     "scope": {"cpu_psi_softnet": "host", "tcp_interfaces": "target_network_namespace",
                               "thread_counters": "surviving_thread_lower_bound"}})
        slot = 0
        while True:
            now = time.monotonic_ns()
            if stopped.is_set():
                outcome, result = "interrupted", 130
                break
            if child and child.poll() is not None:
                outcome = "child_exited"
                result = 0 if child.returncode == 0 else 1
                break
            if now - start >= args.duration * NS:
                outcome, result = "duration_limit", 0 if child is None else 1
                break
            scheduled = start + slot * NS
            if now < scheduled:
                stopped.wait(min((scheduled - now) / NS, 0.1))
                continue
            if count >= SAMPLE_CAP:
                raise Invalid("sample_cap")
            before, cpu_before, wall = time.monotonic_ns(), time.process_time_ns(), time.time_ns()
            try:
                sample = snapshot(pid, token)
            except (FileNotFoundError, ProcessLookupError):
                if child:
                    child.wait(timeout=1)
                    outcome, result = "child_exited", 0 if child.returncode == 0 else 1
                else:
                    outcome, result = "target_exited", 0
                break
            after = time.monotonic_ns()
            sample.update(kind="sample", index=count, slot=slot, scheduled_ns=scheduled,
                          begin_ns=before, end_ns=after, wall_ns=wall,
                          read_cpu_ns=time.process_time_ns() - cpu_before,
                          late_ns=before - scheduled)
            writer.emit(sample)
            count += 1
            slot = max(slot + 1, (time.monotonic_ns() - start) // NS + 1)
    except (OSError, ValueError, subprocess.SubprocessError) as exc:
        detail = str(exc) if isinstance(exc, Invalid) else reason(exc)
    finally:
        # Only an owned child session may be terminated; --pid is read-only.
        if child and child.poll() is None:
            try:
                os.killpg(child.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                child.wait(timeout=1)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                child.wait(timeout=1)
        elapsed, cpu = time.monotonic_ns() - start, time.process_time_ns() - cpu_start
        end = {"kind": "end", "outcome": outcome, "detail": detail, "exit_code": result,
               "child_exit_code": child.returncode if child else None, "samples": count,
               "elapsed_ns": elapsed, "sampler_cpu_ns": cpu,
               "sampler_one_core_percent": cpu / elapsed * 100 if elapsed else None,
               "max_rss_kib": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss,
               "bytes_before_end": writer.bytes}
        try:
            writer.emit(end)
        finally:
            writer.stream.close()
            for sig, handler in previous_handlers.items():
                signal.signal(sig, handler)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    parser.add_argument("--duration", type=float, default=90)
    parser.add_argument("--pid", type=int)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.command[:1] == ["--"]:
        args.command = args.command[1:]
    if (args.pid is None) == (not args.command) or (args.pid is not None and args.pid <= 0):
        parser.error("choose --pid or -- COMMAND")
    if not math.isfinite(args.duration) or not 1 <= args.duration <= 900:
        parser.error("duration must be 1 to 900 seconds")
    try:
        return run(args)
    except (OSError, ValueError) as exc:
        print("host sampler: " + (str(exc) if isinstance(exc, Invalid) else reason(exc)), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
