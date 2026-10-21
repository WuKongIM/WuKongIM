# Tests-first failure inventory

All cases below must be authored before repair implementation. Existing coverage may be reused where it exercises the real boundary; no after-the-fact guard-only substitutes for request/plan behavior.

| Boundary | Required positive / failure evidence |
|---|---|
| Two distinct candidates | Current B + retained A generates B-prefix old product and old bootstrap aliases with original A member/size/SHA. Reverse A-prefix B routes also exist for supplied rollback/current permutations. No candidate/archive mutation. |
| Native original members | Existing A/A and B/B package URLs remain original snapshot_objects, without duplicate overlay aliases. All repodata, repomd/.asc and metadata hash facts remain byte-identical. |
| Explicit source binding | Alias source audit ID/channel/member resolves to the verified retained archive's actual Packages member. Missing/unlisted source, invented B member, wrong member or mismatched size/hash rejects. Shared identical payloads can deduplicate only with an explicit valid original source and exact expected bytes. |
| Collision across namespaces | Same public URI with different SHA, size or cache classification rejects whether found in snapshot_objects or immutable_aliases. No “last mapping wins”. Same-byte collisions are deterministic and keep one binding. |
| Candidate shape and bounds | Product+bootstrap only; extra/missing RPM payload or unsupported path is rejected. At most seven stable snapshots from existing max-eight total; max84 missing aliases follows from the shape. No preview cross-product. |
| Channel/family/architecture | Do not create aliases for preview, APT, keys, metadata, wrong architecture, unknown RPM names, or unsupplied generations. Wrong path in a purported alias or request is rejected. |
| Verification and stability | Failed native verification, changed archive/anchors or changed source bytes cannot produce aliases. Existing v1 preview plan/default/receipt remain unchanged. Repeated same input produces identical canonical plan. |
| Real request sequence | A mirrorlist→A signed metadata→B mirrorlist→B-prefix A payload: fixture serves exact A bytes only via an emitted alias; corresponding native proof accepts it. No makecache/barrier/client-parameter changes. |
| Conditional metadata | A 304 requires the exact selected immutable metadata URL, expected-object ETag and matching request If-None-Match, or (only when If-None-Match is absent) the object-specific Last-Modified validator. Missing/wrong validators, different URLs and conflicting validator precedence reject. Normal cache reuse/native signature checks remain required. |
| Strong metadata proof | B-prefix metadata is rejected even if its bytes happen to match. A-prefix metadata with wrong SHA/size, incomplete write or unknown metadata path rejects. A repomd and signature reads remain required. |
| Strong payload proof | Arbitrary B-prefix URL lacking a plan alias, wrong package bytes/version, absent completion flag, failed HTTP status, unknown source, cross-family/arch path rejects; exact A downloaded package hash and native signature checks still apply. |
| Persisted clients and target | Cached A/current B compatibility covers only supplied aliases. The cache-B-target case must still receive exact B; an authenticated A never passes a B target. Existing wrong version/source/missing CLI cases remain mandatory. |
| Original negatives/cleanup | Metadata missing/wrong signature/mirrorlist/TLS/disconnect failures cannot be turned into a positive or accepted at a harness error. Preserve 49 cases, owned cleanup, bounded logs/timeouts and first failure. |

Structural fixture tests may isolate native cryptographic verification, but must say so; the real CI fixture supplies the native signature evidence. Do not report planner/route tests as Linux DNF recovery.
