# Run34 first runtime failure — read-only observation

The exact request `chat-20261021T163850Z-4ccd7506` entered `rehearsal_diagnosis_pending` at2026-10-21T17:20:19Z. Scheduled finalizer39642565187 (attempt1, source/maincee5d51f325fb84033060f546337d326110adae5) completed successfully as an evidence/safety operation. **The workload failed; finalizer success is not rehearsal pass.** Actual job checkout confirms the same commit.

The complete855906-byte artifact12365508721 was downloaded and its API SHA256 and ZIP CRC independently checked. `final.json`/`rehearsal-result.json` report `product_failure / message_loss`, after61.212052848seconds (17:05:48.909224454→17:06:50.121277292UTC). Sampled correlation1339, delivered1338, expired1; losses1. All122002 SEND attempts received SENDACK, first-attempt failures0, retries0. Sync completed10000 with0failures. Harness failures/underdelivery/drain-timeout are0/0/false. These are exact report fields, not an independently established root cause.

The original handoff/run-plan/deployment/receipt/quote bytes match the already frozen stage. Final fence and time window match the actual run-start. Budget remains H1351044800/S1201044800/prior0. Reported conservative aggregate cost20939800micros matches the diagnosis marker; it is not provider billing. No release/zero-inventory proof exists in this observation.

Diagnosis window: stable failure17:06:50.121277292UTC; **deadline19:06:50UTC (03:06:50Beijing)**. `analysis_required=true`. Release steps were skipped while diagnosis is pending. The safety workflow remained active at17:22:40Z. No operator Stop, SSH, provider request, dispatch, workflow change or new Lease was performed by this observer.

Terminal evidence is explicitly **incomplete**:1capture failure, metrics/range oversized4194305bytes above4194304limit. Logs for4hosts, heap/goroutine profiles for3services, system snapshots and target metadata were collected. All original logs/profiles remain in the verified private ZIP at`rc-new-cee5-private/remote-progress/final-12365508721.zip`; they were not copied into this public allowlist. Source diagnosis, live investigation and cleanup decisions belong to root.

Last snapshot is remote-progress-observation/poll-01 (17:22:40Z); finalizer checkout read17:24:01Z. Prior archives are unchanged. Public payloads are exact selected JSON/Markdown and bounded metadata; raw ZIP, raw GitHub logs/stderr, SSH config and known_hosts are excluded.
