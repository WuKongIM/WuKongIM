# Stable public registration: failures fixed before implementation

Authorized seam: existing `scripts/validate-control.py --root` control validation. Registration is public-only and must not activate signing/publication. Tests use only the two operator-approved public certificates copied from production-public and the public receipt; no private directory or Keychain access.

- Historical roots with no declaration and no stable certificates remain valid.
- Complete actual public certificates plus exactly one role declaration validate; current preview return/state remains unchanged.
- Manifest alone, one missing certificate, certificates without declaration, symlinks/hardlinks and unlisted key material reject.
- Unknown schema/fields (including enablement, Environment, passphrase/secret inputs), malformed roles, duplicate JSON fields and unordered/duplicate historical roles reject.
- Primary/current/next mismatch, swapped current/next and wrong certificate/family fail existing certificate checks.
- Every full/16-hex/8-hex fingerprint across both channels/families remains globally unique; collision failures precede certificate acceptance.
- Existing 30/45/180-day and RPM RSA checks continue through the same public certificate helper, with existing regression suite retained.
- Channels, preview manifest/certificates, composer, verifier, archive and workflows stay byte-identical. No extra stable objects enter a preview snapshot.

First run writes tests before validator changes. Public certificate files are input data, not generated test keys. No new protocol or private signing adapter is in scope.
