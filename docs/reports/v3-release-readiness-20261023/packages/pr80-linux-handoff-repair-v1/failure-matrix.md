# Linux native-to-host handoff failure inventory (before implementation)

1. Producer UID 65532 differs from host SDK/cleanup UID 1001. Private mode-0700 resolved audit directories deny `lstat`/reads; mode-0755 trees deny unlink to another UID. Verify actual Linux owners/modes; Mac bind mounts can conceal this.
2. Changing only read permissions does not make cleanup possible; changing only cleanup does not permit the SDK fixture to consume all bundle inputs. Require actual bundle validation and complete SDK receipt plus scratch removal.
3. Producer and consumer identities must be the same explicit non-root UID/GID. Root/malformed/unavailable identity must fail before Docker; no fallback root, sudo, chown or world-readable private-tree repair.
4. All original non-root container protections, resource/time limits, network isolation, read-only source/public input mounts and exact source/digest anchors must remain.
5. Empty owned scratch can be private to that shared identity. No permission widening of public-export or native private inputs, and no changes to existing native workflow blocks.
6. A failed preparation, SDK exception or cleanup must remain failed, preserve the first error and emit only bounded public receipts. Outer always-cleanup must still independently observe container removal and work removal.
7. Local reproducer must use Linux tmpfs for actual UID enforcement; host bind-mount behavior is not Linux runner evidence. Preserve the first failure and run exact same native/SDK semantics under the repaired identity.
8. Existing SDK tests, native fixtures, public source bindings and refusal cases must stay unchanged; a permission fix cannot replace successful full 20-case native-to-SDK validation.

Initial CI failure is immutable at ../pr80-ci-generation-1; no blind rerun. No cloud, credentials, new keys, privileged production operations or CI dispatch are part of this repair.
