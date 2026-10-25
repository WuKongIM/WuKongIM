# Chat lifecycle configuration

`formal.yaml` is the reviewed four-host, 72-hour qualification profile.
`rehearsal.yaml` differs only in `run_id` and `stage`; it runs the unchanged
full-scale workload for exactly two hours after the all-user full-sync and
first-grant barrier, and can produce only `rehearsal_pass`, never formal
evidence. Replace every `.invalid` address and the `run_id`; do not place
credentials in YAML. `local-shakeout.yaml` keeps the same 12 logical Slot Raft Groups, 256
physical hash slots, replicas 3/3, real TCP traffic, and zero-coverage paginated
conversation sync at smaller scale. Its native-process default keeps 2,500
simultaneously online users, 500 fixed groups including one 100,000-member
group, and 500 SEND/s with a two-second, 1,000-message burst budget. The former
100 SEND/s default could not admit its mandatory bootstrap conversation bursts
before the unchanged five-minute eligibility window. A fixed-clock regression
checks the configured 500 SEND/s grant budget through that boundary for all three
workers; the Linux regression also uses 500 SEND/s. Neither check establishes a
particular developer workstation's sustained capacity: the full local shakeout
must still run there with the unchanged 400 ms hot SENDACK P99 and correctness
thresholds. Three service nodes and three workers share one local filesystem,
which is not representative of the four-host cloud topology. It is not formal
evidence.

Reports use schema `wukongim/chat-lifecycle-report/v6` and threshold contract
`wukongim/chat-lifecycle-thresholds/v2`. `qualification.scope` distinguishes
`local_diagnostic`, `rehearsal`, and `formal_protocol`. A formal-protocol report
still needs matching external Linux deployment and independent-host/disk
receipts; its profile alone does not prove the operating system or hardware.
Old reports remain historical evidence and are not accepted as a new capacity
continuation checkpoint.

Latency warmup is ten minutes; resource-trend warmup remains two hours in the
formal and rehearsal profiles. The first valid cumulative cut after latency
warmup is a baseline, not a measured interval. Positive qualification requires
a complete rolling ten-minute interval and at least 1,000 observations for each
of hot SENDACK, cold SENDACK, and full-sync attempts, with a final cut no older
than five seconds. This count floor resolves the p99.9 rank; it is not a
statistical confidence guarantee. Samples count valid cumulative deltas ending
within the window, without interpolating individual events at its boundary.
Gaps longer than ten minutes reset positive qualification. Resource-only cuts
do not refresh latency evidence. Numerical latency limits and their five-minute
sustained-breach rule are unchanged, as are correctness and resource gates.
Short diagnostic runs can finish without qualifying; they cannot produce a
rehearsal pass or formal pass from missing latency evidence.

Messages separately report `first_attempt_deadlines`,
`first_attempt_deadline_resolved`, and `first_attempt_timeouts`.
Confirmed timeouts count the first attempt's own transport-observed ACK/error
at or after its deadline, excluding local admission errors. They are a lower
bound: an unobserved first result, cancellation, receipt expiry, or invalid
clock leaves unresolved coverage. Show `deadlines - resolved` alongside the
count; zero confirmed timeouts with incomplete coverage does not establish
that every first attempt met its deadline. Retry attempts, explicit product
first-attempt failures, and confirmed timeouts are different counters and must
not be summed. No new timeout-rate gate is introduced.

The reviewed empty-dataset bootstrap rate is one global 100 logins/second until
all 10,000 users are simultaneously online. This does not bypass startup work:
each login still completes WKProto CONNECT/CONNACK and a fresh version-zero full
conversation sync. The deterministic three-worker churn model reaches the barrier in 101
seconds and must remain within 15 minutes. Missed or unused per-step credit is
discarded rather than caught up in a burst. Immutable 34/33/33 worker shares keep
subsecond skew, including UTC-second boundary skew, within the global 100-login
ceiling; coordinator-controlled workers stay all-new at their local shares
until the first global grant moves all three to the unchanged
250,000-new-user/day 80/20 stream.

Supply secrets through exactly one source per credential:

```bash
export WK_BENCH_API_TOKEN='...'
export WK_BENCH_WORKER_TOKEN='...'
```

or owner-only files (`chmod 600`):

```bash
export WK_CHAT_LIFECYCLE_BENCH_TOKEN_FILE=/secure/bench.token
export WK_CHAT_LIFECYCLE_WORKER_TOKEN_FILE=/secure/worker.token
```

Run fixed pressure with:

```bash
wkcli bench soak chat-lifecycle \
  --config configs/wkbench/chat-lifecycle/formal.yaml \
  --output-dir /secure/reports/chat-lifecycle
```

Use `rehearsal.yaml` only through the reviewed cloud rehearsal orchestration or
an equivalent remote systemd owner. It intentionally retains the formal
six-hour, 24-hour, and 72-hour thresholds in its report while warning that
those longer windows are incomplete.

For capacity mode, copy the formal file, set `mode: capacity`, and set
`capacity.aged_checkpoint` to the completed, passing 72-hour report reference,
with `completed: true`, `passed: true`, and `duration: 72h`. Keep the three
service processes and their data directories running, then invoke:

```bash
wkcli bench capacity chat-lifecycle \
  --config /secure/config/chat-lifecycle-capacity.yaml \
  --checkpoint /secure/reports/chat-lifecycle/final.json \
  --output-dir /secure/reports/chat-lifecycle-capacity
```

The 24-hour qualification files are continuous checkpoints; they neither stop
nor reassign workers. The final JSON and Markdown reports are written
atomically. A first signal requests a coordinated terminal cut and bounded
drain; a second signal forces process exit.

The formal cloud layout uses four hosts: three service/data hosts and one
load/coordinator/monitor host running all three worker processes. Each service
host exposes its own filesystem metrics selector and starts with at least
500,000,000,000 usable bytes on the selected data filesystem. The 5-percent
free-space threshold is a hard coordinated stop.

For a bounded local native-process check, use:

```bash
scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh \
  --run-dir "$PWD/tmp/chat-lifecycle-shakeout" \
  --stop-after 120
```

The run directory must be absent or empty. The local profile requires at least
10,000,000,000 bytes on both the service-data and load-host filesystems while
retaining the 5-percent free-space stop. A smaller or fuller filesystem fails
preflight, and no local result is formal evidence.
