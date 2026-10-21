# Local family-signer input adapter: failures before code

Baseline: packages 45619df. Root narrowed the task to the existing signer;
superseded request-protocol tests are not part of this implementation.

Only expose the validator's existing file/env/stdin secret input and file/env
passphrase input on the family signer CLI. Do not alter native signing or
validation semantics. Keep CI's original env arguments working.

Focused lightweight cases written before implementation:

1. Missing input selection or two simultaneous selections must be argparse
   failures (both secret and passphrase groups); no output or native process.
2. File and stdin selections must parse into the existing validator Namespace;
   both APT and RPM can use them. Existing env choices preserve defaults.
3. Existing file validation must reject missing/link/hardlinked files, mode
   0644 secrets/passphrases, noncanonical base64 and multiline/empty passphrase.
   Rejection must occur before invoking GPG, and stage/output must be absent.
4. stdin must reach the same bounded base64 validation and reject malformed
   input, while a valid base64 candidate proceeds to passphrase checks.
5. No secret bytes may appear in errors or a receipt. Use only synthetic
   non-key material in these short tests; no real GPG key generation.

After root's fixed-load window: real tiny-native TEST ONLY signing and public
verification will prove positive file/stdin paths, wrong passphrase/secret key
topology failures, and actual APT/RPM signatures. Existing native key tests
remain separate; this short run cannot substitute for that evidence.
