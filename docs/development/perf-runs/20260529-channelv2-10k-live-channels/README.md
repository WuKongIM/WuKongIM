# ChannelV2 10k Live Channels Perf Run

## Scenario

- workload: three-node ChannelV2, 10,000 group channels, every channel warmed once, sparse steady traffic over 1%-5% active channels
- clean or accumulated: clean stack unless explicitly recorded otherwise
- duration: 15s warmup, 60s measured window, 10s cooldown
- success criteria:
  - sendack_error_rate = 0
  - recv_verify_error_rate = 0 when receive verification is enabled
  - idle-window ChannelV2 pull rate is near zero except recovery probes
  - append p99 and store p99 recorded for comparison

## Required Evidence

- git revision: `git rev-parse HEAD`
- artifact storage: this perf-run directory is ignored by default; keep raw evidence local unless a sanitized artifact should be force-added intentionally
- scenario config: copy wkbench scenario YAML into this directory
- target config: copy target YAML into this directory
- worker config: copy workers YAML into this directory
- metrics:
  - `wukongim_channelv2_active_runtimes`
  - `wukongim_channelv2_follower_parked`
  - `wukongim_channelv2_pull_total`
  - `wukongim_channelv2_recovery_probe_total`
  - `wukongim_channelv2_meta_cache_total`
  - `wukongim_channelv2_append_duration_seconds`
  - `wukongim_channelv2_worker_task_duration_seconds`
  - storage commit histograms
- logs:
  - app.log
  - warn.log
  - error.log
- pprof:
  - CPU profile during measured window
  - heap profile after warmup

## Perf-Triage Rule

Follow `docs/development/PERF_TRIAGE.md` and `.codex/skills/wukongim-perf-triage/SKILL.md`: establish smoke health, collect evidence, classify, form one hypothesis, and change exactly one variable per experiment.
