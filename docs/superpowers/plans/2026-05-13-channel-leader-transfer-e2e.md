# Channel Leader Transfer E2E Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a black-box process E2E scenario that proves single-channel explicit leader transfer works in a real three-node cluster and preserves message delivery.

**Architecture:** Reuse the existing `test/e2e` real-process harness and keep all assertions external through WKProto and manager HTTP. Add reusable manager DTO/helper functions in `test/e2e/suite`, then add one focused `message/channel_leader_transfer` scenario that activates a person channel, transfers its leader to a non-leader ISR replica, verifies metadata invariants, and sends messages after transfer.

**Tech Stack:** Go 1.23, `testing`, `testify/require`, `net/http`, `encoding/json`, existing `test/e2e/suite`, WKProto frame helpers, manager HTTP APIs, `//go:build e2e`.

---

## References

- Design spec: `docs/superpowers/specs/2026-05-13-channel-leader-transfer-e2e-design.md`
- P0.6 implementation spec: `docs/superpowers/specs/2026-05-12-channel-cluster-leader-transfer-p06-design.md`
- Manager restructure doc: `docs/raw/web-admin-restructure.md`
- E2E rules: `test/e2e/AGENTS.md`
- Message domain rules: `test/e2e/message/AGENTS.md`
- Existing cross-node scenario: `test/e2e/message/cross_node_closure/cross_node_closure_test.go`
- Existing slot failover scenario: `test/e2e/message/slot_leader_failover/slot_leader_failover_test.go`
- Existing manager helper file: `test/e2e/suite/manager_client.go`
- Follow `@superpowers:test-driven-development` while implementing each task.
- Run `@superpowers:verification-before-completion` before claiming completion.

## File Structure

- Modify: `test/e2e/suite/manager_client.go`
  - Add channel-cluster replica detail DTOs.
  - Add channel leader transfer response DTOs.
  - Add `FetchChannelClusterReplicas`, `TransferChannelClusterLeader`, `WaitForChannelClusterReplicas`, and `PersonChannelID`.
- Modify: `test/e2e/suite/manager_client_test.go`
  - Add pure decode/helper tests for the new DTOs and `PersonChannelID`.
- Create: `test/e2e/message/channel_leader_transfer/AGENTS.md`
  - Document scenario purpose, external steps, diagnostics, and run command.
- Create: `test/e2e/message/channel_leader_transfer/channel_leader_transfer_test.go`
  - Own the end-to-end scenario and business assertions.
- Modify: `test/e2e/AGENTS.md`
  - Add the scenario to the top-level catalog.
- Modify: `test/e2e/message/AGENTS.md`
  - Add the scenario to the message-domain catalog.

## Task 1: Add reusable manager helpers for channel-cluster leader transfer

**Files:**
- Modify: `test/e2e/suite/manager_client.go`
- Modify: `test/e2e/suite/manager_client_test.go`

- [ ] **Step 1: Re-read E2E rules**

Run:

```bash
cat test/e2e/AGENTS.md
cat test/e2e/message/AGENTS.md
```

Expected: confirm helpers stay black-box, use manager APIs, and do not import `internal/app`.

- [ ] **Step 2: Write failing DTO/helper tests**

In `test/e2e/suite/manager_client_test.go`, add tests with these names:

