# Spec

Reviewed the seven frozen working-copy files at base/HEAD `7255102de81b45c23d52e908ccdde1b7799ee3f5`, including the untracked driver; no branch commits. The authorized aggregate-refresh contract supersedes the earlier unsupported-refresh plan.

**No remaining Spec finding. All three original P2 items are closed.**

- **Public evidence:** The requirement “每个object及观测PUT/GET、全部config before/after、URL完整集合” (`provider-m2b-plan/README.md:48`) is now satisfied by actual adapter observations. I independently matched all 306 ordered objects against the unchanged native manifest, their source/plan bindings and complete GET EOF/SHA/size/cache facts, plus 11 configuration and eight refresh operations. The 152 create-only conflicts truthfully show unconsumed PUT bodies and verified full GETs. Missing/duplicate refresh responses retain 177/179 actual rows; incomplete effects remain visible.
- **Network guard:** The requirement “socket尝试 … driver非零、always有限收据” (`m2b-native-delivery-sdk-v1/failure-matrix.md:28`) is now enforced outside exception classification. Bounded probes confirmed both swallowed and converted connect exceptions produce nonzero incomplete receipts with `unexpected_attempts=1`; a clean stage permits only TEST ONLY success.
- **Cleanup:** The same failure-matrix row requires cleanup failures to preserve finite receipts. Independent probes confirmed primary `KeyError` and cleanup `PermissionError` coexist, cleanup-only and no-op removal fail, and earlier attempted-effect counts survive.

**Non-blocking observation:** `guarded_sdk` still creates an unclosed self-check socket because its deliberate `RuntimeError` bypasses `socket.create_connection`'s `OSError` cleanup. Six bounded probes each reproduced one ResourceWarning, with no real connection. A context-managed explicit socket is a small recommended cleanup; this does not invalidate the independent attempt counter or re-open the three P2s.

No additional missing/incorrect requirement or scope creep found. All seven source hashes matched before/after. No SDK, native suite, container, cloud call or actual network connection was executed by this review. The author's first native failure remains unexplained; later successful validation and the independent bad-path verifier finding do not establish its cause.
