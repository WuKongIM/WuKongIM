# Independent bounded review: RPC capacity design

**Final result: 0 remaining findings.** One substantive evidence-attribution issue was reported and corrected; only that correction was rechecked. This is documentation review, not implementation validation, causal confirmation, or three-node/2k/release acceptance.

- Document: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-qualification/docs/reports/2026-10-23-rpc-capacity-system-design.md`
- Final reviewed SHA-256: `3578e20fbe01694791126cddbb5f5683d00727729b976b11dd01054a26ee556e` (19,349 bytes).
- Source worktree HEAD: `8b4c12b1f6722f969b907fdfd2b2d9425dac5aa9`. The proposal is untracked; its content hash identifies this review.
- Authorized plan C comparison SHA-256: `c45c653d906e9bc70315280866be17d21707e7ff6b677f7ae1ba1135de953407`.
- No source edits, Go execution, product execution, HTTP, DB reads, new profiles, or additional tests.

## Finding and closure

Initial document SHA `969723328fec8cb5d4efee01efe7d7cdcc60d5de7f0b1f40df2f94496d056652`, lines 21–24: line 21 explicitly named 5f, but the six GOR counts in line 23 came from exact 8100. Line 22's “other two samples” could join its selected timeout to the adjacent 5f samples. The preserved GOR manifest identifies source `8100d7fd7a9455029e8de4f6370f140ff07612bc`, tree `ff1a0135caa5e933550fa12f9ac601b786af8980`.

**Corrected:** final lines 22–23 explicitly identify the old 8100 profile round and prohibit extrapolation to 5f. Line 32 names both run directories and explicitly denies same-request pairing across rounds, cuts, and interval metrics. Closed; no remaining finding.

## Requested design checks

The final proposal separates process/direction budgets and terminal ownership, prohibits partial-budget blocking, reserves downstream/response/cancel progress, and requires real execution-slot release for deferred work. One ownership model does not imply a global mutex. Per-call 4096 availability requires a separately versioned bounded continuation design and preserves the old-peer path. Mixed mutation cancellation and Raft/Channel ordering remain guarded. Static connection collision is a hypothesis, not demonstrated causality. The 120-second diagnostic remains explicitly insufficient for the original sustained acceptance gates. No further substantive issue found in this finite review.

## Applicable instruction freeze

- `AGENTS.md` at `8b4c12b1f6722f969b907fdfd2b2d9425dac5aa9`: `2220f49835b8dba20bafbc277854421222765be52e874fc6ee994bcd3ee0d746`
- `CONTEXT.md` at `8b4c12b1f6722f969b907fdfd2b2d9425dac5aa9`: `2c6c14ebb838dcfb498b6b6c130f0a500c21dcd3a49343bb467961921bd8168c`
