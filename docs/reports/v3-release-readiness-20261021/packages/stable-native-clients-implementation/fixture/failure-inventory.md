# Fixture boundary failure inventory

Approved seams: `build_packages(..., binary_payloads=...)` and `export_client_generations(...)` only. Tests use the filesystem and intercepted external native-command boundary; structural ELF test bytes are never represented as compiled product binaries or native acceptance.

1. Missing/extra payload names, symbolic/hard links, directories, non-executable files, wrong/truncated/non-amd64 ELF, empty/oversized files must fail before native commands.
2. Supplied server and wkcli bytes must enter both DEB and RPM packaging input trees with executable modes; default metadata fixture remains data-only.
3. Export rejects wrong fixed product SHA, nonempty/linked output and missing/invalid A/B source binaries before signing or export.
4. A=3.0.0/bootstrap1.1.1 and B=3.0.1/bootstrap1.1.2 use separate scratch controls and unsigned package identities, same two generated TEST ONLY family certificates, with stable disabled and candidate GitHub IDs/SHA null.
5. First native error stops generation. Key homes and passphrase files must be removed in finally before verification/export; cleanup failure must fail closed. No private scratch or raw child output is exported.
6. Only each generation's public control/source/plan/archive/audit receipt/family receipts/site paths leave scratch. Existing output cannot be overwritten; failures must not leave a partial public generation.
7. Existing default metadata 7 and candidate 31+8 cases remain unchanged. Full native closure and installed binary identities are reserved for parent-authorized Linux CI, not inferred from mock tests.

No GPG, Docker, Go or native package command may actually execute during this subtask.
