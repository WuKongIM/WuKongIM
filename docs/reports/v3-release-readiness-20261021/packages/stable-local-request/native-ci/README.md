# Native CI increment (written during fixed local-load quiet window)

Base: packages `45619df14b931ef0da3eae7a6e3d5402307daa60`.
Nine-file patch `review.patch` includes the new untracked drill. Exact file
identities and patch digest are in `freeze.json`. The root retains ownership
of commit/push/PR; the author has not committed, pushed, run a native tool,
generated a key or changed a production workflow/manifest.

Validation already completed before quiet: the thin signer adapter's six
focused CLI cases went RED then GREEN (parent `01-local-input-red.log` and
`02-local-input-green.log`). The new native drill and workflow contract have
only been authored/read; four Python files parsed as AST without importing.
There is **no native pass claim**. Use the existing PR TEST ONLY workflow for
real execution while the Mac's 30-minute product load remains isolated.

The new step reuses the same manifest-pinned image already pulled by the
existing metadata drill. It gets readonly control, no network or credentials,
UID/GID 65532, one CPU, 512 MiB, 256 MiB scratch tmpfs and an eight-minute
timeout with named-container cleanup. Token permissions remain contents:read.
Only `native-local-signing.json` is uploaded by the new artifact step.

Container entrypoint:

```sh
python3 -B /control/tests/native_local_signing_drill.py --output-dir /evidence
```

Public evidence names ten cases: apt-files, rpm-files, apt-env, rpm-env,
apt-stdin, wrong-passphrase, full-primary-secret, apt-tampered-release,
rpm-tampered-payload, rpm-tampered-repomd. Positive cases carry existing family
receipts plus independent public-only native verification; source/tool hashes
and observed native versions bind the replay inputs. Private test material is
removed before fresh public verification. Next-only RPMDB rejection separately
confirms the package actually verifies with the current-only certificate.

The fixture builder and GPG test factory are existing repository helpers. No
product binary is built, and neither fixture package is installed/executed.
No exchange protocol or production stable publishing support is introduced.
