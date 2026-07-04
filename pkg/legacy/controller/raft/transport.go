package raft

import (
	"context"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
	"go.etcd.io/raft/v3/raftpb"
)

const (
	// msgTypeControllerRaft must not collide with cluster-level transport
	// message types because controller raft shares the same transport.Server.
	msgTypeControllerRaft              uint8  = 5
	msgTypeControllerRaftSnapshotChunk uint8  = 6
	controllerShardKey                 uint64 = 1
)

type raftTransport struct {
	// client sends controller Raft frames through the shared cluster transport pool.
	client *transport.Client
	// localNodeID identifies looped outbound frames that must never enter the network.
	localNodeID uint64
	// maxBodySize overrides the transport frame body limit in focused tests.
	maxBodySize int
	// nextSnapshotChunkID assigns chunk groups for oversized MsgSnap sends.
	nextSnapshotChunkID atomic.Uint64
}

func newTransport(pool *transport.Pool, localNodeID uint64) *raftTransport {
	return &raftTransport{
		client:      transport.NewClient(pool),
		localNodeID: localNodeID,
	}
}

func (t *raftTransport) Close() {
	if t == nil || t.client == nil {
		return
	}
	t.client.Stop()
}

func (t *raftTransport) Send(ctx context.Context, messages []raftpb.Message) error {
	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if msg.To == 0 || msg.To == msg.From || (t.localNodeID != 0 && msg.To == t.localNodeID) {
			continue
		}
		data, err := msg.Marshal()
		if err != nil {
			return err
		}
		if len(data) > t.frameBodyLimit() && isControllerRaftSnapshotMessage(msg) {
			if err := t.sendSnapshotChunks(ctx, msg); err != nil {
				return err
			}
			continue
		}
		// Match multiraft transport semantics: transient send failures are not fatal
		// to the local raft loop and are retried by later raft traffic.
		_ = t.client.Send(uint64(msg.To), controllerShardKey, msgTypeControllerRaft, data)
	}
	return nil
}

func isControllerRaftSnapshotMessage(msg raftpb.Message) bool {
	return msg.Type == raftpb.MsgSnap && msg.Snapshot != nil && len(msg.Snapshot.Data) > 0
}

func (t *raftTransport) sendSnapshotChunks(ctx context.Context, msg raftpb.Message) error {
	if !isControllerRaftSnapshotMessage(msg) {
		return nil
	}
	header := msg
	snap := *msg.Snapshot
	snapshotData := snap.Data
	snap.Data = nil
	header.Snapshot = &snap
	headerData, err := header.Marshal()
	if err != nil {
		return err
	}
	bodyLimit := t.frameBodyLimit()
	chunkDataLimit := bodyLimit - controllerRaftSnapshotChunkHeaderSize - len(headerData)
	if chunkDataLimit <= 0 {
		return nil
	}
	chunkID := t.nextSnapshotChunkID.Add(1)
	for offset := 0; offset < len(snapshotData); offset += chunkDataLimit {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := offset + chunkDataLimit
		if end > len(snapshotData) {
			end = len(snapshotData)
		}
		chunk := controllerRaftSnapshotChunk{
			chunkID: chunkID,
			from:    uint64(msg.From),
			to:      uint64(msg.To),
			index:   msg.Snapshot.Metadata.Index,
			term:    msg.Snapshot.Metadata.Term,
			total:   uint64(len(snapshotData)),
			offset:  uint64(offset),
			message: headerData,
			data:    snapshotData[offset:end],
		}
		body := encodeControllerRaftSnapshotChunkBody(chunk)
		if len(body) > bodyLimit {
			return nil
		}
		if err := t.client.Send(uint64(msg.To), controllerShardKey, msgTypeControllerRaftSnapshotChunk, body); err != nil {
			return nil
		}
	}
	return nil
}

func (t *raftTransport) frameBodyLimit() int {
	if t != nil && t.maxBodySize > 0 {
		return t.maxBodySize
	}
	return transport.MaxMessageSize
}
