# RPC connection frame observation — 2026-10-27

The first two-control execution passed. It demonstrates response-reader
starvation within one multiplexed TCP connection under an injected gate. It
does not identify the actual R14/R16 root cause, reproduce R14's request-direction
188ms gap, or qualify Linux performance or the v3 release.

## Source and controls

Baseline: `50f85fd38f8e0282a9614ceeb5c01742fd46b4a2`. The exact tested
[fixture source](artifacts/2026-10-27-rpc-frame-observation/frame_hol_mechanism_integration_test.go.txt)
is archived as text, outside package discovery and permanent CI gates:
SHA-256 `05d9d35ab866963d9a63d4254e6753621589a7509bf68e35da26329be47648c0`.
No production code, PoolSize=16, owner deadline=250ms or native gate changed.
Pinned Go 1.25.11, Darwin arm64, CGO=0, GOMAXPROCS=2; one focused invocation,
exit 0, package elapsed 0.629s. This was a shared-Mac diagnostic.

1. Hold the service-2 handler before any reply bytes exist. Service 14 on the
   same connection completes before that handler is released.
2. Fully buffer a 131,097-byte framed service-2 reply from real TCP, deliver
   only its header and first body byte to the client readLoop, then gate the
   remainder. Service 14 on the same connection times out; an already-warmed
   second connection succeeds. Release the gate and the bulk call succeeds.

Keys 1 and 17 select slot 1; key 18 selects slot 2. Actual negotiated connection
identities confirm reuse/isolation. These are manual public-transport keys,
not a dynamic test of Node.CallRPC. Service 2 is a fixture label, not evidence
of actual production Pull traffic. Gates/events establish ordering without
sleep-based timing guesses; request deadlines only bound the experiment.

## Observed same-request phases

Durations below come from matching client/server TraceIDs, in microseconds
unless marked ms. Server physical-write completion does not prove receipt or
application consumption at the peer.

| Control / owner | Client total | Client queue / request write | Server handler / response write |
| --- | ---: | ---: | ---: |
| Held handler, same slot | 61.416 | 1.500 / 4.875 | 0.459 / 3.500 |
| Gated reader, same slot | 251.227875ms, timeout | 1.583 / 5.416 | 0.375 / 3.667 |
| Gated reader, other slot | 63.542 | 1.333 / 4.042 | 0.333 / 2.375 |

The requested timeout stays 250ms; actual terminal publication includes
scheduling overrun (call envelope 251.240417ms). The gated reply reaches the
wrapper in full at fixture +0.689000ms; the gate is reached at +0.689250ms.
Same-slot owner handler entry/return is +0.727166/+0.727250ms, its client
returns timeout at +251.931208ms, and the gate releases at +251.950166ms.
That timed-out client retains no complete receive/dispatch/resume observation.
The other-slot owner returns at +0.756833ms. In the handler control, owner
returns at +1.235750ms before bulk-handler return at +1.238708ms.

All nine physical requests have exactly one client and one server sample.
Each full identity is `connection / request`; abbreviations below only name
the four actual anonymous connection generations:

- H1 = `167331f7de7a3d0293d65433f90bbe27`
- H2 = `d55c067f7031d1dd7d97af271c49238b`
- R1 = `bae94130c454ca1e9a4644591933eb22`
- R2 = `c1f13126847f7a23a139dd67e9dd3402`

| Control | Call | Connection / RequestID | Outcome |
| --- | --- | --- | --- |
| handler | warm-1 | H1 / 8000000000000003 | OK |
| handler | warm-2 | H2 / 8000000000000003 | OK |
| handler | bulk | H1 / 8000000000000004 | OK |
| handler | owner-same | H1 / 8000000000000005 | OK |
| reader | warm-1 | R1 / 8000000000000003 | OK |
| reader | warm-2 | R2 / 8000000000000003 | OK |
| reader | bulk | R1 / 8000000000000004 | OK after release |
| reader | owner-same | R1 / 8000000000000005 | timeout |
| reader | owner-other | R2 / 8000000000000004 | OK |

Both fixtures released all gates, joined Client/Server StopContext and call
owners, closed four wrapped TCP connections, and reported zero connection,
pending-RPC and queue gauges. The waited test process group was empty. No
independent per-port probe or whole-runtime goroutine census is claimed.

## Manual reproduction

