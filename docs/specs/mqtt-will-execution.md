# MQTT Will execution turns

This implements first dispatch and positive-receipt recovery for a detached Will.
It does not enable the product listener. The existing Will row and command are
reused; no table or encoding is added.

## Failure inventory before implementation

1. Session replacement hides an older detached Ready Will, or a scan candidate
   supplies execution authority without an exact foreground reread.
2. Two callers both dispatch after the same claim: only a definite Applied
   Ready-to-Executing CAS grants first dispatch. Unchanged, conflict, malformed
   replies and unknown outcomes cannot grant it.
3. Setup authorization is reused at execution; a fresh explicit denial must
   finish Rejected without invoking SEND. Authority failures retain the task.
4. A committed append reply is lost. Recovery must use the immutable server key,
   exact body/properties and original message ID, sequence and timestamp.
   Receipt recovery precedes any new authorization decision.
5. A lease expires during an unknown append. A missing receipt, missing runtime,
   permission denial or foreign boot must not be treated as nonpublication.
   An existing Executing task may recover a positive receipt, but this increment
   does not redispatch it or reject it based on present permission.
6. The final CAS reply is lost, conflicts or reports the wrong revision. A later
   exact read may recover Published; no invented timestamp or second SEND is used.
7. A wall-only clock, regression, overflow, cancellation or elapsed local lease
   permits an effect. The monotonic deadline starts before the claim proposal;
   every subsequent effect checks it. Four admitted turns and a five-second
   maximum context bound work without a waiting queue.
8. The receipt adapter uses cached placement, falls back to a native client-number
   lookup, converts unavailable authority into absence, accepts an invalid HW or
   content hash, or normalizes a person target differently from SEND.
9. Mapping changes Will content, drops properties, runs a template as ordinary
   MQTT, or invents a device/session identity to obtain privileged send policy.
   SEND must reuse the message usecase, with the server key in metadata v2.
10. A configured lease shorter than the turn timeout leaves dependencies with a
    later deadline. The claim and SEND context must be capped at the original
    monotonic lease deadline, including time already spent proposing the claim.

## Remaining product gates

An expired execution is not proof that its prior append stopped. Receipt absence
therefore retains a pending obligation. Redispatch after uncertain or never-started
execution still needs a fenced admission/recovery decision, receipt transfer and
whole-Channel delete/restore isolation. Automatic scheduling, receipt quotas and
retirement, product lifecycle and process-level acceptance remain required.
SEND retains its existing hooks. A hook-transformed body cannot match the
immutable template receipt and therefore remains pending; product admission must
define and freeze such transformations before enabling this execution path.

## Validation

Failure-first tests cover real metadata lifecycle/CAS, lost claim/final replies,
detached generations, current denial versus unavailable authority, absent or
invalid receipts, local clock/lease/cancellation and bounded concurrency. Adapter
tests cover person normalization, exact server identity, content SHA-256 and
unavailable fresh authority.

The real app test uses a 256-hash-slot single-node cluster, current device-token
authentication and Session disconnect. It verifies normal publication, then
injects lost observation after an actual SEND commit, removes group membership
and recovers the original ID/sequence/time from a successor executor. A separate
Ready task is rejected under the same revoked permission; history is unchanged.
This is integration coverage, not process-level MQTT acceptance.

Validation passed on 2026-09-27: all three affected packages under the race
detector; after the shorter-lease deadline regression was fixed, the focused
execution race suite and real app integration passed again. FLOW validation
reported 86 compliant files, no invalid files and the nine existing warnings.

Repeat with:

```sh
GOWORK=off go test -race -p 2 ./internal/usecase/mqttsession ./internal/infra/cluster ./internal/app -count=1
GOWORK=off go test -race -p 2 -tags=integration ./internal/app -run '^TestMQTTWillExecutionSingleNodeCluster' -count=1 -v
```

## Frozen implementation context

Source `2e2bd625e818ee65e73643699dd50b86556e8ba5`; SHA-256 digests:

| File | SHA-256 |
| --- | --- |
| AGENTS.md | d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade |
| internal/usecase/mqttsession/FLOW.md | 52beea743cd24c1f22a5668a52dd7d4be48d0a5db7f68d6562720637e365bcda |
| internal/app/FLOW.md | 21841d2da3b34bcf9ec15b73803d29b3dc3f9a24590c9d12fb88e7b1807d9f3b |
| internal/infra/cluster/FLOW.md | d1bd525955cbf74d52c4a0f4fd2702e3705b976075fba0e9f94cd3d41b0e7b47 |
| internal/usecase/message/FLOW.md | 2807932a88c025051791cbd1daf3cc0392fc949fb139081630e45fa938553042 |
| internal/runtime/channelappend/FLOW.md | 0eed1460db51ec882d463c7b0ab8d4e38ad46ca01f33a52274ef8d7d554cf404 |
| internal/contracts/channelappend/FLOW.md | a78422c8230bb50e54b47b94c9543e53fe5ed38d8e55abbaa6726f865306912e |
| pkg/db/FLOW.md | 33c7ba81c18ed8cb377e9bef541526028ef31e64dd03becc7ac1ea6cc14fb10a |
| pkg/db/meta/FLOW.md | 9ae26c99184159c100544920f08a8023f7098e6a1ff6b50ba6e1e067618fa8ee |
| pkg/db/message/FLOW.md | bebe270dbea585057f562be6b0fd549bba59c6038388c313899ec0b06c9d133b |
| pkg/protocol/publication/FLOW.md | c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655 |
| pkg/channel/FLOW.md | 89e78216341b55d381f48ae59c05aaa9266c8894b4cb0052de1b7948161aef95 |
| pkg/cluster/FLOW.md | 621efd9665273bf7a30cd773ef0b35774b4a62e680dc5e5966cf8e03b986bbb3 |
