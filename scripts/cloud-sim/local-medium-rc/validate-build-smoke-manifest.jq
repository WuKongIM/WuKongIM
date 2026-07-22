def lower_hex($width):
  type == "string" and length == $width and test("^[0-9a-f]+$");

(keys | sort) == [
  "adapters", "baseline_lock", "baseline_source_sha", "benchmarks", "binaries",
  "candidate_source_sha", "common_workload", "equivalence", "schema", "shape",
  "status", "toolchain"
] and
.schema == "wukongim/local-medium-rc-revision-neutral-build-smoke/v1" and
.status == "passed" and
(.baseline_source_sha | lower_hex(40)) and
(.candidate_source_sha | lower_hex(40)) and
.baseline_source_sha != .candidate_source_sha and
(.baseline_lock | keys | sort) == ["baseline_source_sha", "path", "schema", "sha256", "source_run_identity"] and
.baseline_lock.path == "scripts/cloud-sim/local-medium-rc/baseline-lock.json" and
.baseline_lock.schema == "wukongim/local-medium-rc-baseline-lock/v1" and
.baseline_lock.baseline_source_sha == .baseline_source_sha and
(.baseline_lock.source_run_identity | type == "string" and test("^gh-[0-9]+-[0-9]+$")) and
(.baseline_lock.sha256 | lower_hex(64)) and
(.toolchain | keys | sort) == ["goroot", "gotoolchain", "path", "sha256", "tools", "version"] and
.toolchain.gotoolchain == "local" and
(.toolchain.path | type == "string" and length > 0) and
(.toolchain.version | type == "string" and test("^go version go[^ ]+ (darwin|linux)/[^ ]+$")) and
(.toolchain.sha256 | lower_hex(64)) and
(.toolchain.goroot | keys | sort) == ["gohostarch", "gohostos", "path"] and
(.toolchain.goroot.path | type == "string" and startswith("/")) and
(.toolchain.goroot.gohostos | type == "string" and test("^[a-z0-9]+$")) and
(.toolchain.goroot.gohostarch | type == "string" and test("^[a-z0-9]+$")) and
(.toolchain.tools | keys | sort) == ["asm", "compile", "link"] and
all(.toolchain.tools | to_entries[];
  (.value | keys | sort) == ["path", "sha256"] and
  (.value.path | type == "string" and startswith("/")) and
  (.value.sha256 | lower_hex(64))) and
.common_workload.path == "scripts/cloud-sim/local-medium-rc/workload_test.go.overlay" and
(.common_workload | keys | sort) == ["path", "sha256"] and
(.common_workload.sha256 | lower_hex(64)) and
(.adapters | keys | sort) == ["baseline", "candidate"] and
.adapters.baseline.path == "scripts/cloud-sim/local-medium-rc/adapter_baseline_test.go.overlay" and
.adapters.candidate.path == "scripts/cloud-sim/local-medium-rc/adapter_candidate_test.go.overlay" and
all(.adapters[]; (. | keys | sort) == ["path", "sha256"] and (.sha256 | lower_hex(64))) and
.shape == {physical_hash_slots:256,logical_slots:10,recipients:512,target_groups:221,online_routes:55,payload_bytes:256} and
.benchmarks == [
  "BenchmarkLocalMediumRCRevisionNeutralAuthorityResolve512x256",
  "BenchmarkLocalMediumRCRevisionNeutralOwnerPushAck512x221x55"
] and
(.binaries | keys | sort) == ["baseline", "candidate"] and
.binaries.baseline.path == "bin/app-baseline.test" and
.binaries.candidate.path == "bin/app-candidate.test" and
all(.binaries[]; (. | keys | sort) == ["path", "sha256"] and (.sha256 | lower_hex(64))) and
(.equivalence | keys | sort) == ["baseline", "candidate"] and
.equivalence.baseline.path == "equivalence/baseline.txt" and
.equivalence.candidate.path == "equivalence/candidate.txt" and
all(.equivalence[]; (. | keys | sort) == ["path", "sha256"] and (.sha256 | lower_hex(64)))
