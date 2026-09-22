import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import host_sample as sample
from correlate import correlate, delta, load_samples, thread_delta, strict_json

N = sample.NS


def stat(start=123, comm="worker (with) space)"):
    fields = ["S"] + ["0"] * 21
    for i, value in {7: 11, 9: 2, 11: 30, 12: 40, 17: 5, 19: start}.items():
        fields[i] = str(value)
    return ("42 (" + comm + ") " + " ".join(fields)).encode()


def fixtures(role="client"):
    host = {"OS": "linux", "HostID": "h", "BootID": "b", "PID": 42,
            "StartTicks": 123, "BinarySHA256": "x"}
    span = lambda n: {"LowNS": n, "HighNS": n + 1000}
    probe = {"Schema": "wkrpc-process-probe/v1", "Client": host,
             "Options": {"Duration": 2 * N}, "Seconds": [{"Calls": 10}, {"Calls": 20}],
             "Timeline": {"Start": span(10 * N), "BeforeRPC": span(9 * N), "AfterRPC": span(13 * N)},
             "ServerBefore": {"Host": host, "Instance": "s", "MonotonicNS": 1009 * N + 500},
             "ServerAfter": {"Host": host, "Instance": "s", "MonotonicNS": 1013 * N + 500}}
    shift = 1000 * N if role == "server" else 0
    records = [{"kind": "header", "schema": sample.SCHEMA, "diagnostic_only": True,
                "identity": host.copy(), "interval_ns": N, "scope": {}}]
    for i in range(5):
        begin = (9 + i) * N + shift
        records.append({"kind": "sample", "index": i, "slot": i,
                        "scheduled_ns": begin, "begin_ns": begin, "end_ns": begin + 100,
                        "late_ns": 0, "wall_ns": 100000 * N + i * N, "counters": {"cpu.steal": i},
                        "threads": {"42": {"start_ticks": 123, "counters": {"runqueue_ns": i * 3}}},
                        "missing": {}, "gauges": {"schedstats_enabled": 1}})
    records.append({"kind": "end", "exit_code": 0, "outcome": "child_exited", "samples": 5})
    return copy.deepcopy(probe), records


