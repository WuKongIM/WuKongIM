# Object-storage planner pre-implementation failure inventory

Authorized seam: local descriptor + canonical archive/receipt + independent control files to a deterministic output plan. No cloud, credentials, key generation, publisher mutation or signature/provenance claim. Scope: preview add_release audit snapshots only; all other channels/operations explicitly rejected. No deletion plan.

Before implementation, CLI tests will cover:

- Current preview audit produces byte-identical output across invocations and relocation, preserves inputs, and labels all unperformed verification explicitly false.
- Float Packages.gz / repomd.xml / repomd.xml.asc remain entrypoints; APT by-hash, versioned payloads and digest-named RPM metadata are immutable aliases.
- Explicitly supplied prior audits preserve old by-hash and payload objects with no delete action; input order does not affect output.
- Duplicate audit identity; same immutable public URI with different bytes across valid snapshots.
- Corrupt archive or external receipt digest; jointly rewritten archive/receipt fails independent anchor; wrong expected audit/control/source identity; modified control bytes under unchanged SHA label.
- Stable channel request/enabled control/strict stable version in preview; unsupported retirement/bootstrap operation; excess snapshots and cumulative declared byte budget.
- Canonical tar path traversal, symlink/hardlink and duplicate member; ambiguous encoded public path; unlisted extra site object.
- Missing APT by-hash; missing RPM metadata/payload; invalid or duplicate/escaping repomd location; content or open-content digest mismatch; unsigned receipt changed while archive remains pinned.
- RPM metadata name without byte digest or native zstd requirement rejected in this pure-Python first slice, rather than silently classified as permanent.
- Existing output, symlink output/parent, duplicate JSON keys and unknown input fields fail without input mutation or partial success file.

Existing composer/audit fixtures contain TEST ONLY synthetic signing bytes. These tests validate transport consistency only. They do not establish cryptographic trust or successful publication.
