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
