# Spec review

Reviewed initial through delivered frozen scopes against
`docs/specs/mqtt-will-pressure-races.md`, base
`a8559a6bad3f48337315f814dc664b3c221e76b6`. No tests or source changes.

## Initial findings and dispositions

1. **[P1] Incomplete delayed-CAS acceptance:** initial spec lines 52–53 required
   “three distinct original business publications and 15s healthy quiet”
   (now lines 57–58). The initial matrix passed five cases; single-node
   `late-release` repeatedly timed out after passing expired-page retention.
   Joined isolated/repeated cases passed, but another complete 35s-window
   matrix still passed only five cases. Resolved within the final bounded
   slice: all six 60s-window cases passed in 638.060s, each preserving the
   current original, three publications and healthy quiet with zero extras.
   A 41.631s receipt proves the earlier 35s gate insufficient; the precise
   latency cause and any product-repair claim remain unqualified.

2. **[P2] Uncovered early-read starvation:** line 17 names “Reclamation pages
   starve behind early failed reads or become unbounded.” Final spec lines
   64–65 explicitly exclude failed authority-read starvation from this slice.
   Disposition: accepted scope clarification, retaining open qualification;
   no implemented fairness claim.

3. **[P2] Full-capacity client joining:** line 21 inventories “Closing an owned
   client leaves Paho workers alive when the receipt is written.” Resolved:
   both fixtures now use bounded `AbortAndWait`; Paho closes `Done` after
   workers join. Exact-source full-capacity reruns passed 749.098s/467.413s,
   and joined cap-two regression passed both topologies, 144.938s.

## Final qualification and remaining limits

No remaining actionable implementation findings in the approved slice.
Spec lines 54–56 define “eventual completion acceptance, not a latency SLO.”
The 60s observation preserves the 750ms page assertion, 10s grant,
unknown-current ordering, identities, original subscription and healthy quiet.
The delivered fixture only extracts failure-path stack capture into the
shared suite; unchanged acceptance controls plus a 105.977s delivered-source
late-release rerun support the matrix result. Source hashes distinguish both.

Public failure-only history lookup is limited to eight messages, with counts
only in JSON. Optional private stack capture is capped at 1MiB; its real-process
probe saved 946,209 bytes. Neither diagnostic replaces a failed receipt.
Counters use `return(false)` and preserve branch behavior. No unasked production
behavior, scope creep or incorrect implemented requirement found.

Four unsafe cancellation-ignoring controls failed the intended second-page
assertion. Earlier failed runs remain separate. Failed-read starvation,
uncommitted Raft apply, partitions, corrupt/lost journals and complete
issued-effect terminal recovery remain open; this is not complete MQTT
or latency qualification.
