# Fake Cloud Lease Provider Flow

The fake adapter implements the complete Cloud Lease Provider port without a
cloud SDK, network call, process, timer, or background goroutine.

It derives deterministic network, compute, disk, public-address, access, Quote,
Receipt, and zero-inventory proof data from a normalized Lease Plan. All
returned values are deep copies and every resource carries the complete Lease
tags. Release removes the live entry; repeated Release queries the same exact
Selector and returns a fresh proof without a retained inventory tombstone. A
separate fake provider-idempotency record prevents reuse of the released Lease
identity; it is never reported as live or zero-inventory evidence.

Failure injection covers:

- read-only Quote and List failure;
- partial acquisition retained as `release_pending`;
- a complete acquisition followed by an ambiguous error;
- access mutation failure;
- a configured number of dependency-ordered residual Release attempts; and
- a completed Release followed by an ambiguous error.

The fake exists to verify Controller semantics and no-background dry runs. It
cannot supply real Alibaba capacity, price, permission, or zero-inventory
evidence.
