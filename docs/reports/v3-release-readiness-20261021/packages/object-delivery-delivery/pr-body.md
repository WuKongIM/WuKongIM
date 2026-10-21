Transport plans describe publication objects, but operators cannot yet materialize their exact source bytes or exercise partial upload/activation outcomes before connecting a provider. This change adds a local preparation command for the existing preview and composite plans, plus an in-memory delivery simulation with create-only readback, exact URI routing, configuration drift checks, and bound client-verification receipts.

The simulation observes the current activation control before upload and again before activation, and requires stable client receipts to match this preparation’s original input and plan digests. It preserves source/alias identities, signed bytes, original verifier receipts and incomplete side effects. Production stable stays disabled; real OSS/CDN adapters and public publication remain separate work.

Validation:

- Independent Standards and Spec reviews passed on the final five-file patch.
- New failure tests were written first; 75 related tests and 4 workflow contracts passed.
- Full local suite: 553 passed, 1 existing skip because native RPM build tools are unavailable on this Mac (554 total).
- The same validated tree was rebased onto current main `620bdfa1804c732dc1b3b072bb64dbe6f0ae0475`; no source bytes changed. Linux PR checks will validate this exact head.

Includes an Unreleased operator-facing changelog entry. No signing keys, production resource configuration, publisher workflows or channel manifests changed.
