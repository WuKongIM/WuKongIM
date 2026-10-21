## Problem and change

The preview/stable planners lacked a shared offline delivery stage that proves exact object bytes, route ordering and current activation-control fences. This adds a bounded preparation/simulation core for the original v1 preview and v2 composite plans, with create-only writes, complete readback, partial-effect receipts and unchanged original client evidence. Stable client receipts bind the original transport input and plan digests.

The RPM A-to-B test uses two DNF invocations. The original zero-second policy can persist explicit expiry when makecache exits, so changing only the second invocation still permits B metadata to replace A. The TEST ONLY switch now keeps the repository expiry policy fixed across both invocations, while the first still performs signed makecache --refresh. Its request proof requires this case's B mirrorlist before the B-prefixed exact-A payload response, with one request serial. Production client policy, fresh-B and persistent-cache cases are unchanged.

## Validation

- Independent Standards and Spec reviews: no remaining findings for the offline core and both narrow switch repairs.
- Core: 75 focused tests; original Linux Pages passed 554 tests and four control contracts.
- Final repair: modified regression first failed against the previous driver, then all 44 related tests passed; complete local suite ran 557 tests with one existing Mac RPM-tool skip; four control contracts passed.
- Fixed Rocky and Alma signed-package probes reproduce native persisted expiry using the exact previous shell command, then verify the final command retains A through B mirrorlist and B-prefix signed payload delivery. Independent fresh-B still selects B. These controlled probes do not uniquely explain historical CI timing.
- Both earlier CI failures remain recorded: eac9d252 passed 33/49 before Alma case34 failed; d824fe37 passed 22/49 before Rocky case23 failed despite the second-stage option. Cleanup passed. The new head 2368c8745853d7d8ad82e706a6e7980842526241 requires its own first Linux native result.

This prepares offline delivery only. It adds no real provider adapter, resource purchase, production key access, stable activation or public package publication.
