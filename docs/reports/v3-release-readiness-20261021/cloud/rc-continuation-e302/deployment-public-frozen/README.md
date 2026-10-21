# Deployment green, pre-clock failed

Deployment39607092610 completed success; all artifact bytes/API SHA/ZIP CRC and Lease/source/control/bundle links verified. Four hosts,256 healthy leaders/replica sets,3 workers and7/7 Prometheus targets at13:26:03Z. No encrypted material was decrypted.

Issue1018 comment6317250473 at13:26:58Z reports rehearsal_deployment_repair_pending, failure_code=stage_state_parse_failed, last_gate=deployment_receipt. This is a real pre-clock parsing failure, not a workload start or measured result. The original initial failure remains unknown; current cause also awaits typed preclock diagnostic bytes. The existing top retains exact Lease until its bounded repair window/stop/cleanup. No repair trailer, code change, retry or new procurement was performed locally.
