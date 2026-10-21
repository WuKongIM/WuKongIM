# A/B public fixture implementation — ready for independent review

Scope: exactly two shipping files at packages 335cd2569d2b62de577916ed86c097fd3a22547e. No commit/push, producer/control/workflow edit, GPG, Go build, Docker or native package invocation was performed by this subagent.

## Exact callable contract

`tests/native_stable_candidate_drill.py: export_client_generations(*, output_root: Path, binaries_root: Path, trusted_root: Path, product_source_sha: str) -> dict`

* Caller supplies an existing empty nonlinked output directory; binary root contains `3.0.0/{wukongim,wkcli}` and `3.0.1/{wukongim,wkcli}`; each is a regular, single-link executable, 64-bit little-endian amd64 ELF, 64 bytes to 256 MiB. Structural ELF validation does not attest the source commit: the driver must supply real builds and independently check their compiled identity.
* `product_source_sha` is exactly `fc18221282f289088ec9b4282fda8e0bfc67a3b8`. Trusted builder bytes must match checked-in trusted-toolchain.json. Execution requires isolated nonroot Linux and the existing native tool set. Environment is temporarily reduced to PATH/HOME/LANG/LC_ALL/TZ and restored even on failure.
* Generates two family TEST ONLY keys once, then A=product3.0.0/bootstrap1.1.1 and B=product3.0.1/bootstrap1.1.2. Controls copy the same certificates and remain stable-disabled. Each has fresh unsigned→signed→composed bytes, canonical archive and independent audit verification; candidate source/control/release/asset IDs remain null under the existing test-only schema.
* All key factory homes and passphrase files are removed together in finally. Both generations then pass core verification/archive/audit with no private material. Only eight public allowlisted inputs per generation are staged and exported. Export exceptions remove this call's own A/B paths; process kill is not claimed to guarantee cleanup.
* Result: `test_only: true`, `source_sha: <fixed build input>`, `secret_tree_removed: true`, `generations: {A: {...}, B: {...}}`. Each generation contains `version`, `bootstrap_version` and POSIX paths relative to output root: `control`, `source`, `plan`, `archive`, `receipt`, `apt_receipt`, `rpm_receipt`, `site`. Paths are `A/control`, `A/source`, `A/plan.json`, `A/<canonical archive basename>`, `A/<canonical receipt basename>`, `A/apt-receipt.json`, `A/rpm-receipt.json`, `A/site`, and analogously B. Nothing is printed. Failures raise; driver should record a bounded failure category rather than raw child exception text.

`tests/native_source_metadata_drill.py: build_packages(root, deb_version, rpm_version, *, epoch=0, binary_payloads=None) -> (deb_path, rpm_path)`

`binary_payloads` is the exact mapping `{'wukongim': Path(...), 'wkcli': Path(...)}`. Both native staging trees contain both supplied executable bytes; RPM post-install stripping/debug extraction is disabled for these explicit test binaries. Explicit fixtures retain systemd dependencies. Omitted argument retains the original data-only metadata fixture bytes and native commands.

## Tests-first record and limits

Failure inventory was written first. Preserved red/green pairs: 01→02 new payload keyword; 03→04 ten invalid payload classes; 05→06 wrong source at new export seam; 07→08 unusable output/missing generation; 09→10 first signing command failure removes private tree and exports nothing. 10 has 6 top-level tests, including ten mutation subcases and four output subcases, all pass. Every external native command was intercepted at subprocess boundary; structural ELF byte fixtures are never claimed to be compiled product binaries.

11: all 15 existing default candidate unit tests pass. 12: default stable/preview metadata staging bytes/modes and external command arguments match the baseline; existing metadata main/fixture_receipt and candidate pipeline/main/generate_control/sign_family ASTs are unchanged. The native metadata7 and candidate31+8 are preserved by source/default behavior, but were not re-executed in this subtask. Full cryptographic A/B closure and real installed CLI acceptance remain for parent-authorized Linux execution.

No producer changes were required in this implementation. Existing TestKeyFactory GPG calls have no per-command timeout; the parent container/driver deadline must bound this inherited fixture operation. Existing signer/prepare/compose/audit commands retain the bounded invoke deadline.

`source-freeze.json` binds the exact two files and diff. `allowlist.txt` and `archive-sha256.json` bind this evidence directory without recursive cache discovery.
