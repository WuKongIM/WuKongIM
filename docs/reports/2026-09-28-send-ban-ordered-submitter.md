# Bounded ordered submission module: integration still pending

The proposal trace localized completion delay to occupied replica flights and
coordinator-result waits. Removing the gateway batch join by launching unowned
goroutines would break session preparation order, remote Channel order and
capacity accounting. The first implementation step is `OrderedSubmitter` in
`internal/runtime/channelappend`, an optional node-owned admission/completion
module over the existing routed batch sender.

No composition root or entrypoint calls it yet. Production SEND behavior is
unchanged, the EOF is not fixed, and R2/R6 remain incomplete. There is no new
configuration or Changelog entry because the module is currently inactive.

## Implemented contract

- Submit atomically accepts or rejects a batch. A successful admission owns one
  callback and copied descriptors/recipient slices; payloads remain immutable
  borrowed bytes. Record and payload budgets include executing and callback-owned
  work, not just waiting work.
- Canonical Channel dependencies use the same pre-route codec as Router, including
  normalized person pairs, Channel type and configured command suffix. Overlapping
  batches execute in FIFO order, including multi-key predecessor chains; independent
  batches may run concurrently. Malformed commands retain Router's aligned policy.
- Linked dependencies occupy O(admitted distinct keys), are removed at completion,
  and never require a scan of all queued jobs or a historical per-Channel cache.
  Worker count is fixed. Admission canonicalization is serialized under the owner
  mutex; its contention and preparation allocation cost still need original-load
  measurement before promotion.
- Close fences admission and joins accepted delegated calls and callbacks. Timeout
  stops only the caller's wait. It does not replace Group's drain for durable writes
  that outlive a canceled routing result, or prove delivery/ACK receipt.
- Existing Channel append pool ownership reports busy batches, waiting records,
  capacity and monotonic rejection totals. SafeGo accounts for direct workers once;
  pool retirement preserves rejection history. Critical panic policy is unchanged.

## Validation

Failure contracts and integration tests preceded implementation. Initial RED was
the absent interface, not a claimed reproduction of the original EOF. Eight
integration scenarios cover independent progress, overlapping FIFO, active/callback
capacity, normalized keys/duplicate keys, copied descriptors, worker bounds,
queued cancellation, atomic rejection, command aliases, repeatable drain and pool
ownership. Table subcases cover separate record and payload exhaustion.

The first ownership review exposed missing pool accounting; its integration
regression failed with zero capacity before registration was added. Final targeted
integration tests passed20 repetitions, race checks passed5 repetitions, and the
complete default unit suites for channelappend and goroutine passed. These are
module guarantees, not gateway or cluster E2E evidence.

`flow-doc-contracts` passed after removing a sixth Read First reference and
regenerating the index. The named command comes from the policy catalog. A host
toolchain selection failure and the earlier FLOW failures remain archived. Final
FLOW validation used cached Go1.25.11. The108-line channelappend guide exceeds
the100-line target by eight lines; the documented reason is to retain both existing
durability/retry rules and the distinct asynchronous ownership/drain contract.
No validator or rule was weakened.

## Required continuation

The message usecase must finish permission, directory and hook preparation in
session order before transferring work. Asynchronous results must not race its
existing aligned state or cause item deadlines to be canceled when preparation
returns. Hook identity rewrites still require fresh authorization.

Gateway must keep its existing overall/shard reservation across queued, executing
and completed-but-unpublished records, publish ACKs in session order across batches,
and include all of that work in DrainSends. A node-owned queue is not permission
to multiply the accepted-work budget. Frame ownership, error paths, terminal seals
and constructor rollback also need entry/composition integration coverage.

Only then run the unchanged5000/4500/cap32 failure loop, followed by uninstrumented
R2 and three fresh complete R6 comparison pairs. Do not report this unused module
as a repair or reuse diagnostic passes as qualification.

Artifacts: `assets/send-ban-ordered-submitter-20260928/manifest.json`.
