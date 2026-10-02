# Started Will recovery acceptance

Source: `14386ca24a4eb00e7a4d4ebf5899023736e2c70a` plus the exact product hashes
in [final provenance](final-provenance.json). The local task branch is unmerged.
The approved [recovery contract](../../specs/mqtt-will-started-recovery.md)
narrows recovery to exact, durably sealed non-dispatch attempts.

A body-free node journal reserves the attempt before Started, then arbitrates
Reserved -> Admitted versus Reserved -> Sealed. An owning-node lock/retired-boot
fact joins older-boot proof. Current permission, frozen content and a definite
successor Slot CAS precede dispatch; positive receipt recovery takes precedence.
Fresh target absence, expired grants and missing/corrupt records grant no retry.
Failed successor claims keep their reservation even on definite conflict, since
an earlier unknown equal-tuple proposal may apply late. Known successor/terminal
cleanup removes only captured tuples. RPC 105 requires matched product binaries.

## Process evidence

The initial Started-before-SEND baseline failed at original Will receipt after
successful exact restart and persistent reconnect: 51.483 seconds. The first
repair passed in 43.984 seconds. The pinned-worker fixture repeats the original
baseline failure in 54.262 seconds; its [bounded RED artifact](red-undispatched-pinned-cut.json)
ends at `original-will-delivery` with no resubscription or ambiguous CONNECT retry.
The necessary 35-second receipt bound covers the original ten-second grant,
background discovery, native recovery and delivery; it is unchanged after repair.

The six cases use independent Paho MQTT 5 and public HTTP against real owned
processes, 256 hash Slots, one- and three-node clusters:

| Cut | Required result |
| --- | --- |
| Started before dispatch, SIGKILL/restart exact executor | Original Will once after persistent reconnect, no SUBSCRIBE. |
| Published before observation, SIGKILL/restart exact executor | Original unfinished QoS 1 PacketID, DUP and message identity; no second business publication. |
| Accepted append delayed 30 seconds | Independent CONNECT works; no delivery or second publication attempt in a 21-second unknown window spanning two ten-second grants; original publication eventually completes. |

Started/published cuts hold the exact worker in a 60-second temporary gofail
sleep until SIGKILL. Disabling the term does not release its in-flight sleep.
The accepted append delay runs after native item admission and before its own
append budget begins, so caller cancellation does not manufacture nonpublication.
Reports run after client/process cleanup and exclude keys, credentials, payloads
and raw logs. A quiet observation must expire by deadline, never closed transport.

The final six-case matrix passed in 317.404 seconds against the exact delivered
source and instrumented binary hashes. All six [case artifacts](cases/) record
one business publication, zero unexpected delivery, no CONNECT retry and no
resubscription. Both published cases preserve the original unacknowledged
exchange; both admitted cases record one attempt throughout the 21-second
unknown-effect window. [Validation receipts](validation.json) bind these artifacts
and focused race, ordinary Will, integration, Session and FLOW checks to log hashes.
The final ordinary Will regression also passed in 51.446 seconds.
Intermediate complete matrices passed in 314.284 and 307.066 seconds; the latter
uses the pinned cut. They predate the final equal-tuple retention hardening.

## Repeat

Use Go 1.25.11 and the ordinary repository module graph. Enable gofail only in
the script's temporary source copy; `go.mod` and `go.sum` stay unchanged.

```sh
scripts/build-gofail-binary.sh --out /tmp/wukongim-will-started-verified-gofail \
  --package internal/usecase/mqttsession --package internal/app \
  --package internal/runtime/channelappend
WK_E2E_GOFAIL_MQTT=1 \
WK_E2E_BINARY=/tmp/wukongim-will-started-verified-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-started-verified-matrix \
GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_recovery \
  -count=1 -timeout=12m -v
```

[Frozen context](frozen-context.json) records pre-implementation failure inventory,
original test hashes and applicable exact-source AGENTS/FLOW digests. The final
provenance records the stronger fixture, source hashes and both binary hashes.
[Two-axis review](reviews.md) records scope, initial findings and resolution.

## Limits

This qualifies only the six controlled windows and finite quiet observations.
Admitted-before-SEND crashes, legacy/lost journals, already-admitted unknown-effect
terminal recovery, later denial of sealed Started work, orphan journal reclamation,
delayed unknown-claim race qualification, arbitrary restore/delete and
partition/asymmetric links remain unqualified. The journal retains uncertain or
unused reservations conservatively; its fixed 1,024-record node cap can exhaust
and fail closed. Whole-Channel deletion/restore and aggregate node/cluster
shared-storage admission remain separate target-contract gaps. The broader
MQTT implementation and release acceptance remain incomplete.
