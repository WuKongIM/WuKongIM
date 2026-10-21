# Smallest deterministic feedback plan (proposed, not implemented)

Do not relax the zero-loss gate, increase the deadline, retry the failed cloud run to obtain green, or treat any eventual RECV as timely delivery.

## First, retain the already emitted bounded evidence

The attached pure-data probe validates the exact terminal ZIP SHA/CRC and demands exactly one bounded `worker-evidence.json` alongside a final report with one expired correlation. It is RED on the retained ZIP because that document is absent. This is a concrete evidence-collection contract gap; it does not prove the runtime failed to write the document. Root should review the exact collector separately.

Before modifying that collection path, write all failure modes: valid three-worker terminal document preserved byte-for-byte; missing/oversize/malformed document recorded as evidence unavailable; wrong run/generation rejected; duplicate members, path traversal and symlink rejected; no raw IDs, tokens, payloads or arbitrary errors exported; one expired correlation's bounded example survives collection; no change to final verdict. Use an offline fixture/stub collector and its existing integration seam, with a repeatable bounded output artifact. Do not fetch new cloud evidence for this test.

## Then, isolate one message with explicit clock and ordered barriers

Use existing `Verifier`, `SessionPool`, Engine fake-client and fake-clock seams. Freeze a single valid sampled logical message, one sender and one distinct leased online recipient, the actual 10-second deadline, and matching ACK/RECV server identity. No sleeps, random timing or 10,000-user reproduction is needed. Any test that starts processes or real waits must use the integration tier. Write the full matrix before adding any code:

| Schedule | Fixed expected fact / counterexample |
| --- | --- |
| No positive ACK, arbitrary clock advance | No delivery loss; prevents registering time from becoming acceptance time. |
| Positive ACK at T; process valid RECV at T+10s−1ns | Delivered once, no loss. |
| Process valid RECV before ACK | Delivered once after ACK, no loss. |
| Duplicate positive ACK at T+9s | Original deadline remains T+10s. |
| Positive ACK; no RECV by T+10s | Exactly one expired loss, one matching bounded fingerprint and logical index. This failure must remain fatal. |
| Queue a valid RECV at T+9s, deliberately hold its session drain; advance Engine to T+10s; release drain at T+11s | Establish whether local queueing alone yields the same loss. Current source predicts expired=1 and no reversal. This is a mechanism probe, **not** evidence that historical cee5 did this. |
| Actually deliver RECV only at T+11s | Distinguish late ingress from timely ingress followed by late processing; do not merge these cases into a pass. |
| Same deadline, explicit barriers select RECV-before-expiry versus expiry-before-RECV lock order | Characterize the precise boundary without nondeterministic races. |
| Session TTL before T+10s while correlation is active | Recipient stays pinned; closing it must not become a hidden successful sample. |
| Wrong payload/server identity, timeout/cancel/read error | Existing explicit classification remains observable; no widened error acceptance. |

The currently missing observation is the distinction between RECV ingress and verifier processing. A red test should first require a bounded, typed per-sample diagnostic timeline for the two queue schedules, rather than assert a new pass outcome. Proposed minimal facts: worker/generation, existing logical index and redacted fingerprint, configured deadline, accepted-at, expired-at, whether receive was observed, and receive-ingress/verifier timestamps when genuinely available. Absent values remain absent, not zero. First/last retention and fixed memory limits must remain bounded; do not add raw UID/channel/payload, arbitrary transport errors, or unbounded per-message history.

Only after the ordered probe demonstrates the mechanism and the diagnostic contract is agreed should a behavioral change be proposed. Moving ACK bookkeeping earlier, ignoring expiration during drain, or accepting an eventual RECV would not independently establish correct delivery timing. Historical root-cause attribution remains unknown without this sample's identity and timeline.
