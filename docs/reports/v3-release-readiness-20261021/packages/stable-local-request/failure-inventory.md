# Slice A: failures written before implementation

Baseline: WuKongIM/packages `45619df14b931ef0da3eae7a6e3d5402307daa60`.
Scope: one TEST ONLY `prepare-rpm` command; no signing/response/metadata command.
Tests exercise the CLI with a fake external GPG executable, never fake internal
validators. This proves protocol/runner behavior, not real cryptography. No test
key generation is needed. Native public-certificate rejection will be reported
separately if executed after the root's quiet window.

1. Fail closed on noncanonical, duplicate/unknown JSON fields, wrong schema/mode,
   boolean numeric IDs/sizes, malformed source/version/control/toolchain/audit,
   inventory duplicates/order/roles/missing or extra evidence, absent payloads.
2. Bind the caller's external descriptor digest before parsing/reading inventory;
   coherently editing descriptor contents without changing that anchor fails.
   Context is a caller assertion, not independently authenticated provenance.
3. Before native commands, reject wrong hashes/sizes, real oversize files,
   absolute/dot-dot/backslash paths, leaf/parent symlinks, hardlinks, nonfiles,
   extra/missing input files, and output overlap/existing targets. Input root is
   resolved once; relative member traversal must never follow symlinks.
4. Public certificates: secret packet or sec/ssb record; wrong certificate bytes;
   malformed topology, current expiry/next runway, RPM ECC/RSA2048; duplicate
   fingerprints/full/16/8-bit hex IDs within/cross families or with preview.
   Never import keys, inspect a secret home, read secret environment/Keychain/NAS,
   or use the private-key validator session. GPG reads checked temporary copies.
5. Bound native output and timeout, clear inherited credentials, use only fixed
   public commands with explicit temporary HOME/GNUPGHOME and tool path. Do not
   echo packet contents, stderr, or environment on errors.
6. Success has deterministic request bytes and digest, fixed rpm/rpm_payloads,
   null parent, source/control/audit anchored through descriptor SHA, complete
   checked artifact inputs, no declared signature hashes for future outputs.
   `plan_only`/`test_only` true; source/provenance/unsigned/signatures/GitHub/cloud/
   control-commit/publication verification false. No ledger/replay/signing claim.
7. Failure leaves no output/temporary result and never overwrites an existing
   output. The CLI rejects signing/response/metadata/production subcommands.
8. Existing manifests, signing helpers and workflows must retain baseline bytes.

Quiet window: tests are authored now but are not executed until root releases it.
