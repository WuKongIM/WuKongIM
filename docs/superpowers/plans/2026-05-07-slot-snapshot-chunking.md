# Slot Snapshot Chunking Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Allow Slot Raft snapshots larger than one transport frame to be transferred to new learners without exceeding the 64MB transport frame limit.

**Architecture:** Keep Raft/FSM semantics unchanged. The cluster raft transport detects oversized Slot Raft `MsgSnap` messages, splits `Snapshot.Data` into bounded wire chunks, and the receiver reassembles chunks back into the original `raftpb.Message` before calling `Runtime.Step`.

**Tech Stack:** Go, etcd raft `raftpb`, existing `pkg/transport` frame transport, existing cluster raft message codec.

---

### Task 1: Codec Tests And Wire Format

**Files:**
- Modify: `pkg/cluster/codec.go`
- Test: `pkg/cluster/transport_test.go` or new focused codec test

- [x] Write a failing test for snapshot chunk encode/decode preserving slot ID, raft message header, chunk offsets, and bytes.
- [x] Run the focused test and verify it fails because the codec is missing.
- [x] Add a compact binary codec for `[slotID][chunkID][from][to][index][term][total][offset][dataLen][raftMessageWithoutSnapshotData][data]`.
- [x] Run the focused test and verify it passes.

### Task 2: Transport Send Chunking

**Files:**
- Modify: `pkg/cluster/transport.go`
- Test: `pkg/cluster/transport_test.go`

- [x] Write a failing test where `MsgSnap` marshals larger than a configured max raft frame body and expect multiple `msgTypeRaftSnapshotChunk` sends, each under limit.
- [x] Run the focused test and verify RED.
- [x] Add snapshot detection in `raftTransport.sendSingle` and batched send path so only oversized `MsgSnap` uses chunks; ordinary messages still use `msgTypeRaft` / `msgTypeRaftBatch`.
- [x] Run the focused test and verify GREEN.

### Task 3: Receiver Reassembly

**Files:**
- Modify: `pkg/cluster/cluster.go`
- Create/modify: `pkg/cluster/snapshot_chunks.go`
- Test: `pkg/cluster/cluster_test.go` or `pkg/cluster/transport_test.go`

- [x] Write a failing test that feeds chunks into the cluster handler and expects a single complete `Runtime.Step` with the original `MsgSnap`.
- [x] Run the focused test and verify RED.
- [x] Implement bounded in-memory reassembly keyed by sender/target/slot/snapshot index/term/chunk ID, with duplicate chunk tolerance and TTL cleanup.
- [x] Register `msgTypeRaftSnapshotChunk` in transport server/glue startup.
- [x] Run the focused test and verify GREEN.

### Task 4: Learner Catch-Up Regression And Docs

**Files:**
- Modify: `pkg/slot/multiraft/compaction_test.go` or `pkg/cluster/*_test.go`
- Modify: `pkg/slot/FLOW.md`, `pkg/cluster/FLOW.md`, `docs/development/PROJECT_KNOWLEDGE.md`

- [x] Add/extend a regression proving compacted leader can transfer large snapshot to a new learner through chunked transport.
- [x] Run relevant Slot/cluster tests.
- [x] Update FLOW docs with chunked `MsgSnap` transfer and note that `StateMachine.Restore` remains unchanged.
- [x] Run `git diff --check`.
