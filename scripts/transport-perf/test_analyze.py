import copy
import datetime
import unittest

from analyze import analyze


def fixtures():
    host = {"OS": "linux", "Arch": "amd64", "KernelArch": "x86_64", "Container": False,
            "Ineligible": [], "CPUCount": 4, "Procs": 4, "CPUModel": "test-cpu",
            "HostID": "client", "AllowedCPUs": "0-3", "Source": "a" * 40,
            "BinarySHA256": "b" * 64, "GoVersion": "go1.25.11",
            "MemoryLimit": 2**63-1, "RuntimeDebug": ""}
    server = dict(host, HostID="server")
    options = {"Mode": "client", "Duration": 20_000_000_000, "Warmup": 10_000_000_000,
               "Workers": 16, "Shards": 1, "Bytes": 64, "Samples": 1_000_000,
               "GC": 400, "Budgets": False, "Read": False}
    limits = {"Concurrency": 64, "QueueSize": 4096, "MaxQueueBytes": 64 << 20,
              "MaxRetainedBytes": 128 << 20, "MaxPayload": 65537,
              "Timeout": 30_000_000_000, "QueueTimeout": 5_000_000_000, "CancelRunning": False}
    stats = {"TotalAlloc": 10, "Mallocs": 10, "PauseNS": 10, "NumGC": 1, "CPUSeconds": 1}
    state = {"Host": server, "Instance": "one", "Options": limits, "GC": 100,
             "EchoCalls": 0, "Stats": stats}
    r = {"Schema": "wkrpc-process-probe/v1", "Client": host, "Options": options,
         "Summary": {"Calls": 320000, "P99MS": .2}, "ElapsedSeconds": 20, "CallsPerSecond": 16000,
         "Errors": 0, "SampleCapHit": False, "WorkerCalls": [20000] * 16,
         "WarmupCalls": [10000] * 16, "Seconds": [{"Calls": 16000, "P99MS": .2}] * 20,
         "ClientBefore": stats, "ClientAfter": stats, "ServerBefore": state,
         "ServerAfter": dict(state, EchoCalls=320000)}
    rows = [copy.deepcopy(r) for _ in range(6)]
    start = datetime.datetime(2026, 1, 1, tzinfo=datetime.timezone.utc)
    for i, row in enumerate(rows):
        row["StartedUTC"] = (start + datetime.timedelta(seconds=30*i)).isoformat()
    return rows


class RepeatabilityTests(unittest.TestCase):
    def test_stable_batch(self):
        self.assertEqual(analyze(fixtures())["status"], "passed")

    def test_p99_instability_is_not_a_pass(self):
        rows = fixtures()
        rows[-1]["Summary"]["P99MS"] = .3
        self.assertEqual(analyze(rows)["status"], "unstable")

    def test_fail_closed_on_invalid_evidence(self):
        mutations = [lambda r: r[0].update(SampleCapHit=True),
                     lambda r: r[0]["Client"].update(Container=True),
                     lambda r: r[0]["Client"].update(Arch="arm64"),
                     lambda r: r[0]["Client"].update(MemoryLimit=1_000_000),
                     lambda r: r[0]["Summary"].update(P99MS=float("nan")),
                     lambda r: r[0].update(WorkerCalls=[1]*16),
                     lambda r: r[0]["ServerAfter"].update(EchoCalls=320001),
                     lambda r: r[0]["Options"].update(GC=100),
                     lambda r: r[0]["ServerAfter"].update(GC=400),
                     lambda r: r[0]["Client"].update(BinarySHA256="c"*64),
                     lambda r: r[0]["ServerAfter"].update(Instance="restarted"),
                     lambda r: r[1].update(StartedUTC=r[0]["StartedUTC"]),
                     lambda r: r[0]["ServerBefore"]["Host"].update(HostID="client")]
        for mutate in mutations:
            rows = fixtures()
            mutate(rows)
            with self.subTest(mutation=mutate):
                self.assertEqual(analyze(rows)["status"], "invalid")
        self.assertEqual(analyze(fixtures()[:5])["status"], "invalid")
        self.assertEqual(analyze([{}]*6)["status"], "invalid")


if __name__ == "__main__":
    unittest.main()
