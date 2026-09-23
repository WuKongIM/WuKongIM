# MQTT replay recovery after a stopped Channel leader

Native failover must establish a surviving leader before waiting for shared replay
coverage. The new leader remains write-fenced while its committed anchor requires
missing content. It can plan recovery under that fence, copy from a surviving
independent donor, then complete the durable migration task and reopen admission.

## Failure inventory before implementation

1. A dead source is probed/drained, or absent shared content blocks native leader
   selection, leaving recovery without a coordinator.
2. Native catch-up clears the fence despite missing shared content, or waiting
   makes the task terminal/unrecoverable.
3. Recovery still routes to the stopped old leader, or accepts local copies as
   committed proof without the surviving donor's anchored content.
4. Successful content repair fails to resume the exact durable task, loses the
   original message, or never restores ordinary append admission.

The integration uses real TCP, disks, Slot commands and native quorum replication
with 256 hash Slots. It creates a failover task with an explicitly selected target
so that the missing-content case is deterministic. Health scanner candidate
selection is not claimed by this test. No product MQTT listener is enabled.

## Frozen context

Source `63909fb73`; SHA-256 follows.

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`

- `pkg/cluster/FLOW.md`: `22de8ab0d1d0591c4c4898bf59893aaccea4afb215891372d82684cf627dcec6`

- `pkg/channel/FLOW.md`: `625cca5058c446f910a62218a18ec68abc3b400c86fbe518e72a93fe6a6a5233`

## Discovered stale-route regression

The stopped-node run completed failover, repaired replay and cleared the fence,
but ordinary Append still dialed the stopped node. Its cached leader was only
invalidated for Channel semantic errors, not a typed transport dial failure.
A failed dial admits no request on that connection; both single and batch append
may perform their existing one fresh-authority retry for this exact error. Keep
ambiguous post-send failures outside that retry classification, and preserve the
existing exact-version invalidation so concurrent newer authority cannot regress.

The first setup incorrectly required full new-channel placement readiness after
stopping one of three nodes. Existing Channel/Slot quorum recovery is validated
through fresh metadata instead. Diagnostic polling also uses a joined sequential
loop so a timed-out assertion goroutine cannot race its final error report.
