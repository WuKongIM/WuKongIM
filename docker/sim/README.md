# Docker Compose Dev Simulator

`wk-sim` is an optional local development simulator. It runs `wkbench dev-sim`,
keeps a deterministic user set online, and sends person/group messages through
the public bench API and WKProto gateways.

`cloud-small.yaml` is a separate deterministic `wkbench/v1` Phase 1 analysis
fixture. It is mounted into `wk-analysis` so `run_inspect` can return the exact
effective scenario, canonical digest, non-zero seed, and 256 hash-slot identity.
It is not executed by the `dev-sim` supervisor.

Start the three-node development cluster with the simulator profile:

```bash
docker compose --profile dev-sim up -d --build
```

Check simulator status:

```bash
curl http://127.0.0.1:19091/healthz
curl http://127.0.0.1:19091/status
docker compose logs -f wk-sim
```

The Compose profile defaults to a higher local-debug workload:

| Setting | Compose default |
| --- | --- |
| Online users | `1000` |
| Person channels | `500` |
| Group channels | `500` |
| Group members | `10` |
| Per-channel send rate | `0.25/s` |
| Traffic concurrency | `128` |
| Receive verification | `none` |
| UID prefix | `devsim-u` |

With 500 person channels and 500 group channels, `0.25/s` per channel targets
roughly `250` ingress messages per second. Group fanout means delivered message
volume can be higher than ingress volume.

Receive verification is disabled by default for this high-throughput profile so
the simulator keeps producing ingress traffic even when local delivery lags.
Set `WK_SIM_VERIFY_RECV=sampled` when you specifically want sampled receive
checks instead of maximum send pressure.

`WK_SIM_TRAFFIC_CONCURRENCY` bounds concurrent send+sendack operations per
traffic stream. Increase it when local RTT caps send pressure below the target
rate, or lower it when debugging on a small laptop.

The default Compose node configs keep `WK_DEBUG_API_ENABLE=true` for `/debug`
routes, `WK_METRICS_ENABLE=true` for Prometheus/Grafana and manager dashboard
charts, and `WK_DIAGNOSTICS_ENABLE=true` for diagnostics collection. Disable
debug API, metrics, or diagnostics in `docker/conf/node*.conf` when you want to
remove that overhead during a pure hot-path run.

The development cluster config uses a `5s` data-plane RPC timeout so local
leader forwarding has enough headroom during this high-debug workload.
It also sets an explicit data-plane pool size of `8` with fetch/pending limits
of `16` so the high-traffic simulator profile is not bottlenecked by the
general cluster control-plane pool size.

The simulator also sends heartbeat pings every `30s` so generated users that are
not currently sending traffic still remain online during long debugging runs.

Run the local Compose smoke check after changing simulator or cluster startup
behavior:

```bash
scripts/dev-sim-compose-smoke.sh
```

The script starts `wk-node1`, `wk-node2`, `wk-node3`, and `wk-sim` with the
`dev-sim` profile, retries transient `docker compose up --build` failures,
waits for `/status` to report running traffic, and checks recent logs for panic
markers. Use `--no-up` to check an already running stack only.

Run the opt-in e2e smoke when you want to validate the simulator without using
the local Compose volumes:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/bench/devsim_smoke -count=1
```

Lower the workload for smaller laptops without editing `dev-sim.yaml`:

```bash
WK_SIM_USERS=40 \
WK_SIM_PERSON_CHANNELS=10 \
WK_SIM_GROUP_CHANNELS=3 \
WK_SIM_GROUP_MEMBERS=12 \
WK_SIM_RATE=0.5/s \
WK_SIM_TRAFFIC_CONCURRENCY=16 \
WK_SIM_VERIFY_RECV=sampled \
WK_SIM_UID_PREFIX=dev-u \
  docker compose --profile dev-sim up -d wk-sim
```

Plain `docker compose up -d` does not start `wk-sim`; the `dev-sim` profile must be enabled explicitly.
