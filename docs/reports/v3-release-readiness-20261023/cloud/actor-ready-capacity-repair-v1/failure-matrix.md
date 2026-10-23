Approved seam: real actorShard schedule/drain, listener pressure aggregator and observer. Fast isolated tests, no sleeps, clocks, background workers, network or external processes inside the Go tests.

1. 1025 distinct pending connections must retain depth 1025 without declaring preallocation as a hard item bound.
2. Duplicate schedule must not add an entry or observation.
3. Two shards must sum real depth while retaining undefined item capacity.
4. Draining must publish zero depth with undefined item capacity.
5. A delayed older snapshot must not resurrect drained depth.
Existing ordinary/closed actor pressure coverage adopts the same zero-capacity contract. Scheduling, parser, gates and item admission remain unchanged.
