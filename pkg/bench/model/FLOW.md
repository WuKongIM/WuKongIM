# pkg/bench/model Flow

## Responsibility

`pkg/bench/model` owns shared wkbench schema, deterministic plan, report, rate,
and bench/v1 target API DTOs. Both the black-box `cmd/wkbench` implementation
and the promoted `cmd/wukongim` benchmark-only target surface may import this
package.

## Boundaries

- Keep this package as data model and lightweight parsing helpers only.
- Do not import `internal`, `pkg/cluster`, or server runtime packages from
  here.
- Keep exported fields documented because these structs define config, YAML,
  JSON, and HTTP API contracts.
- `DigestScenario` hashes the canonical JSON form of the fully loaded effective
  scenario so lifecycle tags and Analysis MCP refer to the same workload.

`Worker.Client` is an optional complete per-session capacity profile. When it
is present, every capacity is positive; when it is omitted, wkbench retains the
existing generic client defaults. The coordinator copies only the selected
worker's profile into that worker's assignment and never places worker control
credentials in the assignment payload.

`Worker.TCPSource` is an optional complete local TCP source pool. Configured
addresses are unique, valid, non-unspecified IPv4 addresses, and the inclusive
port range is bounded by `1024..65535`. `TCPSourceCapacity` is the Cartesian
product of the address list and port range. After worker weights produce final
identity ranges, the planner requires each configured capacity to cover that
worker's range. The coordinator deep-copies the selected worker's address slice
into its assignment without copying worker control credentials. Omission is
intentional and leaves source selection to the operating system through the
ordinary `net.Dialer`; wkbench does not guess an OS source-port capacity.

Reviewed scenario objectives declare a stable `small`, `medium`, or `large`
scale, ingress and online-fanout QPS, tolerance, and active-channel window.
`IdentityConfig.TotalUsers` is the full online-plus-offline pool; zero is the
legacy compatibility fallback to `OnlineConfig.TotalUsers`. The plan therefore
records both `IdentityPool` and `OnlineIdentityPool`. A worker's optional
`OnlineIdentityIndexes` is mutable runtime mapping for scheduled identity churn
and is omitted from the initial deterministic plan.

`OnlineConfig.Churn` is explicit about interval, churn ratio, same-user share,
identity-swap share, and history-sync policy. Long stability profiles require
the shares to sum to one, at least one full offline identity lane for swaps,
and `history_sync: false`. Bench capabilities separately advertise batched
subscriber adds and removals so identity-swap workers can keep group membership
aligned without growing long-run group cardinality.

`ShardConfig.HashSlotSpread` is a group-profile contract: `HashSlotCount` must
be positive and equal the profile channel count. It means channel index `n`
must be generated into physical hash slot `n`, not merely distributed by the
ordinary worker shard strategy.
