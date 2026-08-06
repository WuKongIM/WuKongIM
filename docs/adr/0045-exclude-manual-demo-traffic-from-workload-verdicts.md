---
status: accepted
---

# Exclude manual Demo traffic from workload verdicts

Manager and Demo remain usable during a Chat Lifecycle Run, but only run-marked workload payloads enter correctness, retry, latency, and throughput denominators. Durable metadata reconciliation therefore requires actual creates to cover all expected marked creates and reports any excess as external_demo_activity instead of failing exact equality; missing expected creates and all marked-traffic correctness errors still fail. Demo resource impact remains in host metrics because it cannot be subtracted reliably.