```go
func TestDecodeChannelClusterReplicaDetail(t *testing.T) {
	body := []byte(`{
		"channel": {
			"channel_id": "u1@u2",
			"channel_type": 1,
			"slot_id": 1,
			"hash_slot": 9,
			"channel_epoch": 7,
			"leader_epoch": 3,
			"leader": 1,
			"replicas": [1, 2, 3],
			"isr": [1, 2, 3],
			"min_isr": 2,
			"max_message_seq": 10,
			"status": "active",
			"features": 5,
			"lease_until_ms": 1700000000000
		},
		"runtime_reported": true,
		"commit_seq": 10,
		"min_available_seq": 1,
		"retention_through_seq": 0,
		"replicas": [
			{"node_id": 1, "role": "leader", "is_leader": true, "in_isr": true, "reported": true, "commit_seq": 10, "leo": 10, "checkpoint_hw": 10, "lag": 0},
			{"node_id": 2, "role": "follower", "is_leader": false, "in_isr": true, "reported": false, "commit_seq": null, "leo": null, "checkpoint_hw": null, "lag": null}
		]
	}`)

	got, err := decodeChannelClusterReplicaDetail(body)
	require.NoError(t, err)
	require.Equal(t, "u1@u2", got.Channel.ChannelID)
	require.Equal(t, int64(1), got.Channel.ChannelType)
	require.Equal(t, uint64(1), got.Channel.Leader)
	require.Equal(t, []uint64{1, 2, 3}, got.Channel.Replicas)
	require.Equal(t, []uint64{1, 2, 3}, got.Channel.ISR)
	require.Len(t, got.Replicas, 2)
	require.True(t, got.Replicas[1].InISR)
	require.False(t, got.Replicas[1].IsLeader)
	require.Nil(t, got.Replicas[1].CommitSeq)
}

func TestDecodeChannelLeaderTransferResponse(t *testing.T) {
	body := []byte(`{
		"changed": true,
		"channel": {
			"channel_id": "u1@u2",
			"channel_type": 1,
			"slot_id": 1,
			"hash_slot": 9,
			"channel_epoch": 7,
			"leader_epoch": 4,
			"leader": 2,
			"replicas": [1, 2, 3],
			"isr": [1, 2, 3],
			"min_isr": 2,
			"max_message_seq": 10,
			"status": "active",
			"features": 5,
			"lease_until_ms": 1700000001000
		}
	}`)

	got, err := decodeChannelLeaderTransferResponse(body)
	require.NoError(t, err)
	require.True(t, got.Changed)
	require.Equal(t, uint64(2), got.Channel.Leader)
	require.Equal(t, uint64(4), got.Channel.LeaderEpoch)
}

func TestPersonChannelID(t *testing.T) {
	left := PersonChannelID("u1", "u2")
	right := PersonChannelID("u2", "u1")
	require.NotEmpty(t, left)
	require.Equal(t, left, right)
	require.Contains(t, left, "@")
}
```

- [ ] **Step 3: Run helper tests to verify RED**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(DecodeChannelClusterReplicaDetail|DecodeChannelLeaderTransferResponse|PersonChannelID)$' -count=1
```

Expected: FAIL because DTO decode functions and `PersonChannelID` are undefined.

- [ ] **Step 4: Implement DTOs and helpers**

In `test/e2e/suite/manager_client.go`, add documented exported DTOs:

```go
// ManagerChannelRuntimeMetaDetail mirrors channel runtime metadata used by channel-cluster E2E assertions.
type ManagerChannelRuntimeMetaDetail struct {
	ChannelID     string   `json:"channel_id"`
	ChannelType   int64    `json:"channel_type"`
	SlotID        uint32   `json:"slot_id"`
	HashSlot      uint32   `json:"hash_slot"`
	ChannelEpoch  uint64   `json:"channel_epoch"`
	LeaderEpoch   uint64   `json:"leader_epoch"`
	Leader        uint64   `json:"leader"`
	Replicas      []uint64 `json:"replicas"`
	ISR           []uint64 `json:"isr"`
	MinISR        int      `json:"min_isr"`
	MaxMessageSeq uint64   `json:"max_message_seq"`
	Status        string   `json:"status"`
	Features      uint64   `json:"features"`
	LeaseUntilMS  int64    `json:"lease_until_ms"`
}

// ManagerChannelClusterReplicaDetail mirrors /manager/channel-cluster/:type/:id/replicas.
type ManagerChannelClusterReplicaDetail struct {
	Channel             ManagerChannelRuntimeMetaDetail       `json:"channel"`
	RuntimeReported     bool                                  `json:"runtime_reported"`
	CommitSeq           *uint64                               `json:"commit_seq"`
	MinAvailableSeq     *uint64                               `json:"min_available_seq"`
	RetentionThroughSeq *uint64                               `json:"retention_through_seq"`
	Replicas            []ManagerChannelClusterReplicaStatus  `json:"replicas"`
}

// ManagerChannelClusterReplicaStatus mirrors one channel-cluster replica row.
type ManagerChannelClusterReplicaStatus struct {
	NodeID       uint64  `json:"node_id"`
	Role         string  `json:"role"`
	IsLeader     bool    `json:"is_leader"`
	InISR        bool    `json:"in_isr"`
	Reported     bool    `json:"reported"`
	CommitSeq    *uint64 `json:"commit_seq"`
	LEO          *uint64 `json:"leo"`
	CheckpointHW *uint64 `json:"checkpoint_hw"`
	Lag          *uint64 `json:"lag"`
}

