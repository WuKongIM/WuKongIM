# conversation_directory_multi_node AGENTS

This scenario proves membership-backed conversation hydration keeps cluster
semantics in real static multi-node clusters, including requests accepted by an
ingress node that is not a replica of the UID-owned Slot.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory_multi_node -count=1 -timeout 3m -p=1
```

The bounded 25/100/200-candidate synchronization performance gate is opt-in:

```bash
WK_E2E_CONVERSATION_DIRECTORY_PERF=1 GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory_multi_node -run TestThreeNodeConversationDirectoryPerformanceAcceptance -count=1 -timeout 6m -p=1 -v
```

## Rules

- Keep assertions black-box through public channel-management,
  `/message/send`, `/message/sync`, `/channel/messagesync`,
  `/conversation/list`, `/conversation/retry`, Manager HTTP, and `/metrics`
  entrypoints.
- Enable Manager HTTP on all nodes and wait for stable actual Slot leaders
  before selecting channels.
- Keep the four-node non-replica-ingress case at three Slot replicas and one
  Channel replica. Its purpose is to isolate UID-owned directory routing from
  separate multi-replica Channel commit behavior.
- Deterministically select an ordinary source Channel whose Leader is outside
  the ingress node, then always execute ordinary pull plus CMD bind/send/sync.
- Select channels by their publicly reported Channel Leader; do not inspect
  internal stores or import `internal` packages.
- Prove batching with low-cardinality public metrics and prove partial failure
  through `unresolved` results without timing-based latency assertions.
- Keep the performance gate at the public maximum of 200 candidates per page.
  It must assert exact hydration operations, zero membership mutations, bounded
  remote calls, and zero Channel mailbox-full admissions; wall time is evidence,
  not a host-independent acceptance threshold.
