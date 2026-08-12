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
group, and 100 SEND/s. This is the highest sustained profile validated on the
shared developer workstation; 5,000 users completed login and sync but made
the three service nodes and three workers contend on one local filesystem,
which is not representative of the four-host cloud topology. It is not formal
evidence.

The reviewed empty-dataset bootstrap rate is one global 25 logins/second until
all 10,000 users are simultaneously online. This does not bypass startup work:
each login still completes WKProto CONNECT/CONNACK and a fresh version-zero full
conversation sync. The deterministic three-worker churn model reaches the barrier in 421
seconds and must remain within 15 minutes. Missed or unused per-step credit is
discarded rather than caught up in a burst. Immutable 9/8/8 worker shares keep
subsecond skew, including UTC-second boundary skew, within the global 25-login
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
wkbench soak chat-lifecycle \
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
wkbench capacity chat-lifecycle \
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

The run directory must be absent or empty. A local filesystem smaller than the
configured minimum, or already below the 5-percent reserve, is expected to fail
preflight and is not formal evidence.
