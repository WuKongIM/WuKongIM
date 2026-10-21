# Lock-wait wording clarification

The reviewed diagnosis remains immutable: `../pr1024-gen3-timeout-diagnosis/allowlist.json` SHA256 `9debf2d669b77cb2565e7af066eee2f94ab2f44c907a2f72b69f6202f9fee072`; its README SHA256 `d16e30767d34dd6040dad372ccc388961f57ddaa3e520e5083614316d5f39c04`.

Original README lines 16–17 say that the increases “are lock waits” and that “lock waits are directly observed.” Those two phrases require this qualification: safe CI events directly show CONT-to-child intervals and overlapping root-test spans. Together with the source-defined admission/locking order, they **support an inference consistent with admission or lock waiting**. They do not directly observe lock acquisition/release time, isolate all OS scheduling delays, or uniquely attribute the elapsed increase to lock contention.

The detailed boundaries already stated in the original README lines 37–49 remain valid. The disjoint timing arithmetic, test counts, original outcomes and release verdict are unchanged. No build, readiness, actual workload or drain duration is reconstructed from missing runtime receipts.

Parent review accepted this clarification. The independent review retains one historical P3 wording finding, **closed by this appendix**, rather than altering the frozen diagnosis or claiming zero original findings. Subsequent reports and PR prose should use the qualified inference above. No source, CI, workflow budget, provider state or evidence worktree was changed.