From a checkout containing this report, with an installed Go 1.25.11 toolchain,
copy the archived bytes into a **fresh** detached worktree at the tested baseline.
The observed Darwin arm64 Go executable SHA-256 was
`b65655bf3d496007755bd132e9611f9e5e64c46e33d89fcafdef916e299587fd`.
Set `task_go` to that installation's absolute `bin/go` path. This recipe creates
new evidence; timing and random TraceIDs will differ from the historical run.

```sh
(
  set -eu
  task_go=/absolute/path/to/go1.25.11/bin/go
  task_replay_dir=.worktrees/rpc-frame-observation
  task_baseline=50f85fd38f8e0282a9614ceeb5c01742fd46b4a2
  if [ -e "$task_replay_dir" ] || [ -L "$task_replay_dir" ]; then
    printf '%s\n' 'Replay path already exists; use a fresh path.' >&2
    exit 1
  fi
  git worktree add --detach "$task_replay_dir" "$task_baseline"
  task_replay_head=$(git -C "$task_replay_dir" rev-parse HEAD)
  [ "$task_replay_head" = "$task_baseline" ]
  task_replay_status=$(git -C "$task_replay_dir" status --porcelain)
  [ -z "$task_replay_status" ]
  cp docs/reports/artifacts/2026-10-27-rpc-frame-observation/frame_hol_mechanism_integration_test.go.txt \
    "$task_replay_dir/pkg/transport/frame_hol_mechanism_integration_test.go"
  task_artifact_dir=$(mktemp -d)
  cd "$task_replay_dir"
  env CGO_ENABLED=0 GOMAXPROCS=2 GOFLAGS=-p=1 GOWORK=off \
    GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOENV=off \
    LANG=C LC_ALL=C TZ=UTC WK_TRANSPORT_HOL_ARTIFACT_DIR="$task_artifact_dir" \
    "$task_go" test -tags=integration ./pkg/transport \
    -run '^TestTransportPoolHOLMechanism$' -count=1 -parallel=1 -timeout=20s -v
)
```

Retain exit status/stdout and the generated `handler.json` / `reader.json` before
cleanup. Review the bounded gates, exact joins, zero-after-Stop gauges and actual
errors; a compilation/fixture failure is not a product regression RED. Do not
rerun until green or relax the pool/deadline to obtain the expected outcome.
The two cases use different bulk sizes and a wrapper that can change syscall
batching, so this is not a throughput benchmark or a response-size comparison.

## Source boundaries for diagnosis

[Owner operation 11](../../internal/access/node/presence_owner_recovery_owned.go)
uses [Node.CallRPC](../../pkg/cluster/node.go), whose default
[service key](../../pkg/cluster/net/transport.go) is service+3 except explicit
cases. [Peer Acquire](../../pkg/transport/internal/peer/manager.go) selects
key modulo pool size. Owner service 14 therefore uses key 17/slot 1 per remote
peer with the default pool of 16; it is multiplexed, not one RPC at a time.
Pull/PullBatch use key 1 too, but [default composition](../../pkg/cluster/node_defaults.go)
installs QuorumLog. The ordinary [follower pull loop](../../pkg/channel/reactor/follower_replication.go)
then exits; normal [QuorumExchange](../../pkg/cluster/channels/quorum_transport.go)
is service 86/key 89/slot 9. Static collision alone cannot identify actual load.

The [frame reader](../../pkg/transport/wire/reader.go) obtains one full frame
before dispatching the next. Async handlers and queue priorities do not make
already-selected bytes preemptible. This response-direction gate proves a
narrow mechanism, not network slowness, earlier request-direction blocking,
actual Pull contention, or a reason to increase pool/timeout limits.

Separately, the pinned Go 1.25.11
[`cmd/trace/pprof.go`](https://github.com/golang/go/blob/go1.25.11/src/cmd/trace/pprof.go#L145-L197)
filters stackless transitions before state bookkeeping, including stackless
exits; this can leave a tracked interval open. Its scheduler aggregates are
not exact contiguous runnable waits. The pinned
[`cmd/trace/main.go`](https://github.com/golang/go/blob/go1.25.11/src/cmd/trace/main.go#L270-L313)
can retain a useful prefix after a later parse error, and its `-pprof` branch
can successfully export that prefix. Require a complete reader-to-EOF result,
state continuity and edge-censoring accounting for exact state analysis. Do not
generalize these tool-source findings to other Go versions, or treat aggregate
multi-goroutine wait totals as CPU time or one capture's elapsed duration.
