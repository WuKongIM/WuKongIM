# Chat lifecycle configuration

`formal.yaml` is the reviewed seven-host, 72-hour qualification profile.
Replace every `.invalid` address and the `run_id`; do not place credentials in
YAML. `local-shakeout.yaml` keeps the same 12 logical Slot Raft Groups, 256
physical hash slots, replicas 3/3, real TCP traffic, and version-zero full
conversation sync at smaller scale. It is not formal evidence.

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

The formal layout uses seven independent hosts: three service/data hosts,
three worker hosts, and one coordinator host. Each service host exposes its own
filesystem metrics selector and starts with at least 1,000,000,000,000 bytes on
the selected data filesystem. The 5-percent free-space threshold is a hard
coordinated stop.

For a bounded local native-process check, use:

```bash
scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh \
  --run-dir "$PWD/tmp/chat-lifecycle-shakeout" \
  --stop-after 120
```

The run directory must be absent or empty. A local filesystem smaller than the
configured minimum, or already below the 5-percent reserve, is expected to fail
preflight and is not formal evidence.
