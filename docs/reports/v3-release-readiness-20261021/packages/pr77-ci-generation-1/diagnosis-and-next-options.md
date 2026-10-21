# Bounded first-failure diagnosis and next options

The failed run is retained, not converted to a pass: PR #77 head `19ce29fb2233b3039e29c503963e27bd1fab972a`, native run `39660717995`, attempt 1. Zero complete stable scenarios passed; the first baseline failed and 48 were not reached.

## Proven boundary and remaining uncertainty

The exact installed Alma shell is `bash -euc`, inside `timeout --kill-after=10s 600s`. Its ordered chain is candidate SHA check → public-key import and `rpm --checksig` → `dnf --setopt=localpkg_gpgcheck=1 --setopt=timeout=60 install -y python3 /candidate.rpm` → `python3 /acceptance.py ... >/evidence/installed-cli.json`. Full 137-byte stdout/stderr SHA was independently recomputed. SHA/signature checks succeeded, the final visible line reports Alma AppStream 30 MB at 110 kB/s / 04:37, then exit 124 at 600.151 seconds. The independent 650-second driver timer did not fire.

No dependency-resolution-complete or completed package transaction appears in that complete output. The fixture HTTP trace has 33 completed 200 responses, all serial 2 / normal A; it does not observe Alma's external dependency repositories. The preceding Alma download-only and signature check succeeded. Three earlier installed subprocesses exited 0 inside the unfinished baseline; no completed clients-A/B identity receipts were exported.

`tests/native_stable_clients_drill.py:848–851` raises immediately on the nonzero runner result. Therefore host `_installed_cli_receipt` at `scripts/validate-production-package-clients.py:1578` was not reached for Alma. The container's Python stdout was redirected and its individual output file was not retained. We cannot prove from absent console text alone that Python never started. The exact timeout subphase, selected external mirror, DNS/transfer state and resource pressure remain unknown.

The #76 first failure used the identical Alma image, shell and 600-second limit, ending after 600.218 seconds with AppStream 30 MB / 05:59. All four product binary SHA/size pairs match this run. This is repeated failure-boundary evidence, not proof of the same unique cause; #76's later successful attempt cannot pass #77.

## Old-path scope check

The production validator is byte-identical to be32. Existing 49-case `exercise` and product-build ASTs are identical; fixed-source/ELF export guard prefixes are identical. New shared export defaults `include_preview=False`; existing seal defaults `preview=None`; signing defaults `channel='stable'` and yields the original stable paths. Original client calls do not pass new options. A/B binary keys are populated and validated before `payloads.get(version)`, preventing a data-only fallback on that path. The helper's result adds two false metadata fields, so the entire helper/result shape is not byte-identical. Actual fixture export reports data_only=false and exact fixed source, followed by valid native downloads/signatures. See `default-client-path-review.json` and the frozen helper delta.

## A — one identical failed-job-only repeat

There is evidence to justify one bounded distinguishing experiment at the same SHA, pinned image, command and thresholds: native download succeeds, identical binaries are verified, and the repeated boundary is the normally networked dependency/installation chain. It could distinguish repeatability. A pass would restore that run's gate only after all 49 cases, identities, caches and cleanup are checked; it would not establish the first timeout's unique cause. Given the recurrence, another uninstrumented pass leaves the principal observation gap. No repeat is authorized or dispatched by this collector.

## B — preferred: fixed phase evidence before a new head's first CI

Minimum robust scope is approximately five files: `scripts/validate-production-package-clients.py`, `tests/native_stable_clients_drill.py`, their existing tests (`tests/test_installed_cli_acceptance.py` and `tests/test_native_stable_clients_drill.py`), and `CHANGELOG.md`.

Add only literal begin/completed stdout markers around SHA check, RPM signature check, dependency install, and installed CLI. For APT use only applicable phases; do not invent a separate DEB signature check. Keep every original command, ordering, strict shell behavior, exact identity/receipt gates, image, normal dependency network, 600/650-second limits, resource caps and 49-case inventory unchanged. These events never authorize acceptance.

The driver currently retains only the last 4096 output bytes (`run_command:108`), so markers alone can disappear behind verbose DNF output. Preserve a bounded fixed enum of phase events from the existing 8 MiB bounded capture, including failed/timeout completion paths, without copying arbitrary lines or secrets. A missing completion remains unknown/incomplete, not a successful phase. Do not add retries, extend timeouts, prewarm repositories, change mirrors, or treat event presence as a passing check.

Write all failure cases before implementation: exact event order; failure at each native stage prevents later events; missing/failed CLI cannot report complete; original nonzero/timeout remains nonzero; events survive >4096 bytes of unrelated output; missing/duplicate/invalid markers remain diagnostic only; no variable/credential text; exact command/resource/gate preservation. Use deterministic bounded shell/control probes with fake command outcomes for marker flow, then the normal new-head native CI for real package behavior. Preserve both historical failures.

This crosses M1's formerly frozen production-validator boundary and needs an explicit new review scope. Local CODEOWNERS is `* @tangtaoit @No8blackball`; it does not designate a special path exception. The validator implements the production installed-CLI gate in `docs/release-contract.md:559–574`; it is not one of the twelve signing/archive producers or Issue/Review Agent control files. Hosted branch/ruleset protection was not queried, so no claim is made that the path is unprotected. This document is a read-only proposal; no source, CI, PR or cloud mutation occurred.

## Final decision

The parent selected B and assigned implementation to another agent. A will not be run. `phase-diagnostics-plan.md` is the final exact five-file handoff. This archive preserves the uninstrumented failure; it does not claim to identify the timeout subphase.
