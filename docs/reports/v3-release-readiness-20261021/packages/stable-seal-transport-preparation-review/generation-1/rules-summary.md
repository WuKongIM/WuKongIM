# Applicable rule summary

- Inherited product-root AGENTS.md: test-first; isolated systems need failure inventory before implementation; run directly related validation; prefer scoped worktrees; user-visible changes need CHANGELOG; no over-engineering; preserve protected automation controls.
- Read applicable module FLOW before deep work. packages c8aae37 has no AGENTS.md or FLOW.md under its checkout; no valid package FLOW applies. This was a source/proposal review only: no implementation, tests, server, external API or workflow invocation.
- packages README trust boundary: reviewed manifests/certificates are control; stable remains disabled and CI lacks stable private keys; source preflight and actual publication are distinct.
- docs/release-contract.md: reserved numeric audit identity and create-only tags, immutable two-asset state machine, no asset replacement; candidate control SHA is declared, local evidence-byte verification is not live provenance; future sealing/publication must repeat source/immutable identity checks.
- docs/object-storage-planning.md: offline plans cannot prove GitHub provenance or activate anything; retain all declared generations, no deletion; current v1 preview behavior must remain.
- Proposal mandate: only four existing helpers plus tests/docs, no producer-core changes without concrete review, no channel/control/key/production workflow/cloud modifications, no actual seal/publish execution in this slice.
