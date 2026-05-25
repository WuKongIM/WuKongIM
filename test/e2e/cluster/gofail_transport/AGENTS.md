# gofail_transport AGENTS

This scenario validates the opt-in gofail-enabled `cmd/wukongim` binary surface.

## Purpose

- Start a real single-node cluster with a prebuilt gofail-enabled binary.
- Enable `GOFAIL_HTTP` through per-node e2e environment injection.
- Verify the transport failpoints are registered and externally controllable.

## Run

Build the failpoint binary first:

```sh
scripts/build-gofail-binary.sh --out /tmp/wukongim-gofail
```

Then run:

```sh
WK_E2E_BINARY=/tmp/wukongim-gofail WK_E2E_GOFAIL_TRANSPORT_SMOKE=1 go test -tags=e2e ./test/e2e/cluster/gofail_transport -count=1
```

## Rules

- Keep this opt-in; normal e2e runs must skip it unless `WK_E2E_GOFAIL_TRANSPORT_SMOKE=1` is set.
- Do not assert internal state or import `internal/app`.
- Use the gofail HTTP endpoint only on loopback addresses allocated by the test.
