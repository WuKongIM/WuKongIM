# Frozen Will publication preparation

Will hooks run before append. Their transformed payload must be durably frozen
before dispatch so a lost SEND reply can be resolved against the actual content.
The original Will configuration remains immutable and available for audit.

## Failure inventory before implementation

1. Recovery compares the configured body with a hook-transformed publication and
   cannot find its positive receipt; repeating hooks changes content again.
2. A crash before publication looks identical to an uncertain accepted append.
   Explicit Preparing, Prepared and Started phases must distinguish these cases.
3. Claim loss, preparation-CAS loss or takeover permits a worker to publish before
   its exact Started transition is definitely committed. Unknown replies never
   grant publication; unfinished preparation may resume after lease takeover.
4. A stale executor changes frozen content, skips a phase, clears the marker or
   replaces the original Will. Same-executor live-lease CAS must enforce each
   phase and retain frozen content across takeover and terminal receipt commits.
5. Snapshot, command round trips, same-batch overlays, byte bounds, aliasing or
   diagnostic output lose/leak the extra payload. Empty prepared bodies need an
   explicit phase rather than nil/non-nil inference.
6. SEND hook preparation changes publisher, target, metadata or privileged flags;
   only payload replacement is supported. Prepared publication still checks fresh
   ordinary permission, establishes person directories and uses the shared append
   submitter without rerunning transformations or inventing a device owner.
7. Permission denial before Started can reject safely; denial after Started,
   absent receipt, lease expiry and foreign boot cannot prove nonpublication.
8. Legacy Executing rows lack phase evidence: they remain positive-recovery-only,
   never silently acquire a new Preparing phase and risk a duplicate send.
9. Cancellation, clock regression or lease expiry during either durable phase
   prevents later effects. Admitted turns and all contexts retain existing bounds.
10. A restored terminal row pairs Published with unfinished preparation, or
    Rejected with an uncertain Started phase. Shape validation must reject these
    combinations even when no CAS transition is replayed.
11. Two maximum payloads plus metadata and JSON-escaped bounded identities exceed
    the old 256 KiB command budget. Valid prepared rows must fit a fixed, tested
    wire budget without truncating either body or weakening identity bounds.

## Durable shape

Table 27 adds optional columns 35 `dispatch_stage` and 36 `dispatch_payload`.
Zero stage preserves old encoding/JSON. Stage 1 is Preparing, 2 Prepared, 3 Started.
Only Executing and terminal execution rows may carry them. Ready claims may enter
Preparing; a live exact executor freezes a bounded payload in Prepared, then
commits Started without changing it. Takeover preserves both values. Published
requires Started; Rejected is permitted only before Started for marked records.
The original columns 15/16 remain immutable. Publication metadata v2 is derived
from the original template and server key; hooks may replace only the payload.
New rows are bounded at 192 KiB; each payload remains at most 65,535 bytes.
Command 72 has a 320 KiB bound, including worst-case escaped identities. Old strict command readers
reject new JSON; matched writers/tools and pre-feature rollback remain required.

## Remaining boundaries

Started with no positive receipt still needs safe retry admission, retained
receipt transfer, runtime-incarnation binding and distributed restore isolation.
Preparation evidence alone does not authorize redispatch. Automatic scheduling,
product lifecycle and process E2E acceptance remain separate work. Storage preserves
empty prepared payloads distinctly; [empty publication admission](mqtt-empty-publication.md)
now permits them through the shared router/preparer and before-send Webhook while
retaining native nonempty-body validation.

Preparation adds two bounded Slot CAS writes before the first append. Four admitted
turns retain the existing five-second maximum context and monotonic lease checks;
there is no new waiting queue or background body cache. Hooks may repeat while
preparation has not committed. After Prepared, takeover reuses the frozen body;
only a definite Started CAS in that turn can authorize its first publication.

## Repeatable validation

```sh
GOWORK=off go test -race -p 2 ./pkg/db/meta ./pkg/slot/fsm ./internal/usecase/message ./internal/usecase/mqttsession ./internal/infra/cluster ./internal/app -count=1
GOWORK=off go test -race -tags=integration -p 2 ./internal/app -run '^TestMQTTWillExecutionSingleNodeCluster' -count=1 -v
GOWORK=off go run ./scripts/flowcheck --mode check
```

Failure-first tests reproduced original-body recovery, phase-observation loss,
impossible restored terminal phases, and command overflow for maximum escaped
identities. Related full race suites passed: metadata, Slot FSM, message policy,
Session orchestration, cluster adapters and app wiring. After tightening terminal
shape and command bounds, metadata and Slot FSM full race suites passed again.

A real 256-hash-slot single-node cluster uses its HTTP Webhook, device-token
Session lifecycle, foreground Slot commands and Channel storage. It proves both
normal publication and recovery after a lost observation and permission revocation,
with the actual transformed payload, original identity/time and two hook calls for
two business messages. The integration emits `mqtt_will_preparation_evidence`.
These are package integrations, not process E2E, load acceptance or product startup.

## Frozen repository context

Source: `700ed65a382c150f02002e018ed3cfe7548f716e`. SHA-256 values refer to that exact revision.

| File | SHA-256 |
| --- | --- |
| `AGENTS.md` | `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade` |
| `pkg/db/FLOW.md` | `33c7ba81c18ed8cb377e9bef541526028ef31e64dd03becc7ac1ea6cc14fb10a` |
| `pkg/db/meta/FLOW.md` | `288ada27ab0fe12b47286ba441df4859ed13ec4244a690ac90e5f78e86647d0c` |
| `pkg/slot/FLOW.md` | `4c55608f2c333df17019136b67f527f358d267c2d5fce215d46d065bb27d5d45` |
| `internal/usecase/message/FLOW.md` | `2807932a88c025051791cbd1daf3cc0392fc949fb139081630e45fa938553042` |
| `internal/usecase/mqttsession/FLOW.md` | `e78df46dc670067198aba0974b7bd539cf77f7f3e579af65660d5f8f9959fe6b` |
| `internal/app/FLOW.md` | `da57910598d78185403fe83780054adc0d295e8f9cca203d1264370da97e26fb` |
| `internal/infra/cluster/FLOW.md` | `b96d4741b47a3b59a094eb83de316b2080004f4c2cc06f579060d6451c052c30` |
