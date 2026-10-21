# Stable seal/transport independent review — final-freeze-v1

Result: one P2 finding, R1. No other blocking finding in the reviewed production identity, network, publication or preservation boundaries. Implementation remains uncommitted; no real seal/publication was performed by this review.

## R1: resolution digest and parsed JSON can come from different reads

`plan-object-storage-publication.py`, `_stable_snapshot`: the bounded `resolution_raw` read is SHA-256 checked against the independent descriptor, but the next `C.load_json(resolution_path, ...)` reopens the path. A deterministic real-file replacement after the first read changed only `published_at`; the second parsed value passed resolution digest/shape/identity gates and reached candidate verification. The parsed bytes had SHA-256 `56b47162e2cf70d5108327dc1edbd2c72cbecefffb586b34f721d75194702f91`, while the accepted descriptor anchor was `9c182212059ed6dd0cea7a404ca4f6119da4f6714ea7cd376425dee7d34f98d4`. This violates the stated byte anchor even though the offline plan correctly makes no GitHub authenticity claim.

The probe stopped at candidate verification; it does not claim a full valid plan, a signature bypass, or any external mutation. Fix narrowly by parsing the already bounded/hash-checked raw bytes and retaining duplicate-key and canonical encoding validation. Preserve this v1 finding and add the replacement test before changing the reader.

## Boundaries reviewed

- Stable production endpoints fixed to official API origin; TEST ONLY checks both API/download origins before requests, rejects all redirects and disables environment proxies. Upload uses exact numeric Release path/origin and has no redirect following.
- Candidate control SHA is distinct from sealing control SHA. Historical commit/tree, nontruncated exact Git blobs, Git blob SHA-1, SHA-256/size, 10 control files and 12 producer files are verified, with ancestor/current-main fences. Downloaded historical code is not executed.
- Actual source resolver downloads/verifies seven immutable source assets. Fixed reviewed attestation and native metadata helpers execute in an isolated credential/config environment; saved success JSON cannot bypass them. Source immutable identity is compared to the captured candidate and reread before publication.
- Candidate audit/crypto validator is reused; writer supplies independent plan/control/source/receipt inputs. Asset upload and final Release publication retain original two assets and null candidate IDs, no overwrite/delete, with main/tag/immutable policy checks. Binding only reserves an empty draft and explicitly reports source_verified=false.
- Composite output is plan_only, no cloud/provenance/activation authorization. Every supplied generation is validated and retained; stable signature verification is distinguished from structural preview transport fixture. Stable RPM mirrorlist is a derived no-store transport object, preserving archived signed metadata; shared preview entrypoints remain owned by preview.
- Explicit stage/mode/schema boundaries and frozen disabled channels prevent claiming a stable public release. No production workflow, role, certificate, published preview bytes or signing core changed.

All 15 frozen files and patch digest independently match manifest; 38 protected files independently match actual base Git blobs and current worktree bytes. See freeze-verification.json. Rules: governing root AGENTS.md; no package-local AGENTS/FLOW found. Relevant contract/docs and existing unchanged source/attestation/metadata implementations were read. No full suite/native/GPG/Docker/GitHub writes were executed by reviewer. Author reports 468 Python tests (one existing native-RPM conditional skip), 4 contract checks, native31+transport8; reviewer will bind final validation receipts to the amended freeze before closing R1.
