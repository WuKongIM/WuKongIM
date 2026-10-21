# Generation 2: ordinary RPM metadata compatibility

The five-file implementation is frozen at planner SHA-256 `ae1ca68e28d6dcf487a2cf56af27944ae92accfa795df35a681e62b0772c3da7`, based on packages `782c14602a6b243d893e6abf4d1682b7370736c4`. Root owns the subsequent commit/PR/CI; the implementation agent has stopped repository edits.

The first reviewed generation correctly rejected unbounded inputs but also rejected beta.24's valid ordinary RPM metadata filenames. That original source identity, patch, test results and synthetic CLI receipts remain in `generation-1-resource-fix/`; the first real-input failure remains in the sibling `transport-plan-real/` directory.

The compatibility change only removes the early digest-name rejection and restricts shared immutable classification to full-content-SHA filenames. Every ordinary metadata file must still satisfy the existing repomd reference, compressed/open digest/size and exact closure checks. It becomes the current no-store public entrypoint and remains separately preserved in each generation's immutable snapshot path. It never becomes a shared immutable alias.

Validation:

- `09-plain-rpm-red.log`: representative ordinary gzip filenames, two generations and four unsafe/incorrect metadata cases were fixed as failing tests before implementation (2 tests, 5 failures).
- `10-plain-rpm-related-green.log`: 84 related tests pass, including 31 planner tests.
- `11-plain-rpm-contract.log`: repository contracts pass. `git diff --check` passes.
- `cli/acceptance.json`: actual CLI subprocesses preserve all temporary TEST ONLY input files and produce byte-identical two-generation plans. The fixture is automatically removed. Reproduce with `python3 reproduce-cli.py --repo <reviewed-checkout> --output-dir <new-directory>`.
- Independent reviewer verified both the exact narrow code delta and ordinary same-name gzip metadata with different bytes in both generations: six separate no-store snapshot objects, three current no-store entries, zero shared aliases. Wrong compressed/open identity and a missing retained reference fail with no output.
- The same real beta.24 audit now succeeds. All 44 canonical archive files match the plan's hash/size independently; plan SHA-256 `3c1dd765103ede4839f4d213d58bb8674365091d190008bc39ee84a289aed114`, 31,484 bytes. All three ordinary metadata files are no-store entries and no-store snapshot objects, with zero shared aliases.

This proves offline planning compatibility for the real single generation and fixture multi-generation retention. It does not prove cryptographic signatures, GitHub/control provenance, stable publication or cloud activation. Fixed-base-URL requests spanning generations may safely fail signature/hash validation. Atomic or seamless activation and a future A/B routing rehearsal remain outside this slice.

Archive only the explicit top-level `archive-allowlist.txt`; `archive-sha256.txt` binds those small source/log/receipt artifacts. The allowlist excludes tar archives, package bytes, generated fixture trees and Python caches.
