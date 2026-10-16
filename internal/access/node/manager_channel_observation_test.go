package node

import (
	"context"
	"errors"
	"testing"

	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/stretchr/testify/require"
)

// Failure inventory: old peers decode observation bytes as list requests;
// truncation/oversize/trailing bytes pass; response identity or order changes;
// unknown state carries a fabricated active flag or message frontier.
func TestManagerChannelObservationRPCPreservesAlignmentAndOptionalSequence(t *testing.T) {
	target := management.ChannelObservationTarget{ChannelID: "cold", ChannelType: 2, LeaderID: 2, Epoch: 3, LeaderEpoch: 5, RouteGeneration: 7}
	seq := uint64(11)
	service := &observationRPCFake{rows: []management.ChannelObservation{{Target: target, Known: true, MaxMessageSeq: &seq}}}
	adapter := New(Options{ManagerChannelObservations: service})
	node := &fakeManagerChannelRPCNode{handler: adapter.HandleManagerChannelRPC}
	got, err := NewClient(node).ObserveManagerChannels(context.Background(), 2, []management.ChannelObservationTarget{target}, true)
	require.NoError(t, err)
	require.Equal(t, service.rows, got)
	require.True(t, service.include)
	require.Equal(t, ManagerChannelRPCServiceID, node.serviceID)
	service.rows[0].Target.LeaderEpoch++
	_, err = NewClient(node).ObserveManagerChannels(context.Background(), 2, []management.ChannelObservationTarget{target}, true)
	require.Error(t, err)
}

func TestManagerChannelObservationRPCRejectsInvalidEvidenceAndLegacyPeers(t *testing.T) {
	target := management.ChannelObservationTarget{ChannelID: "x", ChannelType: 2, LeaderID: 2}
	for _, rows := range [][]management.ChannelObservation{
		{{Target: target, Active: true}},
		{{Target: target, MaxMessageSeq: new(uint64)}},
	} {
		_, err := encodeManagerObservationResponse(managerObservationResponse{Status: rpcStatusOK, Items: rows})
		require.Error(t, err)
	}
	body, err := encodeManagerObservationRequest(managerObservationRequest{Targets: []management.ChannelObservationTarget{target}})
	require.NoError(t, err)
	_, err = decodeManagerChannelRequest(body)
	require.Error(t, err, "old peers must fail explicitly")
	for _, broken := range [][]byte{body[:len(body)-1], append(append([]byte(nil), body...), 0), make([]byte, maxManagerObservationBytes+1)} {
		_, err := decodeManagerObservationRequest(broken)
		require.Error(t, err)
	}
	_, err = encodeManagerObservationRequest(managerObservationRequest{Targets: make([]management.ChannelObservationTarget, 201)})
	require.Error(t, err)
	oldPeer := &fakeManagerChannelRPCNode{handler: func(context.Context, []byte) ([]byte, error) { return nil, errors.New("unsupported codec") }}
	_, err = NewClient(oldPeer).ObserveManagerChannels(context.Background(), 2, []management.ChannelObservationTarget{target}, false)
	require.Error(t, err)
}

type observationRPCFake struct {
	rows    []management.ChannelObservation
	include bool
}

func (f *observationRPCFake) ObserveLocalChannels(_ context.Context, _ []management.ChannelObservationTarget, include bool) ([]management.ChannelObservation, error) {
	f.include = include
	return f.rows, nil
}
