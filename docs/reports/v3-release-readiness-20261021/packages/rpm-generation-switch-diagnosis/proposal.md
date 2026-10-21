# RPM generation switch: evidence and narrow repair proposal

Status: diagnosis/proposal only at f2c37e92b09bcd11599d31449bbca5de0ac52c25, tree 11ead51354aecc30087202959eeb09028617387f. The CI synthetic checkout 3ca7c376fc9cef29f0530886c893dd82a7609bf4 has that exact tree. No implementation/test-source changes, Docker, native package build, commit, push or CI dispatch in this diagnosis.

## Established evidence

Run 39602187505 attempt 1, stable job 119229193899 executed 23/49 cases; 22 passed. Both A=3.0.0 and B=3.0.1 clean baselines passed authenticated downloads plus complete installed CLI for all four distributions. Ubuntu/Debian A/B, caches and negatives also passed. First failure: Rocky A-to-B request barrier. Command 161 (zero-based receipt array index) failed exit 1 after 1.161s, not timeout. Cleanup verified zero containers/networks.

The source script invokes two distinct DNF processes: makecache --refresh, then install --downloadonly. They share cache and repository configuration; they are not one DNF transaction. Serial 24 records:
1. First floating mirrorlist response identifies A. The server then switches current generation to B.
2. A-prefix repomd.xml, .asc, primary and filelists are delivered with 200; all have response_write_completed=true.
3. The second DNF process reads the floating mirrorlist again and gets B.
4. It still requests version 3.0.0 selected from A metadata, now relative to B's base URL. B-prefix Packages/wukongim_3.0.0_linux_amd64.rpm returns four complete 404 responses.

The retained A archive and canonical public A payload exist; that does not create a B-prefix route to the A payload. _stable_snapshot retains original members only below their own snapshot prefix, and the existing canonical aliases do not match the requested B-prefix URI. The native signature/target checks did not report rejection; the failure is transport addressability of the old authenticated payload. The logs establish this observed behavior, not a claim about every DNF version or its undocumented cache internals.

## Three ranked explanations and discriminating evidence

1. Retention does not close the actual URL set when a second native invocation refreshes mirrorlist but reuses metadata. Supported by both mirror responses, A metadata, exact B/old-package 404 and planner source.
2. Signed A metadata or RPM bytes are invalid. Contradicted for these exact fixture bytes by four-distribution A baseline and full installed CLI; current failure is HTTP404 before payload verification, not a cryptographic error.
3. TLS truncation, timeout or unbounded cleanup caused this failure. Contradicted here by completed-write200 metadata/mirrorlist, completed-write404 payloads, exit1 in1.161s and verified cleanup. No claim that those unrelated fault classes can never recur.

## Approved-contract mismatch requiring explicit amendment

The original generation-2 scope-proposal.md section “确定性 A/B 与版本判定” explicitly required Rocky/Alma repomd/.asc/repodata/RPM all under A snapshot after A mirrorlist. Its failure-matrix.md Rocky/Alma row repeats “全immutableA prefix”. Current docs/stable-audit-transport.md lines 129–132 states retained immutable RPM objects remain A. Current native driver combines repodata and Packages paths and requires every path starts with A's prefix.

Two real native invocations invalidate the URL-prefix premise. Changing transport alone would still fail that driver assertion. Amend the guarantee explicitly: A metadata must remain under exact A snapshot with A byte identity; the package may be requested under any supplied stable snapshot prefix only when the planner explicitly maps that URI to an authenticated retained archive member whose bytes match A's expected package. Native signature checking, exact package version/hash and expected product commit remain mandatory. This is delivery of A bytes via an enumerated transport alias, not acceptance of B bytes or a floating metadata mix.

## Preferred transport change

Change only the existing composite planner, its tests, native driver/tests and relevant docs/CHANGELOG. Legacy preview planning/defaults, signed archives, producer scripts, channels/keys, release flags and activation behavior stay unchanged.

After each supplied stable snapshot has completed P.verify_candidate and bounded archive verification, form a catalog of its two verified RPM payloads: the exact product and stable bootstrap RPM at rpm/stable/el/9/x86_64/Packages/*.rpm. Composer build_candidate_plan fixes active_versions=[version] and retained_versions=[]; validate_rpm_tree closes active/all payloads over product+bootstrap. Assert that existing narrow contract when extracting the catalog; do not iterate arbitrary archive members or silently broaden to other channels/architectures/families.

For every supplied current/retained stable snapshot prefix, expose every catalog payload's relative path. Existing original snapshot members remain solely in snapshot_objects. If the exact URI already exists there, require identical SHA256, size and cache classification and omit a duplicate alias; conflicting bytes fail. Only absent paths become immutable_aliases, following the current object_key='public/'+public_uri[1:] convention and retaining source.channel/audit_release_id/member of the original authenticated payload. Check collisions against both existing snapshot_objects and aliases, not just within alias groups; the materializer currently copies snapshots before aliases and must never hide a differing original object. Same-path/same-byte duplicates may deduplicate deterministically; source binding must remain explicit and traceable to the expected A package. Never invent a member under B's archive for an A package.

The current composite cap is eight total snapshots including mandatory preview, hence at most seven stable snapshots. With exactly two RPM payloads per candidate, at most 2*S*(S-1)=84 missing aliases are needed (at most98 target slots including native members). Reuse these existing contracts and reject cardinality/path deviations; do not introduce an unbounded archive cross product or a new generic capacity subsystem. No new signed bytes or generated package content. No metadata/signature aliases across prefixes. No deletion/GC; availability is limited to explicitly supplied retained snapshots.

The driver proof must separate metadata and payloads. Require exact A-prefix metadata URLs and bytes. For a payload outside A's own original paths, require an exact plan alias, an authenticated retained source archive/member, and SHA256/size matching the selected A package; require completed response write and observed response facts match that mapping. Arbitrary same-prefix, wrong family/architecture, unknown source and changed bytes must reject. Keep makecache, the second DNF invocation, real floating mirrorlist, switch timing, cache mounts, native exit checks, exact target/source checks and the 49 native cases.

## Alternatives evaluated

- Remove makecache, force a pinned baseurl/cache-only mode, postpone switching, or count an error as success: changes the real client workflow or erases the failing guarantee; rejected.
- Change signed RPM metadata to absolute canonical payload locations: potentially valid broader design, but requires producer/signature/archive changes and invalidates frozen candidate identities; outside scope.
- Recompose B's archive with all historical A payloads: conflicts with the present single-version candidate contract, changes candidate/archive identities and source inputs, and is unnecessary for a transport alias repair; outside scope.
- Add old mirror bases as mirrorlist fallbacks: changes current exact single-snapshot mirrorlist validation, permits metadata fallback and complicates target freshness; broader and less direct than enumerated payload aliases.
- Dynamic server redirects or invented fixture-only routes: would not prove the production plan and bypass the explicit allowlist; rejected.

## Validation order

Write every case in failure-inventory.md before implementation; retain red outputs from the unmodified planner/driver. Apply the narrow plan/driver repair only after parent/peer review. Run focused structural/routing tests, full existing relevant tests/contract checks once, preserve all protected-source digests and original archives, then freeze the delta for parent independent review. The next exact-head native CI attempt is authorized within this repair/validation task; only its actual result can establish 49-case Linux success. PR merge remains separately approved. No current claim of native Rocky recovery, public repository verification or stable release readiness.
