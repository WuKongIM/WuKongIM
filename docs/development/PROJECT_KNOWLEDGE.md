# Project Knowledge

## Channel Runtime

### Long-poll leader lease refresh
- A channel leader metadata refresh that only renews `LeaseUntil` must preserve existing leader-side lane sessions and follower cursors.
- Clearing the lane cursor on a lease-only refresh can make the next replication fetch start from offset `0`, preventing follower progress and HW from advancing for the next append.
