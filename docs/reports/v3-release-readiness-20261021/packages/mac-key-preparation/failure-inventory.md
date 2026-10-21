# One-shot Mac stable key preparation: failures before implementation

2026-10-21. No production keys or Keychain operations authorized for this preparation turn.
Only write the operator script, fake-command tests and instructions; static compilation is
allowed. Execution of tests and real TEST ONLY rehearsal waits for root after quiet.

Expected failures to cover before implementation:

- Missing explicit execute intent, reused target directory/symlink, wrong/non-private output,
  or changed trusted validator must refuse without generating or overwriting keys.
- Existing exact Keychain service/account must refuse without retrieving its password;
  locked/denied/unknown lookup failures must not mean absent. Never use -U or -v.
- One security -i invocation must carry exactly one add command, one trailing newline,
  then EOF. Passphrases must never appear in argv, error text, logs or public receipts.
- Failed/ambiguous add must not be followed by reading an old item's password. Only this
  run's successfully created exact entry may be read; mismatching retrieval fails closed.
- All native-command failures must preserve only an explicit incomplete outcome, never
  claim prepared or discard retained encrypted material/Keychain identity implicitly.
- Wrong primary/current/next topology, algorithm, lifetime/runway, fingerprint or short-ID
  collision (including public preview identities) must fail before declaring success.
- Full backup restore missing a private primary or either private subkey is insufficient;
  full restoration must exercise primary certification plus both subkey signatures.
- Primary proof mutates only scratch restoration; original public cert/full backup stay
  byte-identical. Its changed certificate never becomes the public production certificate.
- Bad signature/exact signer/hash, wrong-passphrase unexpectedly succeeding, empty native
  export, missing revocation, or wrong current-only material must fail.
- Current-only export uses exact CURRENT! and original complete public topology; existing
  validator must make the final passphrase-protection/current-only signing check.
- TEST ONLY mode must not reuse default production directory or fixed production Keychain
  names, and no test may execute real GPG/security while fake tests run.
- Cleanup touches only recorded scratch homes. Persistent target and newly created
  Keychain entries survive a failure for operator inspection; no broad deletion.

Evidence limits: fake tests prove control/data boundaries, not Mac GPG/Keychain compatibility.
Real TEST ONLY rehearsal is required before production execution. First red execution remains
pending due to quiet; test source was written before the implementation. Do not label this
preparation as red/green or native-verified until the corresponding commands actually run.

Root pre-execution review correction (before applying it): Keychain account must be
the local account lookup label, not an assertion that an organization custodian was
confirmed. Use getpass.getuser(), preserve exact service/account/keychain binding.
