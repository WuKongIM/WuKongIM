## Problem and change

The preview/stable planners lacked a shared offline delivery stage that proves exact object bytes, route ordering and current activation-control fences. This adds a bounded preparation/simulation core for the original v1 preview and v2 composite plans, with create-only writes, complete readback, partial-effect receipts and unchanged original client evidence. Stable client receipts must bind the original transport input and plan digests.

The RPM A-to-B test used two DNF invocations with zero-second metadata expiry. After A metadata aged, the second invocation could refresh to B despite the case requiring A. Only that second switch phase now explicitly reuses the signed A metadata. Its request proof requires this case’s B mirrorlist before the B-prefixed exact-A payload response, with one request serial. Fresh-B and other cache cases retain their existing behavior, and the production validator is unchanged.

## Validation

- Independent Standards and Spec reviews: no remaining findings for the offline core and four-file switch repair.
- Core: 75 focused tests; full first Linux Pages run passed 554 tests and four control contracts.
- Switch repair: tests written before implementation, preserved behavioral failures, 44 related tests passed; complete local suite passed 556 of 557 tests with one existing Mac RPM-tool skip; four control contracts passed.
- Fixed AlmaLinux signed-package experiment preserved the old cross-second B selection, then proved B mirrorlist → B-prefix exact-A download with signature/hash/size checks; an independent fresh-B case still selected B.
- Original native CI failure remains recorded: 33 of 49 passed, case34 failed, 15 not reached; cleanup verified. The new head d824fe37918ee4ef22b1b1745bc02518812563e3 requires its own first Linux native result.

This prepares offline delivery only. It adds no real provider adapter, resource purchase, production key access, stable activation or public package publication.
