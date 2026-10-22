# Object publication: frozen failure inventory, phase 1

Base 7255102de81b45c23d52e908ccdde1b7799ee3f5. Reviewed design manifest acc0877bd3c3bd2f17ad121fce7dce2ea2813c569dc347dcdb6628299e894bd5.
All phase-1 tests are written before implementation; later SDK orchestration tests must precede that implementation after M2b acceptance. No production success is in this slice.

## Phase 1: immediate tests

| ID | Failure / boundary | Observable outcome |
|---|---|---|
| C1 | Default control absent, enabled, extra authority fields, unknown schema/backend, non-bool enabled | No publication; fixed CLI refusal, no credentials/SDK/output materialization |
| C2 | Any disabled control binding non-null; duplicate JSON keys, symlink, oversized JSON | Reject without output files or remote operations |
| C3 | Runtime attempts alternate control root, callback, TEST ONLY, insecure/CA/connect target override | Fixed CLI parser rejects; no bypass interface |
| C4 | Explicit upload or activate with disabled current control and missing inputs | Same disabled refusal before opening transport input; no saved receipt used |
| G1 | Actual temporary Git clean checkout plus current-main mismatch at HTTP transport | Original fresh helper reaches canonical GitHub read then refuses; never report authenticated |
| G2 | Actual Git HEAD differs, tracked helper edited, staged edit, untracked file | Original fresh helper refuses before first HTTP request; do not mock Git/subprocess |
| G3 | Committed helper byte change but old expected executor SHA | Refuses before HTTP; real Git identity rather than mocked clean status |
| G4 | Invalid original plan digest or TEST ONLY input | Original planner/fresh helper refuses before HTTP/native/SDK |
| T1 | Exact canonical URI with loopback connection override, correct CA/SAN | Actual server sees canonical Host and SNI and byte-exact URI/query; exact full body/hash/cache verified, fixture receipt stays non-production |
| T2 | Wrong SAN or untrusted CA | Real TLS handshake refuses before HTTP request; no insecure fallback |
| T3 | Redirect or HTTP error | Reject; do not follow redirect or accept alternate target |
| T4 | Body differs, short/oversized body, wrong cache or encoded representation | Reject full-response contract, never HEAD/ETag acceptance |
| T5 | Header limit, timeout, slow streaming deadline | Bounded rejection; subprocess killed/reaped, socket/server/thread scratch cleaned |
| T6 | Bad URI/cname, credential-bearing URL, newline/backslash/fragment, non-loopback test target, invalid size/time/digest | Reject before network; fixed production limits cannot be CLI overridden |
| T7 | Proxy/curl config/CA environment attempts | Probe uses explicit isolated transport settings, keeping canonical TLS target; fixture-only CA remains explicit |
| W1 | Integration suite omitted or silently skipped/zero cases | Existing read-only workflow runs explicit suite and retains finite receipt; runner fails on skip/zero |

## Frozen later upload/activate inventory (not phase-1 implemented/tested)

| ID | Failure / semantics | Required future result |
|---|---|---|
| O1 | Each operation starts after current-control operation/commit changed | Independent fresh→prepare from clean current main; historical upload receipt is evidence only |
| O2 | Activate finds any object absent, different bytes/cache, or incomplete GET | Full GET every planned object; no PUT, no config/refresh on mismatch |
| O3 | Same-activation fresh(previous=first) drift before config or after clients | Stop next phase, preserve already observed effects; never compare with previous upload invocation |
| O4 | Source/audit/tag/numeric asset identity or real attestation/native command fails | Original verifier refuses; saved verified JSON cannot replace it |
| O5 | Mixed principal/session/account/bucket/domain/CNAME/resource digest | Exact independently reviewed resource/session binding; no observed-identity backfill or automatic AssumeRole |
| O6 | Create-only conflict/unknown PUT / partial config / uncertain refresh | Original provider rules, no blind retry/delete/rollback claim; bounded explicit effects |
| O7 | Config digest/managed IDs drift, unmanaged rewrite/delete or compiler limit | Stop without broader allowlists, permissions or bounds |
| O8 | Refresh aggregate task ID/URL closure, missing root, alias/snapshot precedence or live CDN/cache error | Incomplete; actual live request table before client completion |
| O9 | TEST ONLY callbacks/native fixtures routed into production | CLI hardwires original fresh/native/clients; fixture success never production authentication |
| O10 | Missing distro/installed CLI/version/source or same stable target with other composite digest | Original client receipts unchanged; reuse original binding check; reject outer completion |
| O11 | Preview /repo replaced or public stable rolled back to preview-only | Reject target; previous complete composite remains recovery option |
| O12 | Credential/raw SDK exception leaks into receipt or incomplete cleanup | Bounded redacted effects; no secret/debug/full workspace archive |

## Integration execution and archive boundary

Real Git and TLS are explicit tests/provider_sdk/test_object_storage_publication.py integration cases, never default discovery. They use only owned temporary files, fixture TLS private keys and loopback services. No cloud, real source attestation, production signing, or public CDN proof is claimed. Default tests retain parsing/static/no-background refusal checks only. The 8-file scope is unchanged. Original planner/producer/Provider/fresh equality/trust/Pages are read-only.
