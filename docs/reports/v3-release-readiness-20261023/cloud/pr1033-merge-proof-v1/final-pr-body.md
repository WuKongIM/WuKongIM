Cold presence recovery admits only four authority rounds and four owner-read rounds at once. In the full-population local reproduction, accepted durable messages expired during presence planning; both expired samples were matched to their three database replicas and exact presence-timeout warnings.

Admit up to 16 independent rounds and 8,192 distinct in-flight keys. Keep owned UID clones explicitly bounded to 128 MiB, with the existing 32 MiB subset, page limits, deadlines, retries and authority proofs. The changes include cancellation/reuse and independent byte-limit regressions plus the corresponding FLOW/index and operator notes.

Validation:

- Frozen exported-entry and adapter fixtures fail on the old fifth admission, then pass with sixteen active rounds and a waiting/cancelable seventeenth. The real byte test fills 128 MiB while cohort capacity remains free and blocks the next byte.
- Both affected packages pass normal/race checks and related integration normal/race regressions. Formatting passes; the FLOW check passes after regenerating its initially stale index, with the original failure retained.
- One unprofiled A–B–A sequence uses the same full 10,000-online / 2,000-group / 500-SEND/s configuration, fresh data, service GOMAXPROCS=2, and unchanged failure gates:

| Arm | Recovery width | Final elapsed | SEND = ACK | Sampled expiries | Original outcome |
| --- | ---: | ---: | ---: | ---: | --- |
| A | 4 | 33.076 s | 13,002 | 2 | product_failure/message_loss |
| B | 16 | 122.978 s | 60,503 | 0 | operator_stop/operator_requested after the 120 s target |
| A2 | 4 | 38.530 s | 18,002 | 1 | product_failure/message_loss |

B completed all 664 sampled correlations with no pending work. All three runs ended with independently verified process/port cleanup. This supports the bounded-width hypothesis in this local reproduction; it is not a Linux performance or long-soak acceptance result, nor proof of the original cloud inner cause. First-attempt Linux CI passed all four workflows: 219 default packages, the complete scripts suite, all 55 migration history checks, messaging E2E, and four-distribution test-package/systemd exercises. The fixed Linux AMD64 performance matrix completed 270,000 measured requests with zero errors/drops and P99 maxima of 178.024 ms (TCP), 33.470 ms (append), and 20.693 ms (mixed), all within the unchanged 400 ms gate.

Dedicated Linux ARM64 normal integration additionally passed all seven expected test/subtest outcomes using the exact-source static test binaries. The first local legacy-image build timed out before tests started; that failure remains preserved. A separate, bounded empty-rootfs import then ran the unchanged tests in offline, non-root, resource-limited containers and removed its exact containers/image. Linux race is unverified; Mac race coverage remains separately recorded.

Related to #1025.
