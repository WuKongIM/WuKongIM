# first_person_history AGENTS

Reproduce issue #1066 with real HTTP sends, online WKProto receipt, reconnect,
and both users' legacy conversation and history queries. Use 256 hash slots.
Capture immediate visibility separately from eventual visibility and restart
survival; never equate an empty membership-gated read with missing storage.

Run with `WK_E2E_1066_REPORT=/absolute/report.json GOWORK=off go test -tags=e2e ./test/e2e/message/first_person_history -count=1 -timeout=5m -v`.
Set `WK_E2E_1066_NODES=3 WK_E2E_1066_PAIRS=1` for a minimal three-node cluster case; the default is a single-node
cluster. `WK_E2E_1066_REQUIRE_GAP=1` requires at least one immediate visibility
gap as a positive reproduction signal. The default is one pair / one worker; set
`WK_E2E_1066_PAIRS=128` for 16-worker stress. The report retains at most
8,192 observations with an explicit drop count. Controls check HTTP 200 with
business rejection, top-level `no_persist` overriding header zero, and the
rejection of manual person subscriber mutation. Keep all assertions process-level and
black-box, and preserve requests, responses, metrics, and stage timestamps in
the report. No product instrumentation or storage inspection is allowed.

Immediate visibility is required by default and turns red on the original
symptom. Set `WK_E2E_1066_REQUIRE_IMMEDIATE=0` only for a baseline diagnostic.
`WK_E2E_1066_CRASH_AFTER_ACK=1`
requires one pair and one node: after the immediate reads it kills the exact
fixture process, then proves acknowledged-message recovery through public APIs.
This is process-crash coverage, not a physical power-loss test.

`WK_E2E_1066_ALLOW_READ_UNAVAILABLE=1` is an explicit stress-only mode: record
initial exact read-route HTTP 503 responses separately and still require eventual
complete history and restart survival. Never count these as immediate successful
reads or confuse them with a successful empty response. The strict default does
not allow them. `WK_E2E_1066_HOT=1` adds 128 warmup sends and 4096 measured HTTP
sends with 16 workers, P50/P99/QPS and a zero-membership-write assertion; alternate
frozen baseline/candidate binaries on the same host and retain JSON reports.

Set `WK_E2E_1066_HOT_MESSAGES=16384` for a longer comparison (maximum 65536).
`WK_E2E_1066_PROFILE=/absolute/cpu.pprof` enables the loopback debug API only in
that fixture and captures a bounded ten-second CPU profile during measured work;
compare profiled runs separately from unprofiled timing results.

Hot reports snapshot all fixture nodes outside the timed interval and retain at
most 128 fixed stage/read-barrier count/sum deltas, including remote membership
writes. Profiled runs must be compared separately from unprofiled timings.
Hot warmup alone may retry the exact public recovery 503 with an unchanged key
and body using the suite helper; measured sends never retry. Before/after public
Manager runtime layouts make Channel/Slot placement changes explicit.
