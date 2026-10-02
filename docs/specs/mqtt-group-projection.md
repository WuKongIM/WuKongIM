# MQTT group subscription projection

The concrete group projection composes current-owner intent validation,
GroupSources preparation, replicated shared-content readiness and SourceDrain.
Inbox admission remains a distinct required capability, not an empty success.

## Failure inventory before implementation

- Preparation is mistaken for a complete receipt despite absent/shared content.
- A quorum or one replica is mistaken for every eligible future owner, especially
  a learner with a committed anchor but missing shared content.
- Missing anchors, partial imports, scan continuations, pending retirement or
  uncertain replies turn into readiness; a changed placement combines proofs.
- An anchor with the same position but different identity passes, or a captured
  boundary beyond the source is accepted. A maintenance-only tail causes endless
  copying despite containing no publication obligation.
- Stale/forged UID, intent, membership or owner obtains a receipt; a removal needs
  receive permission, resets its source or erases outstanding exchanges.
- Unbounded copying, per-source goroutines/maps or subscriber scans enter SUBSCRIBE.

## Contract

`GroupProjection` implements the existing projection port only for group intents.
It checks the complete current request before work and before returning a receipt.
Establishment prepares one source, confirms replay coverage, then checks receive
permission again. Removal uses bounded group discovery/sealing; it does not wait
for or discard old inflight exchanges. No product listener opens in this change.

`ReplayCoordinator.Confirm` captures fresh active placement and one authoritative
plan for the exact source/start. An anchor must cover the selected start, or a
verified maintenance-only suffix may bridge it. Otherwise one bounded ordinary
coordinator turn may advance copying, but the call returns pending. For a captured
anchor, each configured replica performs at most one existing recovery step and
must report independently verified complete coverage of that exact full anchor;
imports, continuations and pending retirement are not completion. Fresh placement
must still match after all replies. Native protection retains later publications;
this proof neither grants consumer GC nor bypasses migration readiness.

All calls share bounded contexts. Repeated subscription attempts preserve the
stored start. Existing managed maintenance owns long recovery scans/fairness;
confirmation retains no per-source state and starts no retry loop. App composes
real Node/SlotMetaSource/protector ports. Product lifecycle, inbox admission,
ended-Session discovery, delivery/ACK entry and full acceptance remain required.

Tests use the already approved usecase/runtime/metadata and app/cluster seams.

## Frozen context

Source `1f147678bfee57baf59163ebda824e6a975b02b4`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `f173c59729f6d20b87b3fed5a3c6a1a692ba769c458f4750b3e643d9f3789649`
- `internal/app/FLOW.md`: `889558070e43d796e4f216b095d328d2760155b6d3233159040e80ee00c72c04`
- `pkg/channel/FLOW.md`: `84b7fa1e43bf3e829f32f0f0f9b8ac6572dc26f4ed6642233feb1929d02807d3`
