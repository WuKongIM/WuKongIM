# SHA512 transport aliases: failure inventory before implementation

Seam: existing make_plan(..., composite=True, test_only=True). This repairs generation-2 native evidence: the default APT client requests a signed SHA512 index URI, gets 404, then falls back to a floating index. No ForceHash/client weakening or fixture-only route is permitted.

- Both fixed indexes need canonical SHA512 immutable aliases sourced from their exact original archive member; aliases must carry the original SHA256/size and immutable cache policy.
- All supplied stable generations need their aliases retained; repeated plans must be deterministic; candidate archives, preview plans, snapshot inventories and producer bytes stay unchanged.
- Rejected native verification must stop before alias production. The structural test isolates only the external native verification boundary; it is not cryptographic acceptance.
- A source read exceeding the metadata limit or differing in SHA256/size must fail. Hashing and alias identity must use the same bounded raw read.
- Derived aliases and archived aliases must use the same URI conflict rejection, never silently select conflicting bytes.

Authorized files: scripts/plan-object-storage-publication.py and tests/test_composite_storage_plan.py. Native TLS/fault-selector work is separately owned. No GPG, containers, public writes, producer/control edits, retry or publication here. Existing archive may contain only SHA256 paths; adding public SHA512 aliases must not invent archive members or mutate signed bytes.
