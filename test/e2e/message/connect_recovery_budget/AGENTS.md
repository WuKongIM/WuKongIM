# CONNECT recovery budget E2E

## Scope and failure inventory (before implementation)

This policy mechanism regression uses real product processes, public Token HTTP,
WKProto and suite-owned TCP relays. A350ms hold proves a bounded transient owner
read fits the original activation/client budgets. It does not reproduce or prove
R17's scheduling/allocation cause, qualify performance, or replace10k/2k Linux
acceptance. Never change production failpoints, registration retry policy or
release gates here.

Failures to preserve:

- Authentication is bypassed, readiness is incomplete, or observed leader/hash
  Slot placement does not prove a remote authority Register.
- The delay misses the fresh UID, applies to another service/operation, repeats,
  modifies a header/body, or affects Controller/SlotRaft traffic.
- A first CONNECT receives CONNACK15 after a350ms owner-read hold even though
  activation remains3s and client operation remains5s. No CONNECT retry may hide it.
- Remote Register is replayed; partial/unknown owner state is treated as success.
- Successful CONNECT cannot exchange matching durable SENDACK/RECV in both
  directions, or identifiers, sequence, sender/recipient or payload disagree.
- Truncated/oversized framing, cancellation or shutdown leaks a buffer, copy
  worker, socket or product process, or omits the bounded final JSON evidence.

## Execution contract

Run a256HashSlot single-node cluster control and a real three-node cluster,
using12physical Slots and1/3replicas respectively, production Token auth and
unchanged protocol budgets. Require public readiness and stable actual Slot
Raft leaders. Public Manager inventory plus the public routing hash primitive
selects a fresh UID whose authority differs from ingress1 in the three-node case.
Provision an independent receiver first, then the target Token; arm the proxy
only before the target's one CONNECT. Delay only the first target SID14/op11
request350ms. SID13/op1 counts remote Register invocations. Match only a bounded
fixture-UID byte pattern; never retain or print UID/token/body/error text.

Use the optional generic upstream copy seam in suite.ClusterTCPPartition. Keep
SID/op/matcher policy here. Public pkg/transport/wire decodes the header; original
header/body bytes, request budgets, capability metadata and CancelFrames are
forwarded unchanged. Presence inspection is bounded64KiB; other bodies stream.
A cancel-aware timer and suite's128bridge bound/close/join own all work.

Wait boundedly for the delay to finish even on CONNECT failure so the old250ms
policy's real reason15 remains distinguishable from fixture/compile failures.
Successful first CONNECT must finish before3s without a retry, then complete one
verified acknowledged message in each direction. This functional test adds no
exactly-once wire-delivery guarantee. Two-thousand-per-second acceptance remains
separate.

Run after the current Go owner releases its window:

```sh
WK_E2E_CONNECT_RECOVERY_BUDGET_REPORT_DIR=/absolute/artifact-directory GOWORK=off go test -tags=e2e ./test/e2e/message/connect_recovery_budget -count=1 -timeout=3m -p=1 -v
```

WK_E2E_BINARY can select a frozen baseline/candidate product. Artifact filenames
are single_node_cluster.json and three_node_cluster.json; default directory is
/tmp/wukongim-connect-recovery-budget. Reports contain only fixed scenario
vocabulary, binary SHA, numeric topology/placement/budget/timing/counters,
recognized CONNACK reason and final client/copy/process cleanup booleans. They
are written0600 after cleanup, capped16KiB. No arbitrary error, UID, device,
Token, payload or raw frame enters them. Keep old baseline RED evidence intact.
