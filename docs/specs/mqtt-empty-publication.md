# Empty MQTT publication bodies

[MQTT 5.0 section 3.3.3](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html)
allows a zero-length PUBLISH payload. The shared append runtime and before-send
webhook still reject that body even though MQTT decoding, Will preparation and
storage accept it. This prevents valid publications and frozen empty Will output
from reaching the existing durable message path.

## Failure inventory before implementation

1. Router or local preparation rejects valid empty MQTT or server-keyed Will
   content; alternate scoped/transient paths disagree about admission.
2. Merely nonempty, corrupt metadata or an unkeyed Will template bypasses the
   native nonempty-body rule. Rejection must precede routing, allocation and hooks.
3. Before-send rejects empty input or an explicit empty callback replacement;
   nil replacement incorrectly erases the original body. Invalid/oversized
   replacements or business denials lose their existing failure policy.
4. QoS 1 acknowledges without a commit, cross-PID retries append again, or QoS 0
   fails to persist. Ordinary membership revocation must continue denying sends.
5. Will treats an empty frozen body as unprepared, repeats transformations after
   lost replies, or cannot recover its retained receipt after permission changes.
6. Native empty sends become accepted or a new data/replication format is needed.

## Contract

Publication metadata supplies a shared validation function for sendable metadata:
absent native metadata is valid, MQTT metadata must be bounded/canonical, and Will
templates require their durable server identity before sending. Expiry arithmetic
remains validated. This grants no authorization or Session authority.

The append runtime permits empty bodies only with such nonempty validated
metadata. The webhook applies the same provenance rule to input and explicit
replacement; nil replacement continues to preserve the input. Native validation,
payload size limits, deny/failure policies and immutable metadata remain intact.
No table, column, wire codec or storage format is added. Full product MQTT
composition and process E2E remain separate work.

## Repeatable validation

```sh
GOWORK=off go test -race -p 2 ./pkg/protocol/publication ./internal/runtime/channelappend ./internal/usecase/message -count=1
GOWORK=off go test -race -p 2 -tags=integration ./internal/app -run '^TestMQTTPublish(SingleNodeCluster|Empty)|^TestMQTTWillExecution(SingleNodeCluster|Empty)' -count=1 -v
GOWORK=off go run ./scripts/flowcheck --mode check
```

Failure inventory and regression tests preceded implementation. The focused
RED run rejected valid MQTT/Will empty bodies and callback replacements; all five
empty-body cluster scenarios also failed before the fix. The explicit nonempty
Will-to-empty replacement regression failed before the fix as well.

Full related race suites passed: publication 1.276s, append runtime 2.312s and
message usecase 1.531s. Eight real single-node cluster integrations with 256 hash
Slots passed together under race in 27.880s, including the three existing nonempty
scenarios. PUBLISH uses the real authenticated adapter with a controlled transport
writer; Will uses real Slot claims, hooks, append and retained-receipt reads. The
HTTP Webhooks are real. This does not constitute product-listener or process E2E
acceptance, cross-node failover or large-scale performance evidence.
FLOW validation passed with 86 compliant files, no invalid files and nine existing
file-size warnings.

Representative repeatable integration output:

```text
mqtt_publish_evidence: hash_slots=256 authenticated=true commit_before_ack=true qos0_persisted=true cross_pid_retry_deduplicated=true reused_pid_new_message=true membership_revocation_denied=true empty_payload=true real_webhook=true
mqtt_will_preparation_evidence: nodes=1 hash_slots=256 real_webhook=true frozen_payload=true lost_reply=true revoked_before_recovery=true hooks=2 committed_messages=2 product_listener=false empty_payload=true
```

## Frozen repository context

Source: `a7dd5fb3dcbe98c965b2d6b3dafd1ef8a1cc0528`. Applicable rules/navigation were read before package work.
SHA-256 values refer to that exact source revision.

| File | SHA-256 |
| --- | --- |
| `AGENTS.md` | `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade` |
| `internal/runtime/channelappend/FLOW.md` | `0eed1460db51ec882d463c7b0ab8d4e38ad46ca01f33a52274ef8d7d554cf404` |
| `internal/usecase/message/FLOW.md` | `23e99d9771665a40eb023f15a60c9350721a26cdefaa3fbb40f41f28f1d9bcd6` |
| `pkg/protocol/publication/FLOW.md` | `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655` |
| `internal/app/FLOW.md` | `6d5cbc6153b4b6b18d947f27e43a25e01b7ad2673bc253be790abf8bb37fe9c5` |
