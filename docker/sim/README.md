# Docker Compose Dev Simulator

`wk-sim` is an optional local development simulator. It runs `wkbench dev-sim`, keeps a small deterministic user set online, and sends low-rate person/group messages through the public bench API and WKProto gateways.

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

Tune the safe defaults without editing `dev-sim.yaml`:

```bash
WK_SIM_USERS=40 \
WK_SIM_PERSON_CHANNELS=10 \
WK_SIM_GROUP_CHANNELS=3 \
WK_SIM_GROUP_MEMBERS=12 \
WK_SIM_RATE=0.5/s \
WK_SIM_UID_PREFIX=dev-u \
  docker compose --profile dev-sim up -d wk-sim
```

Plain `docker compose up -d` does not start `wk-sim`; the `dev-sim` profile must be enabled explicitly.
