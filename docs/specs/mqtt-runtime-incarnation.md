# Runtime deletion incarnation fence

Reliable MQTT continuation requires stale prepared writes to fail after physical
runtime metadata deletion and recreation. The existing route fence can reset
when its whole row is removed; a fresh Slot read alone does not prevent this ABA.

## Failure inventory before implementation

1. Direct or Slot-batched runtime deletion erases all authority generations;
   recreating the same identity makes an old exact append request valid again.
2. A delayed monotonic upsert resurrects a deleted runtime. After deletion only
   an explicit create may reopen it; an upsert must fail while the row is absent.
3. Create/delete/create within one committed batch misses its own deletion,
   resets a floor or leaks staged state after an atomic batch failure.
4. A duplicate delete increments or removes the witness; a create loser changes
   an existing runtime; a second deletion forgets newer live generations.
5. Snapshot, portable binary backup or reopen loses the fence; keys for another
   channel, type or hash Slot incorrectly share an allocation floor.
6. Corrupt, key-swapped or unknown-version witnesses are interpreted as absent;
   maximum generations wrap and permit an old authority to return.
7. A successful create returns its original candidate rather than the actual
   authority assigned during Slot apply. The creation coalescer must reread the
   aligned committed rows even when every insertion succeeds.
8. Recreated person-directory metadata permits old admission/projection progress
   to resume, or unchanged ordinary channels pay an unbounded scan cost.
9. A retained old person-directory task conflicts with recreation, or an old Ready
   marker skips the new projection. Runtime deletion must atomically remove the
   old task and make an existing directory Pending; stale completion cannot undo it.
10. A delayed directory admission supplies an old generation while the retired
    runtime is absent. It must not recreate the task or restore Ready state.

## Durable boundary

Table 3 (`channel_runtime_meta`) System 1 retains one key-bound version-1 fixed
uint64 high water per deleted `(channel_id, channel_type)`, owned by its hash
Slot. The high water is the maximum of the deleted runtime's channel/leader/
route/directory/write-fence generations and any previous witness. Deletion and
the witness commit atomically. Existing MQTT inbox invalidation remains atomic.
For a person channel, the same batch removes the old directory task and marks an
existing business Channel Pending, retaining its business flags/membership. Cache
publication happens only after commit. Stale task completion cannot restore Ready,
and even explicit-generation directory admission rejects an absent retired runtime.
Recreation admits a task against the newly allocated directory generation.

Explicit recreation initializes channel, leader, route and write-fence versions
above that floor (and directory generation for person channels), preserving any
higher candidate version. New identities without a witness retain existing
normalization. Ordinary updates do not read a witness. A maximum floor prevents
recreation instead of wrapping. No wall clock or caller-supplied absence proof
allocates a new incarnation.

Creation results remain on the existing wire format: `Created` proves insertion,
not that the candidate's versions were stored verbatim. The bounded creation
coalescer performs one aligned authoritative reread per batch, including wholly
successful batches. This adds a read to cold creation, not the message hot path;
the correctness requirement supersedes the previous candidate-return shortcut.

All writers must match before relying on this semantic fence. Existing deletion
history without a retained witness cannot be reconstructed. Full live restore
activation and old-owner isolation, retained Will receipt transfer/lifecycle,
MQTT JSONL transfer and uncertain Will dispatch remain separate product gates.

The witness adds one bounded point read/write to deletion and one point read to
creation; ordinary updates to existing runtimes retain their existing path. The
person-directory transition adds a fixed number of point operations, not UID
fanout. Witnesses must survive repeated deletion indefinitely: automatic cleanup
without an independent non-reuse proof would reopen the ABA window.

## Repeatable validation

From the repository root:

```sh
GOWORK=off go test -race -p 2 ./pkg/db/meta ./pkg/slot/fsm ./pkg/cluster/channels -count=1
GOWORK=off go test -tags=integration -race -p 2 ./internal/app -run '^TestMQTTPreparedAppendRejects.*SingleNodeCluster$|^TestMQTTInboxAdmissionOfflineDirectoryBeforeFirstPersonSingleNodeCluster$|^TestMQTTWillExecutionSingleNodeClusterRecoversBeforeRevocation$' -count=1 -v
GOWORK=off go run ./scripts/flowcheck --mode check
```

The failure inventory preceded the implementation. The creation-coalescer test
failed by returning candidate epoch 1 instead of committed epoch 77. The directory
regressions failed on retained old tasks and successful absent-runtime admission;
both now pass. Full related race suites passed (metadata 28.486s, Slot FSM 17.642s,
Channel routing 7.082s). Four real app integrations passed together in 15.383s.
The new integration emits:

```text
mqtt_runtime_incarnation_evidence: nodes=1 hash_slots=256 physical_delete=true slot_fsm=true old_epoch=1 new_epoch=2 old_route=1 new_route=2 stale_request_absent=true committed_bodies=2
```

It uses real Slot create/delete commands and committed indexed reads to prove
both valid messages and absence of the rejected request. These are package
integrations, not product process E2E or scale acceptance. They do not prove
atomicity between Slot deletion and Channel work already admitted before deletion,
or safety when distributed restore installs an older metadata generation.

## Frozen repository context

Source: `9d5977d394f4ce23f408e6d7dd394bedc4b9422a`. Applicable rules and navigation were read before
package work. SHA-256 values refer to that exact source revision.

| File | SHA-256 |
| --- | --- |
| `AGENTS.md` | `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade` |
| `pkg/db/FLOW.md` | `33c7ba81c18ed8cb377e9bef541526028ef31e64dd03becc7ac1ea6cc14fb10a` |
| `pkg/db/meta/FLOW.md` | `9ae26c99184159c100544920f08a8023f7098e6a1ff6b50ba6e1e067618fa8ee` |
| `pkg/slot/FLOW.md` | `f4bdca225e7a62404de5c444f6547e5ded46c5f7819d5ee184465babecf6b03a` |
| `pkg/cluster/FLOW.md` | `621efd9665273bf7a30cd773ef0b35774b4a62e680dc5e5966cf8e03b986bbb3` |
| `internal/app/FLOW.md` | `fbe86f362f54a81688157a29bcd07e58844021289e52bd497e745fb4ffcd3db2` |
