# Spec — v3 socket-cleanup increment

**No findings. The earlier non-blocking socket-cleanup observation is resolved.**

The failure matrix requires four existing clean/refused/converted/primary-error scenarios to emit zero ResourceWarning after collection while retaining independent attempt counts, failure/cleanup receipts and the primary error. The two-file delta satisfies this: the self-check and deliberate test connections now own sockets in context managers, including exception paths. `RuntimeError`, refusal classification, the independent counter and all existing receipt assertions are unchanged. The one-attempt self-check remains inside the connect guard.

Tests-first evidence binds the first RED to the frozen v2 driver: 16 methods, four failing subcases. The new warning assertion was present before the implementation changed. The v3 GREEN records 16 methods, zero failures/skips and no ResourceWarning. These supplied results were inspected, not rerun.

All seven frozen source hashes matched before/after; only the intended two test files differ from v2. Provider, workflow, SDK contracts, documentation and Changelog hashes remain unchanged. The previously reviewed v2 evidence remains applicable to unchanged behavior, but the final 20-case native run on v3 still awaits Linux CI. This increment neither substitutes that validation nor makes a production/cloud acceptance claim.

No source edits, SDK/native execution, containers, cloud calls or network probes occurred during this review.
