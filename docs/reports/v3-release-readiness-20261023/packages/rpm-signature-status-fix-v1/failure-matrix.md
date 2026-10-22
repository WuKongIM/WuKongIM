# RPM signature status parsing — tests before implementation

Scope: packages base 7255102de81b45c23d52e908ccdde1b7799ee3f5,
production verifier, focused parser tests, and operator Changelog only.

The independent native paired probe proved identical signed bytes/certificate
pass under good/ and fail under bad/. It does not establish the cause of the
earlier unrecorded native failure.

Before implementation, exercise these outcomes against the original function:

1. Valid RSA/SHA256 status accepts ordinary paths and paths containing bad,
   NOKEY, NOT OK, or signature text; path text is not cryptographic status.
2. Valid signing key IDs containing hexadecimal bad remain accepted.
3. Unsigned digest-only output rejects, including a filename containing signature.
4. A missing or different package header rejects; no arbitrary prefix stripping.
5. A nonzero rpmkeys result rejects even with an OK signature line.
6. NOKEY, NOT OK and BAD signature/digest statuses on stdout or stderr reject,
   even if a synthetic process claims exit zero alongside an OK signature.
7. Missing, duplicate, unsupported-algorithm and wrong-candidate signature rows reject.

Use native signed public fixture bytes and public certificate for a repeatable
same-bytes good/bad-path verification after the focused parser repair; also
reject corrupted payload, unsigned package and wrong signing candidate. No
production signing keys, network, package installation or new signing required.