// ManagerChannelLeaderTransferResponse mirrors the manager leader transfer response.
type ManagerChannelLeaderTransferResponse struct {
	Changed bool                            `json:"changed"`
	Channel ManagerChannelRuntimeMetaDetail `json:"channel"`
}
```

Add helpers:

```go
func FetchChannelClusterReplicas(ctx context.Context, node StartedNode, channelType uint8, channelID string) (ManagerChannelClusterReplicaDetail, []byte, error)

func TransferChannelClusterLeader(ctx context.Context, node StartedNode, channelType uint8, channelID string, targetNodeID uint64) (ManagerChannelLeaderTransferResponse, []byte, error)

func WaitForChannelClusterReplicas(ctx context.Context, node StartedNode, channelType uint8, channelID string, accept func(ManagerChannelClusterReplicaDetail) bool) (ManagerChannelClusterReplicaDetail, []byte, error)

func PersonChannelID(leftUID, rightUID string) string
```

Implementation notes:

- Use `url.PathEscape(channelID)` for URL paths.
- Use existing `fetchHTTPBody` and `postHTTPJSONBody`.
- Use the same `readyPollInterval` polling pattern as existing manager helpers.
- Implement `PersonChannelID` using the current CRC32 ordering copied from existing e2e scenario helpers, then remove duplicate copies only if they are in files you are intentionally touching.

- [ ] **Step 5: Run helper tests to verify GREEN**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run 'Test(DecodeChannelClusterReplicaDetail|DecodeChannelLeaderTransferResponse|PersonChannelID)$' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run full suite helper package**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit helper slice**

```bash
git add test/e2e/suite/manager_client.go test/e2e/suite/manager_client_test.go
git commit -m "test: add channel leader transfer e2e helpers"
```

## Task 2: Add the channel leader transfer E2E scenario

**Files:**
- Create: `test/e2e/message/channel_leader_transfer/AGENTS.md`
- Create: `test/e2e/message/channel_leader_transfer/channel_leader_transfer_test.go`
- Modify: `test/e2e/AGENTS.md`
- Modify: `test/e2e/message/AGENTS.md`

- [ ] **Step 1: Create scenario AGENTS documentation**

Create `test/e2e/message/channel_leader_transfer/AGENTS.md`:

```markdown
# channel_leader_transfer AGENTS

This file is for agents working on `test/e2e/message/channel_leader_transfer`.

## Purpose

Prove a real three-node cluster can safely transfer one active person-channel leader to a non-leader ISR replica through the manager channel-cluster API, then continue WKProto delivery.

## Cluster Shape

One three-node cluster. Sender and recipient connect to follower nodes. The channel leader transfer target is selected from authoritative non-leader ISR replica rows.

## External Steps

1. Start a real three-node cluster through `test/e2e/suite`.
2. Wait for cluster readiness and resolve Slot `1` topology through manager APIs.
3. Connect two WKProto clients to follower nodes.
4. Send a bootstrap person-channel message to activate channel runtime metadata.
5. Poll `/manager/channel-cluster/:type/:id/replicas` until the channel is active and has a non-leader ISR target.
6. POST `/manager/channel-cluster/:type/:id/leader/transfer`.
7. Assert the authoritative leader changes while replica set, ISR, MinISR, status, features, and channel epoch stay stable.
8. Send another WKProto message after transfer and observe `SendAck`, `Recv`, and `RecvAck`.

## Observable Outcome

The manager transfer response reports the selected target as leader and post-transfer message delivery succeeds.

## Failure Diagnostics

- node-scoped stdout/stderr
- node `logs/app.log` and `logs/error.log` tails
- last observed manager slot body
- last observed channel-cluster replica response

## Run

`go test -tags=e2e ./test/e2e/message/channel_leader_transfer -count=1 -timeout 2m`

## Maintenance Rules

