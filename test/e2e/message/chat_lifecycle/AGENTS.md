# Chat Lifecycle E2E

This scenario proves the natural lifecycle of one real person Channel runtime.

## Boundaries

- MUST use process-level black-box traffic and observation only.
- MUST run a three-node cluster with 12 logical Slot Raft Groups over 256
  physical hash slots and Slot/Channel replicas of 3/3.
- MUST wait longer than the real five-minute idle threshold before accepting
  natural absence on all three nodes.
- MUST NOT use a runtime eviction endpoint, mocked clock, direct database
  write, or product-internal runtime call.
- MUST use real WKProto SEND/SENDACK/RECV/RECVACK and a version-zero full
  `/conversation/sync` after every login.
- Polling MUST be bounded and the package command MUST use a nine-minute
  timeout.
- Raw identities MAY appear only in transient failure diagnostics.

## Run

`GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run TestPersonChannelNaturalReheat -count=1 -timeout=9m -p=1`
