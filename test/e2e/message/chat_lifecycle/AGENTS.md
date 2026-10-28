# Chat Lifecycle E2E

This scenario proves the natural lifecycle and receive ordering of real person
and group Channel runtimes.

## Boundaries

- MUST use process-level black-box traffic and observation only.
- MUST run a three-node cluster with 12 logical Slot Raft Groups over 256
  physical hash slots and Slot/Channel replicas of 3/3.
- Hot-state probes MUST observe one loaded Leader with `HW` at least the real
  SENDACK sequence. Follower runtimes may be absent because QuorumLog replication
  does not require them; the Manager metadata must still prove three replicas
  and `min_isr=2`.
- MUST wait longer than the real five-minute idle threshold before accepting
  natural absence on all three nodes.
- MUST query both the paged Manager Channel runtime list and exact lookup from
  every ingress with `include_max_message_seq=true` before idle, after natural
  eviction, and after real SEND reheats the Channel. Lifecycle stays `active`;
  Leader runtime activity changes `active -> inactive -> active`, with the
  runtime node matching the Channel Leader and the exact committed head retained.
- MUST repeat cold Manager reads three times and use the read-only runtime probe
  to prove all replicas remain absent after each round.
- MUST NOT use a runtime eviction endpoint, mocked clock, direct database
  write, or product-internal runtime call.
- MUST use real WKProto SEND/SENDACK/RECV/RECVACK and a zero-coverage full
  `/conversation/list` page walk, including bounded unresolved retry, after
  every login.
- The fixture MUST explicitly disable Token authentication for its tokenless
  lifecycle clients. This scenario does not qualify SDK authentication.
- The cross-ingress burst case MUST assert strictly increasing recipient
  `message_seq` while multiple users send concurrently through different nodes
  for both person and group Channels. It also checks exact per-sender client
  message order and session ACK client-sequence order, emitting a bounded
  `WKRC-SESSION-ORDER` result on success. ACK ordering uses a bounded passive
  TCP frame probe; future completion goroutines are not wire-order evidence.
- Polling MUST be bounded and the package command MUST use a nine-minute
  timeout.
- Raw identities MAY appear only in transient failure diagnostics.
- Natural reheat writes a bounded JSON report on success or failure containing
  public states, sequence numbers and topology, without Channel/user identities.
  Set `WK_E2E_CHAT_LIFECYCLE_REPORT` to retain it at a chosen absolute path;
  otherwise a private temporary directory is created and its path is logged.
- Natural reheat also bridges one real Channel into `LifecycleProof.Poll` with
  an actual wall clock and unchanged public three-node probe results. The test
  delays one caller across `quietStarted + 5m`, then requires no premature
  absence or cold eligibility until a fresh request. This conservative test
  boundary follows SENDACK, RECVACK and disconnect; it is not the server's
  activity timestamp and supports no pre-boundary absence assertion.
- The bridge retains exactly three Poll records and at most 120 public
  all-node read intervals per Poll. The delayed request has a 30-second bound;
  missing evidence or exhausted bounds fail closed. The original five-minute
  natural-idle threshold, 6m15s absence limit and nine-minute package limit stay.
- Report clock cuts, public read intervals and anonymous cold-proof snapshots
  separately from the original real SEND reheat sequence. The bridge never
  approves a worker timer or completes proof reheat phases; it does not cover
  the 1,200-candidate `ProductionLifecycle.Run` protocol, formal hardware
  qualification or a cloud failure's root cause.

## Run

`WK_E2E_CHAT_LIFECYCLE_REPORT=/tmp/wk-channel-runtime-activity.json GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run TestPersonChannelNaturalReheat -count=1 -timeout=9m -p=1`

`GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run TestPersonChannelCrossIngressBurstPreservesReceiveSequence -count=1 -timeout=9m -p=1`

`GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run TestGroupChannelCrossIngressBurstPreservesReceiveSequence -count=1 -timeout=9m -p=1`