class Parsers(unittest.TestCase):
    def test_stat_unusual_comm_and_correct_fields(self):
        parsed = sample.parse_stat(stat())
        self.assertEqual(parsed, {"start_ticks": 123, "state": "S", "threads": 5,
                                  "counters": {"minflt": 11, "majflt": 2, "utime_ticks": 30, "stime_ticks": 40}})
        for raw in (b"42 worker", b"42 (x) S 0", stat().replace(b" 30 ", b" -1 ")):
            with self.assertRaises(ValueError):
                sample.parse_stat(raw)

    def test_cpu_excludes_double_counted_guest(self):
        self.assertEqual(sample.parse_cpu(b"cpu 1 2 3 4 5 6 7 8 99 99\nctxt 12\n"),
                         dict(user=1, nice=2, system=3, idle=4, iowait=5, irq=6, softirq=7, steal=8, ctxt=12))

    def test_psi_optional_full_is_missing_not_zero(self):
        self.assertEqual(sample.parse_psi(b"some avg10=0.0 avg60=0.0 avg300=0.0 total=123\n"), {"some_us": 123})
        with self.assertRaises(ValueError):
            sample.parse_psi(b"")

    def test_paired_tcp_columns_and_optional_missing(self):
        raw = b"Ip: Foo\nIp: 1\nTcp: InSegs RetransSegs\nTcp: 100 7\n"
        self.assertEqual(sample.parse_pairs(raw, "Tcp", sample.TCP), {"InSegs": 100, "RetransSegs": 7})
        with self.assertRaises(ValueError):
            sample.parse_pairs(b"Tcp: InSegs RetransSegs\nTcp: 100\n", "Tcp", sample.TCP)

    def test_interfaces(self):
        raw = b"header\nheader\n eth0: 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16\n"
        self.assertEqual(sample.parse_dev(raw)["eth0"]["tx_dropped"], 12)
        with self.assertRaises(ValueError):
            sample.parse_dev(b"header\nheader\neth0: 1 2\n")

    def test_softnet_hex_cpu_index_and_cap(self):
        raw = b"0000000a 0000000b 0000000c 0 0 0 0 0 0 0 0 0 00000002\n"
        self.assertEqual(sample.parse_softnet(raw), {2: {"processed": 10, "dropped": 11, "time_squeeze": 12}})
        with self.assertRaises(ValueError):
            sample.parse_softnet(raw * 1025)

    def test_softnet_without_cpu_identity_is_unavailable(self):
        with self.assertRaisesRegex(ValueError, "softnet_columns"):
            sample.parse_softnet(b"01 02 03 00 00 00 00 00 00 00 00")

    def test_scheduler_and_switches(self):
        self.assertEqual(sample.parse_schedstat(b"100 200 3"), dict(runtime_ns=100, runqueue_ns=200, timeslices=3))
        self.assertEqual(sample.parse_switches(b"Name: private\nvoluntary_ctxt_switches: 4\nnonvoluntary_ctxt_switches: 5"),
                         dict(voluntary_ctxt_switches=4, nonvoluntary_ctxt_switches=5))
        with self.assertRaises(ValueError):
            sample.parse_switches(b"Name: private")

    def test_file_caps_and_exclusive_output(self):
        with tempfile.TemporaryDirectory() as directory:
            p = Path(directory) / "data"
            p.write_bytes(b"abcd")
            self.assertEqual(sample.read(p, 4), b"abcd")
            with self.assertRaises(ValueError):
                sample.read(p, 3)
            with self.assertRaises(FileExistsError):
                sample.Writer(p)
            self.assertEqual(p.read_bytes(), b"abcd")
            w = sample.Writer(Path(directory) / "out")
            try:
                self.assertEqual((Path(directory) / "out").stat().st_mode & 0o777, 0o600)
                w.bytes = sample.OUTPUT_CAP - 4096
                with self.assertRaises(ValueError):
                    w.emit({"kind": "sample"})
                w.emit({"kind": "end"})
            finally:
                w.stream.close()

    def test_pid_or_exec_reuse_fails_before_read(self):
        for original, current in (((1, 2, 3, "net"), (2, 2, 3, "net")),
                                  ((1, 2, 3, "net"), (1, 2, 4, "net")),
                                  ((1, 2, 3, "net"), (1, 2, 3, "new-net"))):
            with patch.object(sample, "identity", return_value=current), patch.object(sample, "read") as read:
                with self.assertRaisesRegex(ValueError, "identity_changed"):
                    sample.snapshot(42, original)
                read.assert_not_called()

    def test_missing_reads_disabled_scheduler_and_post_read_reuse(self):
        from types import SimpleNamespace
        from contextlib import nullcontext
        token = (123, 1, 1, "net", "time")
        def read(path):
            path = str(path)
            if path.endswith("/stat") and path != "/proc/stat":
                return stat()
            if path.endswith("/status"):
                return b"voluntary_ctxt_switches: 1\nnonvoluntary_ctxt_switches: 2"
            if path.endswith("/sched_schedstats"):
                return b"0"
            if path.endswith("/schedstat"):
                self.fail("disabled schedstat read")
            raise PermissionError(13, "secret-path-must-not-leak")
        with patch.object(sample, "identity", return_value=token), patch.object(sample, "read", side_effect=read), \
             patch.object(sample.os, "scandir", return_value=nullcontext([SimpleNamespace(name="42")])):
            s = sample.snapshot(42, token)
        self.assertEqual(s["gauges"]["schedstats_enabled"], 0)
        self.assertEqual(s["threads"]["42"]["counters"], dict(voluntary_ctxt_switches=1, nonvoluntary_ctxt_switches=2))
        self.assertNotIn("Tcp.RetransSegs", s["counters"])
        self.assertEqual(s["missing"]["cpu"], "errno_13")
        self.assertNotIn("secret-path", json.dumps(s))
        with patch.object(sample, "identity", side_effect=[token, (124, 1, 1, "net", "time")]), \
             patch.object(sample, "read", side_effect=read), \
             patch.object(sample.os, "scandir", return_value=nullcontext([])):
            with self.assertRaisesRegex(ValueError, "identity_changed"):
                sample.snapshot(42, token)

    def test_metadata_rejects_different_time_namespace(self):
        with patch.object(sample, "read", return_value=b"machine"), \
             patch.object(sample, "time_namespace", return_value="other"):
            with self.assertRaisesRegex(ValueError, "different_time_namespace"):
                sample.metadata(42, (123, 1, 1, "net", "time"))


