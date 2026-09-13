# Release completion

A request to release WuKongIM includes Docker images, immutable GitHub binary
assets, documentation, and the applicable signed native package channel.
Finish and verify all four before reporting the release complete. Preserve
existing signing, review, immutable identity, and protected Environment
requirements. A failed stage remains incomplete; never replace an immutable
tag or Release to repair a downstream publication.

Read the [Workflow catalog](../../.github/workflows/README.md) before invoking
Actions. For native publication, also read the current
[package release contract](https://github.com/WuKongIM/packages/blob/main/docs/release-contract.md)
and the exact protected control revision in `WuKongIM/packages`.

## Source release

1. Move the applicable `Unreleased` notes into the exact version section in
   `CHANGELOG.md`. Run the relevant checks and the release-note validation
   described in the Workflow catalog before creating the tag.
2. Require the fixed conversation QPS/P99 and allocation-regression gate for that exact tag in both publishers.
   Verify all 12 single-node/three-node endpoint/page cases plus seven three-node
   mixed/hidden windows (20 endpoint results), clean source SHA,
   profile and binary hashes, zero active runtimes across all roles before/after measurement,
   zero errors/new runtime loads/membership writes, and
   the QPS/P99 floors. Missing evidence or a failed case blocks publication;
   see the Workflow catalog for the fixed profile and retained artifacts.
   Complete the Docker and binary publishers for that exact tag. Verify the
   same source SHA and multi-platform digest in all three image registries,
   plus the immutable numeric GitHub Release ID, complete asset set, checksums,
   and provenance. For native publication, retain the source DEB/RPM digests.
3. Verify the documentation deployment for the release. Its success is
   independent of APT/RPM publication and does not prove package availability.

## Signed native package publication

Prereleases use the `preview` channel. Stable publication remains disabled
until the package repository's required storage and signing prerequisites are
fulfilled. Do not publish stable versions into preview or report complete
delivery while their applicable channel is unavailable.

The native workflows currently require explicit dispatch; pushing a source
tag does not run them. A release operator must complete the following sequence
as part of the same release task:

1. Run `source-release-preflight.yml` in `WuKongIM/packages` from `main`, with
   the exact numeric source Release ID. Require successful checksum, immutable
   identity, provenance, and package metadata validation.
2. Read the live `https://packages.githubim.com/status.json`, then validate its
   immutable audit Release before selecting it as the base. Check retention
   and capacity; complete any required retirement before adding a version.
3. Run `native-package-audit-draft.yml` from the exact current package `main`
   SHA with `operation=add_release` and the target version without `v`.
   Retain the returned numeric audit Release ID and previous control SHA.
4. Prepare and review the `manifests/channels.json` change: add the exact
   version, source SHA, numeric source/audit Release IDs, and unsigned source
   DEB/RPM SHA-256 values. Set publication to the new audit ID and verified
   base ID. Preserve existing versions and bootstrap bytes. Run the package
   contract tests and required PR checks, then merge the control change.
5. After other package production runs finish, run
   `native-package-audit-bind.yml` with the reserved audit ID, previous control
   SHA, and exact merged `main` SHA. Require success before dispatching
   `native-package-publish.yml` with that same merged SHA and audit ID.
   Do not advance package `main` while binding or publication is in progress.
   If `main` advances after binding, follow the package contract's new-draft
   recovery procedure; never move the reserved tag.
6. Wait for the entire publisher, including public clean-client verification,
   to succeed. It signs APT and RPM separately, seals one immutable audit
   snapshot, deploys the complete repository, and verifies target-version
   downloads on Ubuntu, Debian, Rocky Linux, and AlmaLinux. New-release
   publication also requires installed CLI acceptance on all four distributions.

Never copy signing credentials into the source repository or bypass signed
metadata verification. Recover only the exact matching draft or immutable
snapshot using the package repository's existing recovery contract.

## Public availability and delivery evidence

Check the public status reports the intended target version and audit ID, and
that the APT `Packages` index contains the exact mapped DEB version (for
example, `v3.0.0-beta.9` becomes `3.0.0~beta.9`). Require the publisher's clean
APT/RPM client checks to select and download that target, not merely refresh
old metadata. A GitHub asset URL, a successful `/repo` invocation, or a Pages
deployment status alone is insufficient. If propagation delays leave the old
snapshot visible, retain an incomplete status until public checks pass.

For an already configured Debian/Ubuntu client, the operator can then run:

```sh
sudo apt update
apt-cache madison wukongim
sudo apt install -y wukongim
```

For releases containing the unified operator CLI, each clean native client
also checks that `wkcli version --output json` matches
`wukongim version --output json` in version, full commit, and build source.
Both identities must also match the reviewed snapshot's exact source SHA and
version, with build source `release`; two equally wrong binaries cannot pass.
The public package validator's `--verify-installed-cli` gate installs only
snapshot-verified downloads in separate credential-free containers. In addition
to help commands, it runs `bench validate`, queries a known synthetic offline
user, and completes `migrate diagnose` on a fixed stopped-v2 fixture. Invalid
inputs must fail, source bytes must remain unchanged, and diagnosis must not
create a target. Every command and container has a bounded deadline.

All four distributions must return `installed_cli_verified=true` with per-client
functional receipts. Any failure keeps release delivery incomplete, even after
Pages deployment. Signing and pre-publication download-only sandboxes never
execute product payloads. Official Linux/macOS archives contain both binaries.

To repeat this gate for the currently reviewed public snapshot without signing
or publishing, dispatch `native-package-cli-acceptance.yml` in `WuKongIM/packages`
from `main`, setting `expected_control_sha` to its exact current protected SHA.
The workflow derives the audit and version from reviewed control, validates the
immutable archive and public identities, and retains receipts for 90 days. Do
not advance package main or run a competing publication while it executes.

The repository bootstrap keyring has its own version. It is not the server
version. Package upgrades do not restart the running server; service restart
remains an explicit deployment operation under the Linux deployment runbook.

The final release report records the source tag/SHA, GitHub Release link,
verified image digest, documentation result, native publisher run, immutable
audit Release, and public exact-version verification. If any stage is blocked
or fails, name that stage and keep the overall release incomplete. Clean up
merged task branches and clean worktrees after their tips are verified in the
target branch.
