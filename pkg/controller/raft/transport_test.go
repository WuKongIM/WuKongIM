package raft

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestControllerRaftTransportChunksOversizedSnapshot(t *testing.T) {
	srv := transport.NewServer()
	singleFrames := make(chan []byte, 1)
	chunkFrames := make(chan []byte, 8)
	srv.Handle(msgTypeControllerRaft, func(body []byte) {
		singleFrames <- append([]byte(nil), body...)
	})
	srv.Handle(msgTypeControllerRaftSnapshotChunk, func(body []byte) {
		chunkFrames <- append([]byte(nil), body...)
	})
	require.NoError(t, srv.Start("127.0.0.1:0"))
	defer srv.Stop()

	pool := transport.NewPool(controllerRaftTransportTestDiscovery{addr: srv.Listener().Addr().String()}, 1, 5*time.Second)
	defer pool.Close()
	rt := newTransport(pool, 1)
	defer rt.Close()
	rt.maxBodySize = 256

	snapshotData := bytes.Repeat([]byte("0123456789abcdef"), 64)
	msg := raftpb.Message{
		To:   2,
		From: 1,
		Type: raftpb.MsgSnap,
		Term: 8,
		Snapshot: &raftpb.Snapshot{
			Data: snapshotData,
			Metadata: raftpb.SnapshotMetadata{
				Index: 55,
				Term:  7,
				ConfState: raftpb.ConfState{
					Voters: []uint64{1, 2},
				},
			},
		},
	}

	require.NoError(t, rt.Send(context.Background(), []raftpb.Message{msg}))

	var received [][]byte
	deadline := time.After(2 * time.Second)
	for {
		select {
		case body := <-chunkFrames:
			require.LessOrEqual(t, len(body), rt.maxBodySize)
			received = append(received, body)
			chunk, err := decodeControllerRaftSnapshotChunkBody(body)
			require.NoError(t, err)
			var gotTotal uint64
			for _, raw := range received {
				decoded, err := decodeControllerRaftSnapshotChunkBody(raw)
				require.NoError(t, err)
				gotTotal += uint64(len(decoded.data))
			}
			if gotTotal >= chunk.total {
				require.GreaterOrEqual(t, len(received), 2)
				select {
				case <-singleFrames:
					t.Fatal("oversized controller snapshot was sent as a single frame")
				default:
				}
				return
			}
		case <-singleFrames:
			t.Fatal("oversized controller snapshot was sent as a single frame")
		case <-deadline:
			t.Fatalf("timeout waiting for controller snapshot chunks, received %d", len(received))
		}
	}
}

func TestControllerRaftSnapshotAssemblerRebuildsMessage(t *testing.T) {
	msg := raftpb.Message{
		To:   2,
		From: 1,
		Type: raftpb.MsgSnap,
		Snapshot: &raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{Index: 12, Term: 3},
		},
	}
	msgData, err := msg.Marshal()
	require.NoError(t, err)
	assembler := newControllerRaftSnapshotAssembler(time.Minute, func() time.Time { return time.Unix(10, 0) })

	first := controllerRaftSnapshotChunk{
		chunkID: 7,
		from:    1,
		to:      2,
		index:   12,
		term:    3,
		total:   10,
		offset:  0,
		message: msgData,
		data:    []byte("hello"),
	}
	assembled, ok, err := assembler.add(first)
	require.NoError(t, err)
	require.False(t, ok)

	second := first
	second.offset = 5
	second.data = []byte("world")
	assembled, ok, err = assembler.add(second)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, raftpb.MsgSnap, assembled.Type)
	require.Equal(t, []byte("helloworld"), assembled.Snapshot.Data)
}

type controllerRaftTransportTestDiscovery struct {
	addr string
}

func (d controllerRaftTransportTestDiscovery) Resolve(nodeID uint64) (string, error) {
	if nodeID == 2 && d.addr != "" {
		return d.addr, nil
	}
	return "", errors.New("node not found")
}