- If the manager endpoint, target-selection rule, metadata invariants, run command, or diagnostics change, update this file in the same change.
- The test files in this scenario directory must stay consistent with the behavior described here.
```

- [ ] **Step 2: Update domain catalogs**

In `test/e2e/message/AGENTS.md`, add a scenario row:

```markdown
| `channel_leader_transfer` | Prove an active person-channel leader can be transferred to a non-leader ISR replica through manager APIs while WKProto delivery continues. | `go test -tags=e2e ./test/e2e/message/channel_leader_transfer -count=1 -timeout 2m` |
```

In `test/e2e/AGENTS.md`, add the same scenario to the catalog table.

- [ ] **Step 3: Write the failing scenario test**

Create `test/e2e/message/channel_leader_transfer/channel_leader_transfer_test.go`:

```go
//go:build e2e

package channel_leader_transfer

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestChannelLeaderTransferPreservesCrossNodeDelivery(t *testing.T) {
	s := suite.New(t)
	cluster := s.StartThreeNodeCluster()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())

	topology, err := cluster.ResolveSlotTopology(ctx, 1)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Len(t, topology.FollowerNodeIDs, 2)

	senderNode := cluster.MustNode(topology.FollowerNodeIDs[0])
	recipientNode := cluster.MustNode(topology.FollowerNodeIDs[1])
	managerNode := cluster.MustNode(topology.LeaderNodeID)

	sender, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = sender.Close() }()

	recipient, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = recipient.Close() }()

	const senderUID = "clt-u1"
	const recipientUID = "clt-u2"
	require.NoError(t, sender.Connect(senderNode.GatewayAddr(), senderUID, senderUID+"-device"))
	require.NoError(t, recipient.Connect(recipientNode.GatewayAddr(), recipientUID, recipientUID+"-device"))

	ok, err := suite.ConnectionsContainUID(ctx, *senderNode, senderUID)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	ok, err = suite.ConnectionsContainUID(ctx, *recipientNode, recipientUID)
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.True(t, ok, cluster.DumpDiagnostics())

	sendAndRequireRecv(t, cluster, sender, recipient, senderUID, recipientUID, "channel-transfer-bootstrap-1", 1, []byte("before channel leader transfer"))

	channelID := suite.PersonChannelID(senderUID, recipientUID)
	before, beforeBody, err := suite.WaitForChannelClusterReplicas(ctx, *managerNode, frame.ChannelTypePerson, channelID, func(detail suite.ManagerChannelClusterReplicaDetail) bool {
		if detail.Channel.Status != "active" || detail.Channel.Leader == 0 {
			return false
		}
		return selectTransferTarget(detail) != 0
	})
	require.NoError(t, err, cluster.DumpDiagnostics()+"\nchannel replicas: "+string(beforeBody))

	targetNodeID := selectTransferTarget(before)
	require.NotZero(t, targetNodeID, cluster.DumpDiagnostics()+"\nchannel replicas: "+string(beforeBody))
	require.NotEqual(t, before.Channel.Leader, targetNodeID)

	result, transferBody, err := suite.TransferChannelClusterLeader(ctx, *managerNode, frame.ChannelTypePerson, channelID, targetNodeID)
	require.NoError(t, err, cluster.DumpDiagnostics()+"\nchannel replicas: "+string(beforeBody))
	require.True(t, result.Changed, cluster.DumpDiagnostics()+"\ntransfer response: "+string(transferBody))
	require.Equal(t, targetNodeID, result.Channel.Leader)
	require.Greater(t, result.Channel.LeaderEpoch, before.Channel.LeaderEpoch)
	require.Equal(t, before.Channel.ChannelEpoch, result.Channel.ChannelEpoch)
	require.Equal(t, before.Channel.Replicas, result.Channel.Replicas)
	require.Equal(t, before.Channel.ISR, result.Channel.ISR)
	require.Equal(t, before.Channel.MinISR, result.Channel.MinISR)
	require.Equal(t, before.Channel.Status, result.Channel.Status)
	require.Equal(t, before.Channel.Features, result.Channel.Features)

	after, afterBody, err := suite.WaitForChannelClusterReplicas(ctx, *managerNode, frame.ChannelTypePerson, channelID, func(detail suite.ManagerChannelClusterReplicaDetail) bool {
		return detail.Channel.Leader == targetNodeID && detail.Channel.LeaderEpoch >= result.Channel.LeaderEpoch
	})
	require.NoError(t, err, cluster.DumpDiagnostics()+"\ntransfer response: "+string(transferBody))
	require.Equal(t, targetNodeID, after.Channel.Leader, cluster.DumpDiagnostics()+"\nchannel replicas: "+string(afterBody))

	sendAndRequireRecv(t, cluster, sender, recipient, senderUID, recipientUID, "channel-transfer-after-2", 2, []byte("after channel leader transfer"))
	sendAndRequireRecv(t, cluster, recipient, sender, recipientUID, senderUID, "channel-transfer-reverse-1", 1, []byte("reverse after channel leader transfer"))
}

