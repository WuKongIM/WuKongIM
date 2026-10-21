# Go seam contract (fixture/example, not a paid payload)

Exact fields/types are in proof-schema.json; example placeholders/zero are intentionally not valid execution inputs. JSON schema `wukongim.chat_lifecycle.released_preclock_continuation/v1`.

- CLI optional `--released-preclock-proof PATH`: strict JSON bounded at 256KiB, no duplicate/trailing/unknown fields; feed `TrustedContext.ReleasedPreclock *ReleasedPreclockContinuation` (type/name root may choose equivalently).
- Normal rehearsal/repair/formal keep existing rules when nil. Proof is accepted only for rehearsal; not direct repair or formal, and cannot coexist with formal Transition.
- Validate fixed schema/operator/CNY budgets/next_attempt2; both run_attempt1; positive IDs, equal workflow IDs; current number exactly prior+1; current run ID greater than predecessor; valid full SHAs/digest/RFC3339Nano UTC times, zero>=created; exact predecessor `<input.RequestID>-rehearsal-1`; request/source/repository/operator/normalized diagnostic public key equal respective trusted/public inputs; `0 < committed < operational_stop`; provided TrustedContext Attempt==2 and CommittedMicros equals proof committed; cost_method exact enum.
- failure_class allowed `unclassified_before_accepted_run_start` or `preflight_before_accepted_run_start`. These labels cannot assert an unknown predecessor was repaired.
- Go does not call GitHub/provider or independently assert the proof is authenticated; the sole read-only helper establishes raw API/artifact/zero/cost provenance and passes the proof only after that gate. Evidence manifest digest is syntactic/byte-binding at this seam; consumers revalidate the sidecar and raw proof.
- Stage passes `--attempt 2 --committed-micros <verified sum> --released-preclock-proof <output>/continuation/proof.json` once, never loops. Original four-input first path has no proof.
- No product runtime/template/provider changes. Root owns five Go/FLOW files. Subagent owns helper/scripts/workflows/tests/docs.