class Correlation(unittest.TestCase):
    def test_client_overlap_keeps_interval_delta_once(self):
        probe, records = fixtures()
        r = correlate(probe, records, "client")
        self.assertEqual(r["status"], "aligned")
        self.assertTrue(all(s["bracketed"] for s in r["seconds"]))
        self.assertEqual(sum(i["counters"]["delta"]["cpu.steal"] for i in r["intervals"]), 4)
        self.assertEqual(r["intervals"][1]["possible_rpc_seconds"], [0, 1])
        self.assertNotIn("counter_delta", r["seconds"][0])

    def test_cross_host_different_clock_origins(self):
        probe, records = fixtures("server")
        r = correlate(probe, records, "server")
        self.assertEqual(r["status"], "aligned")
        self.assertTrue(all(s["bracketed"] for s in r["seconds"]))
        lo, hi = r["clock"]["server_minus_client_ns"]
        self.assertLess(lo, 1000 * N)
        self.assertGreater(hi, 1000 * N)
        self.assertEqual(r["clock"]["drift_assumption_ppm"], 1000)

    def test_reject_mismatched_process_identity(self):
        for key in ("HostID", "BootID", "PID", "StartTicks", "BinarySHA256"):
            probe, records = fixtures()
            records[0]["identity"][key] = "wrong"
            with self.subTest(key=key), self.assertRaisesRegex(ValueError, "identity"):
                correlate(probe, records, "client")

    def test_missing_reversed_wide_anchors(self):
        for key, value in (("LowNS", 0), ("LowNS", 11 * N), ("HighNS", 11 * N)):
            probe, records = fixtures()
            probe["Timeline"]["Start"][key] = value
            with self.assertRaises(ValueError):
                correlate(probe, records, "client")
        probe, records = fixtures()
        del probe["Timeline"]
        with self.assertRaises(KeyError):
            correlate(probe, records, "client")

    def test_server_clock_discontinuity_rejected(self):
        probe, records = fixtures("server")
        probe["ServerAfter"]["MonotonicNS"] += N
        with self.assertRaisesRegex(ValueError, "discontinuity"):
            correlate(probe, records, "server")

    def test_small_server_drift_is_visible(self):
        probe, records = fixtures("server")
        probe["ServerAfter"]["MonotonicNS"] += 1_000_000
        r = correlate(probe, records, "server")
        self.assertNotEqual(r["clock"]["before_offset_ns"], r["clock"]["after_offset_ns"])

    def test_counter_reset_and_disappearance_never_zero_fill(self):
        d = delta({"reset": 10, "gone": 2, "ok": 3}, {"reset": 1, "new": 8, "ok": 5})
        self.assertEqual(d, {"delta": {"ok": 2}, "reset": ["reset"], "missing_before": ["new"], "missing_after": ["gone"]})
        with self.assertRaises(ValueError):
            delta({"x": -1}, {"x": 2})

    def test_thread_reuse_excludes_both_incarnations(self):
        before = {"1": {"start_ticks": 1, "counters": {"runqueue_ns": 9}},
                  "2": {"start_ticks": 2, "counters": {"runqueue_ns": 1}}}
        after = {"1": {"start_ticks": 3, "counters": {"runqueue_ns": 50}},
                 "2": {"start_ticks": 2, "counters": {"runqueue_ns": 4}}}
        t = thread_delta(before, after)
        self.assertEqual(t["surviving_thread_delta_lower_bound"], {"runqueue_ns": 3})
        self.assertEqual(t["removed_or_reused"], ["1"])
        self.assertEqual(t["added_or_reused"], ["1"])
        self.assertFalse(t["complete_observation"])

    def test_disabled_missing_metrics_and_sampler_failure_stay_partial(self):
        probe, records = fixtures()
        records[2]["gauges"]["schedstats_enabled"] = 0
        records[2]["missing"] = {"Tcp": "errno_13"}
        records[-1].update(exit_code=1, outcome="failed")
        r = correlate(probe, records, "client")
        self.assertEqual(r["status"], "partial")
        self.assertIn("missing_metrics", r["issues"])
        self.assertIn("schedstats_disabled_or_unavailable", r["issues"])
        self.assertIn("sampler_failed:failed", r["issues"])

    def test_missing_tail_is_incomplete(self):
        probe, records = fixtures()
        del records[-3:-1]
        records[-1]["samples"] = 3
        r = correlate(probe, records, "client")
        self.assertFalse(r["seconds"][-1]["bracketed"])
        self.assertIn("incomplete_time_coverage", r["issues"])

    def test_gap_and_late_samples(self):
        for mode in ("gap", "late"):
            probe, records = fixtures()
            if mode == "gap":
                del records[2]
                for i, s in enumerate(records[1:-1]):
                    s["index"] = i
                records[-1]["samples"] = 4
            else:
                records[2]["late_ns"] = N // 2
                records[2]["scheduled_ns"] -= N // 2
            r = correlate(probe, records, "client")
            self.assertIn("incomplete_time_coverage", r["issues"])

    def test_wall_jump_does_not_shift_monotonic_mapping(self):
        probe, records = fixtures()
        expected = correlate(probe, records, "client")["seconds"]
        records[2]["wall_ns"] -= 50 * N
        r = correlate(probe, records, "client")
        self.assertEqual(expected, r["seconds"])
        self.assertIn("wall_clock_jump_monotonic_alignment_retained", r["issues"])

    def test_nonfinite_json_is_invalid(self):
        for raw in ('{"n":NaN}', '{"n":Infinity}', '{"n":1e400}'):
            with self.assertRaises(ValueError):
                strict_json(raw)

    def test_terminal_truncation_or_wrong_sample_count_rejected(self):
        probe, records = fixtures()
        with self.assertRaises(ValueError):
            correlate(probe, records[:-1], "client")
        records[-1]["samples"] = 40
        with self.assertRaises(ValueError):
            correlate(probe, records, "client")

    def test_loader_rejects_truncated_and_large_records(self):
        with tempfile.TemporaryDirectory() as directory:
            p = Path(directory) / "samples"
            for raw in ('{"kind":"header"}', 'x' * (sample.READ_CAP + 1)):
                p.write_text(raw)
                with self.assertRaises(ValueError):
                    load_samples(p)
            _, records = fixtures()
            p.write_text("".join(json.dumps(r) + "\n" for r in records))
            self.assertEqual(load_samples(p), records)


if __name__ == "__main__":
    unittest.main()
