# Source progress versus unsubscribe sealing

Source revision `c3f4ac4aa04d252cf5a8e81fb350549602d49f8d`.

## Failure inventory before isolated testing and implementation

A real progress commit between foreground SourceDrain's captured source binding
and its sealing CAS must not turn unchanged closed intent into a fatal control
conflict. Verify the entire Subscription -> GroupProjection -> SourceDrain path,
with SourceProgress and metadata storage performing the competing commit.

Cover unknown port conflict and lost applied replies; unchanged or fictitious
newer CAS rejection; replaced Owner, changed full subscription or source identity;
regressed binding/progress/parent; inconsistent cursor; unavailable fresh reads
and parent cancellation. None may gain pending classification. A qualifying
yield must propose no inline retry, fix no new end, change no quota/exchange and
release every admitted operation. The next ordinary bounded turn must preserve
the original start and exact accounted end before completing removal.

The separate process diagnostic runs retain the unchanged 256 hash Slots,
500 persistent clients and twenty initial publications. Longer churn is a
separate diagnostic attempt; the original full acceptance retains three rounds,
100,000 members and all existing deadlines/assertions.

## Confirmed boundary and repair

A diagnostic process run reaches all initial receipts, then fails its second
churn round (zero-based round 1). Fixed error-boundary records trace the one
foreground unsubscribe closure to `SourceDrain.sealBinding` CAS rejection,
through `seal`, `sealGroup`, projection and subscription completion. No Session
CAS exhaustion appears on that failure path. The competing source writer's
identity is not captured in that run; this is not proof that every earlier
intermittent closure has the same origin.

The deterministic regression fixes the competing order using real progress or
closed-drain commits and the complete subscription/group projection path. Four
valid cases fail on the baseline: progress, identical competing seal, progress
after receive revocation and progress with an unfinished exchange. Fourteen
negative cases preserve fail-closed behavior. Fresh-read errors and cancellation
are also retained instead of being masked by the original conflict.

Only a private definite source CAS rejection enters qualification. Three fresh
point reads prove one newer binding, the same complete Removing child/current
Owner and an exact cursor. Binding identity/start/protection/release stay pinned;
only strict progress or an identical Removing seal is permitted. The cursor's
accounted frontier stays fixed and its revision/completion/window cannot regress.
There is no inline write retry, quota release, completion receipt or new boundary.
The existing request's pending loop retains its original attempts/deadline.
Plain port conflicts, unknown applied replies and source release/retirement do
not enter this path. Final projection and subscription checks remain unchanged.

[Reproduction and validation artifacts](../reports/mqtt-unsubscribe-sealing/README.md)
freeze the applicable instructions from the exact source revision.
