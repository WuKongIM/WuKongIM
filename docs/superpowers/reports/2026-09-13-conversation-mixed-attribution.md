# Conversation mixed-load attribution

## Fixed baseline

Run: https://github.com/WuKongIM/WuKongIM/actions/runs/34735830445
Artifact: `conversation-mixed-34735830445-1`, retained for 90 days.

- Product: `5b32362b712fdc94ab7d30241ac2d7f39892f469` (failed beta.15).
- Binary SHA-256: `8ade6a26623be962c443a6b88add826a64a31efd0b8cd3eb8b3a4b57c7c57438`, identical to the failed publisher gate.
- Harness: `c944a766135a06d7e4fc1152e839f6d09e062f7a`, clean.
- Profile SHA-256: `1d0fb74bf51a6e747b56d1b43fece4670b40143a69a1601cc2f4be6f784a37a1`.
- Ubuntu 24.04, Go 1.25.11, AMD EPYC 9V74, four logical CPUs (two cores, two threads/core).
- Three nodes, node GOMAXPROCS=2, driver GOMAXPROCS=4, 256 hash Slots.
- Unchanged fixture, HTTP connection bounds, list 200/s + sync 60/s, eight workers per endpoint, three fixed 60-second windows. No retries or profiling during measurement.

| Window | List QPS | List P99 ms | List drops | Sync QPS | Sync P99 ms | Node CPU seconds total | Host idle |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 199.883 | 78.123 | 0 | 59.950 | 109.228 | 174.44 | 12.0% |
| 2 | 195.483 | 800.473 | 175 | 59.950 | 143.131 | 175.64 | 10.3% |
| 3 | 197.317 | 576.943 | 64 | 59.933 | 131.398 | 181.70 | 9.3% |

All HTTP responses were correct, with zero HTTP errors, runtime loads/residency
or membership writes. The latter two list windows failed. Driver-wait P99 was
743.116/533.992 ms versus request P99 73.341/72.588 ms. Driver CPU was about
40.4–40.8 seconds per window (Linux process ticks). No CPU steal or cgroup
throttling was observed. These counters do not prove constant physical CPU
performance or rule out every infrastructure influence. The original failure
is reproduced, not attributable solely to an overloaded driver.

Separate ten-second profiling shows storage lookups, allocations and RPC work.
Node 1 sampled 29.46% cumulative CPU under `readStoredConversationHead`, 14.58%
under allocation, with runtime metadata reads also significant. Cumulative
percentages overlap and must not be added. Its allocation delta attributes
133.37 MiB directly to `readConversationHeads` (4.95% of sampled allocated bytes).
The driver spends substantial CPU validating complete JSON responses; validation
must remain intact. Profiling windows are not capacity evidence.

## First candidate: request metadata ownership

Replace embedded full `ch.Meta` values in head/message read descriptors with
references to the call's own aligned, immutable resolved metadata. Origin and
serving nodes still resolve authority independently; pointers never cross the
codec. Missing local metadata still rejects committed cold recovery. Reads,
worker/admission/queue limits, gates, wire formats and metadata freshness do not
change. No cache or new background owner is introduced.

A focused 100-channel remote routing plus encode/decode benchmark on Apple M4
measured 282,595 → 152,898 bytes/op, with 331 allocations/op unchanged, and
44.8 → 31.7 microseconds/op over three samples. This isolates descriptor cost;
it does not establish an AMD64 endpoint throughput gain. Full fixed-load
candidate validation remains pending until recorded below.

## First candidate result: insufficient

Run: https://github.com/WuKongIM/WuKongIM/actions/runs/34736402194
Product/harness: `257ddad0e4a52f5db04e51276af80de105fdd5b2`.
Binary: `2b3bc592ac0b0be528caac68ab8e04bbf250efa9568dd3aad741d7b3cf366f5a`.
Same CPU model, four logical CPUs, Go version and profile.

| Window | List P99 ms | List drops | Sync P99 ms | Node CPU seconds | Allocated GB |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 54.780 | 0 | 93.602 | 168.95 | 45.60 |
| 2 | 706.033 | 106 | 132.579 | 168.06 | 45.39 |
| 3 | 52.745 | 0 | 90.366 | 168.74 | 45.60 |

No HTTP errors, runtime activation or membership writes. The second window
still failed; this candidate is not a verified repair and cannot be released.

## Second candidate: synchronous borrowed decoding

The original allocation profile also identified `rowcodec.Unwrap` and
`Scanner.readValue` as intermediate copies before owned field extraction.
Add explicit borrowed variants for the synchronous message/runtime-metadata
decoders. Existing copying APIs retain their contracts. Borrowed decode still
checks envelope CRC and malformed columns, and String/Bytes accessors return
owned results. Tests recycle source buffers after decoding to protect ownership.

The same M4 256-byte message header/payload microbenchmark improves from
1,768 bytes and 20 allocations to 552 bytes and 10 allocations, with
approximately 542 → 339 ns/op. AMD64 validation is recorded below.

Related unit and focused race tests passed for the first candidate. Three-node
conversation-directory E2E passed. Legacy-sync E2E first failed before reads
with `ReasonAuthFail`: its tokenless fixture had not opted out of default Token
authentication. Explicit per-fixture-node overrides fixed that precondition,
and both topology flows then passed in 63.251 seconds. Product authentication
defaults were not changed.

## Second candidate result: all fixed gates passed

Run: https://github.com/WuKongIM/WuKongIM/actions/runs/34736979830
Product/harness: `c9e4c0ebf0d2f339434f4cb991f1937807471b07`.
Binary: `f3f73da3e3fbb2fd0089e9e7b1fb2a1e629aa9d3410cd3aefe70cb720e209409`.
Same CPU model, four logical CPUs, Go version and profile; hosted machines
are separate allocations, not one controlled physical machine.

| Window | List P99 ms | Sync P99 ms | Node CPU seconds | Allocated GB | Host idle |
| --- | ---: | ---: | ---: | ---: | ---: |
| 1 | 30.199 | 52.363 | 138.99 | 43.42 | 29.71% |
| 2 | 28.724 | 51.966 | 139.04 | 43.42 | 30.03% |
| 3 | 30.269 | 53.576 | 140.49 | 43.42 | 29.50% |

Each window delivered list 199.967/s and sync 59.983/s, with zero errors,
drops, runtime activation or membership writes. No CPU steal or cgroup
throttling was observed. The unchanged complete v2 matrix then passed all
20 endpoint results: 12 topology/size cases, both mixed endpoints, and six
hidden-conversation pages. Its mixed P99 was list 30.282 ms / sync 52.497 ms.
The 99%-hidden gap cases reached 333.729 / 335.516 ms and passed their
unchanged limits. This diagnostic evidence does not replace the publishers'
mandatory exact-release-tag gate.

The same candidate passed the PR three-node regression, Manager browser smoke,
iOS and Flutter SDK acceptance. Android acceptance failed during fixture
startup with HTTP 503, before the Android client ran. The original harness did
not retain server logs at that stage, so its exact failing operation and cause
cannot be established. One local reproduction succeeded. The harness now
requires three consecutive readiness samples within a bounded poll budget and
prints bounded logs on startup failure; it does not retry fixture mutations or
change product readiness. Integration tests cover flapping, deadlines, process
exit and retained diagnostics. Subsequent CI must validate the revised harness.
