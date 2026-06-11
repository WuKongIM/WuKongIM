# Phase 1 — channelwrite allocation reductions

## Result
- Allocation-free channel-key hashing landed (inline FNV-1a, drops `hash/fnv`).
- Future buffer pooling was **dropped**: measured +1 alloc/op (regression), not a
  reduction. Root cause: no safe release point under the current lifecycle (a
  Future may be awaited more than once), so the pool never warms and we pay
  `Get` overhead on top of the `make()`. The struct/channel/slice allocations
  on the SEND hot path are restructured safely in Phase 2 instead.

## Numbers (after Phase 1, count=6)
See phase1.txt. Hot channel and many-channels alloc counts are unchanged from
baseline (41 / ~60) because the hash was already stack-allocated by escape
analysis on this platform; the win is portability + dropping the dependency,
not a measured alloc delta here. Real alloc reductions are expected in Phase 2.
