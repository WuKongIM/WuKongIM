# channel_activation_profile AGENTS

This scenario is for agents working on `test/legacy/e2e/message/channel_activation_profile`.

## Purpose

Profile the process-level cost of activating 100 group channels per wave through
real HTTP API setup, real WKProto sends, and the public debug pprof endpoint.

## Run

```bash
WK_E2E_CHANNEL_ACTIVATION_PROFILE=1 go test -tags=e2e,legacy_e2e ./test/legacy/e2e/message/channel_activation_profile -count=1
```

Optional artifact directory override:

```bash
WK_E2E_CHANNEL_ACTIVATION_PROFILE=1 \
WK_E2E_CHANNEL_ACTIVATION_PROFILE_DIR=tmp/profiles/custom-channel-activation \
WK_E2E_CHANNEL_ACTIVATION_PROFILE_ROUNDS=8 \
WK_E2E_CHANNEL_ACTIVATION_PROFILE_CPU_SECONDS=5 \
go test -tags=e2e,legacy_e2e ./test/legacy/e2e/message/channel_activation_profile -count=1
```

## Profile Knobs

- `WK_E2E_CHANNEL_ACTIVATION_PROFILE_CHANNELS`: concurrent channels per wave
  (default `100`).
- `WK_E2E_CHANNEL_ACTIVATION_PROFILE_ROUNDS`: number of activation waves
  (default `5`).
- `WK_E2E_CHANNEL_ACTIVATION_PROFILE_CPU_SECONDS`: CPU profile duration
  (default `3`).
- `WK_E2E_CHANNEL_ACTIVATION_PROFILE_TIMEOUT`: overall test timeout
  (default `120s`).

## Core Steps

1. Start a fresh single-node cluster with `WK_DEBUG_API_ENABLE=true` so
   `/debug/pprof/*` is exposed on the API listener.
2. Create `WK_E2E_CHANNEL_ACTIVATION_PROFILE_CHANNELS` group channels per
   wave through `/channel`, each with its own sender as a subscriber.
3. Connect one WKProto sender client per channel, per wave.
4. Start a CPU profile request against `/debug/pprof/profile` and release all
   senders together so each wave activates all of its channels concurrently.
5. Require every send to return a successful `SendAck` and verify manager
   channel runtime metadata reports each channel as active.
6. Save CPU and heap profiles, then run `go tool pprof -top` into text
   artifacts for bottleneck inspection.

## Failure Diagnostics

- Test logs include the profile paths, per-wave elapsed time, total activation
  time, p50/p95/max send-ack latency, and a bounded preview of the CPU and
  heap `pprof -top` output.
- On send or metadata failures, include the cluster diagnostics dump from
  `test/legacy/e2e/suite`.