func selectTransferTarget(detail suite.ManagerChannelClusterReplicaDetail) uint64 {
	for _, replica := range detail.Replicas {
		if !replica.IsLeader && replica.InISR {
			return replica.NodeID
		}
	}
	return 0
}

func sendAndRequireRecv(
	t *testing.T,
	cluster *suite.StartedCluster,
	sender, recipient *suite.WKProtoClient,
	senderUID, recipientUID, clientMsgNo string,
	clientSeq uint64,
	payload []byte,
) {
	t.Helper()

	require.NoError(t, sender.SendFrame(&frame.SendPacket{
		ChannelID:   recipientUID,
		ChannelType: frame.ChannelTypePerson,
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		Payload:     payload,
	}))

	sendack, err := sender.ReadSendAck()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageID)
	require.NotZero(t, sendack.MessageSeq)

	recv, err := recipient.ReadRecv()
	require.NoError(t, err, cluster.DumpDiagnostics())
	require.Equal(t, senderUID, recv.FromUID)
	require.Equal(t, senderUID, recv.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, recv.ChannelType)
	require.Equal(t, payload, recv.Payload)
	require.Equal(t, sendack.MessageID, recv.MessageID)
	require.Equal(t, sendack.MessageSeq, recv.MessageSeq)

	require.NoError(t, recipient.RecvAck(recv.MessageID, recv.MessageSeq))
}
```

- [ ] **Step 4: Run scenario to verify RED**

Run:

```bash
go test -tags=e2e ./test/e2e/message/channel_leader_transfer -run TestChannelLeaderTransferPreservesCrossNodeDelivery -count=1 -timeout 2m
```

Expected: FAIL if Task 1 helpers have not been implemented yet, or PASS after Task 1 is complete and P0.6 runtime works. If it fails at runtime, inspect `cluster.DumpDiagnostics()` and the last channel replica/transfer body before changing product code.

- [ ] **Step 5: Run scenario after helpers are implemented**

Run:

```bash
go test -tags=e2e ./test/e2e/message/channel_leader_transfer -count=1 -timeout 2m
```

Expected: PASS.

- [ ] **Step 6: Commit scenario slice**

```bash
git add test/e2e/message/channel_leader_transfer test/e2e/AGENTS.md test/e2e/message/AGENTS.md
git commit -m "test: add channel leader transfer e2e"
```

## Task 3: Targeted regression verification

**Files:**
- No source changes expected.

- [ ] **Step 1: Run suite helper tests**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -count=1
```

Expected: PASS.

- [ ] **Step 2: Run new scenario**

Run:

```bash
go test -tags=e2e ./test/e2e/message/channel_leader_transfer -count=1 -timeout 2m
```

Expected: PASS.

- [ ] **Step 3: Run adjacent message scenarios**

Run:

```bash
go test -tags=e2e ./test/e2e/message/cross_node_closure ./test/e2e/message/slot_leader_failover -count=1 -timeout 5m
```

Expected: PASS.

- [ ] **Step 4: Optional full E2E**

Run only when time allows:

```bash
go test -tags=e2e ./test/e2e/... -count=1
```

Expected: PASS. If this is too slow or unrelated scenarios fail, report exact failures and keep the targeted verification as the completion gate.

- [ ] **Step 5: Final status check**

Run:

```bash
git status --short
git log --oneline -3
```

Expected: only intentional committed changes remain; no unrelated dirty files were modified by this work.

## Completion Checklist

- [ ] `test/e2e` imports remain black-box-safe and do not import `internal/app`.
- [ ] New helper DTO fields have English comments where exported.
- [ ] No fixed sleeps were added.
- [ ] Scenario selects a target only from non-leader ISR rows.
- [ ] Transfer assertions prove only leader fields changed.
- [ ] Post-transfer WKProto delivery succeeds.
- [ ] `test/e2e/AGENTS.md` and `test/e2e/message/AGENTS.md` include the new scenario.
