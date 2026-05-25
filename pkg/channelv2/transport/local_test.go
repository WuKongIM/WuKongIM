package transport

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

type captureServer struct {
	pullReqs     []PullRequest
	ackReqs      []AckRequest
	pullHintReqs []PullHintRequest
	notifyReqs   []NotifyRequest
	pullHint     PullHintRequest
	notify       NotifyRequest
}

func (s *captureServer) HandlePull(ctx context.Context, req PullRequest) (PullResponse, error) {
	s.pullReqs = append(s.pullReqs, req)
	return PullResponse{ChannelKey: req.ChannelKey, Epoch: req.Epoch, LeaderEpoch: req.LeaderEpoch, LeaderHW: req.NextOffset - 1, LeaderLEO: req.NextOffset - 1}, nil
}

func (s *captureServer) HandleAck(ctx context.Context, req AckRequest) error {
	s.ackReqs = append(s.ackReqs, req)
	return nil
}

func (s *captureServer) HandleNotify(ctx context.Context, req NotifyRequest) error {
	s.notifyReqs = append(s.notifyReqs, req)
	s.notify = req
	return nil
}

func (s *captureServer) HandlePullHint(ctx context.Context, req PullHintRequest) error {
	s.pullHintReqs = append(s.pullHintReqs, req)
	s.pullHint = req
	return nil
}

func TestLocalNetworkClientReturnsUsableClient(t *testing.T) {
	network := NewLocalNetwork()
	require.Same(t, network, network.Client())
}

func TestLocalNetworkRoutesPullAckPullHintAndNotify(t *testing.T) {
	network := NewLocalNetwork()
	server := &captureServer{}
	network.Register(2, server)
	client := network.Client()
	key := ch.ChannelKey("1:route")
	id := ch.ChannelID{ID: "route", Type: 1}

	_, err := client.Pull(context.Background(), 2, PullRequest{ChannelKey: key, ChannelID: id, Epoch: 1, LeaderEpoch: 2, Follower: 3, NextOffset: 1, MaxBytes: 1024})
	require.NoError(t, err)
	require.NoError(t, client.Ack(context.Background(), 2, AckRequest{ChannelKey: key, Epoch: 1, LeaderEpoch: 2, Follower: 3, MatchOffset: 1}))
	require.NoError(t, client.PullHint(context.Background(), 2, PullHintRequest{ChannelKey: key, ChannelID: id, Epoch: 1, LeaderEpoch: 2, Leader: 1, LeaderLEO: 1, ActivityVersion: 1, Reason: PullHintReasonAppend}))
	require.NoError(t, client.Notify(context.Background(), 2, NotifyRequest{ChannelKey: key, ChannelID: id, Epoch: 1, LeaderEpoch: 2, Leader: 1, LeaderLEO: 1}))

	require.Len(t, server.pullReqs, 1)
	require.Len(t, server.ackReqs, 1)
	require.Len(t, server.pullHintReqs, 1)
	require.Len(t, server.notifyReqs, 1)
}

func TestLocalNetworkMissingServerReturnsChannelNotFound(t *testing.T) {
	network := NewLocalNetwork()
	_, err := network.Pull(context.Background(), 99, PullRequest{NextOffset: 1})
	require.ErrorIs(t, err, ch.ErrChannelNotFound)
	require.ErrorIs(t, network.Ack(context.Background(), 99, AckRequest{}), ch.ErrChannelNotFound)
	require.ErrorIs(t, network.PullHint(context.Background(), 99, PullHintRequest{}), ch.ErrChannelNotFound)
	require.ErrorIs(t, network.Notify(context.Background(), 99, NotifyRequest{}), ch.ErrChannelNotFound)
}

func TestLocalNetworkDropPullAndAckCounters(t *testing.T) {
	network := NewLocalNetwork()
	network.Register(1, &captureServer{})
	network.SetDropPull(1, true)
	network.SetDropAck(1, true)

	_, err := network.Pull(context.Background(), 1, PullRequest{NextOffset: 1})
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.ErrorIs(t, network.Ack(context.Background(), 1, AckRequest{}), ch.ErrNotReady)
	require.Equal(t, 1, network.DroppedPulls(1))
	require.Equal(t, 1, network.DroppedAcks(1))

	network.SetDropPull(1, false)
	network.SetDropAck(1, false)
	_, err = network.Pull(context.Background(), 1, PullRequest{NextOffset: 1})
	require.NoError(t, err)
	require.NoError(t, network.Ack(context.Background(), 1, AckRequest{}))
}

func TestLocalNetworkDropPullHintAndLegacyNotifyCounters(t *testing.T) {
	network := NewLocalNetwork()
	network.Register(1, &captureServer{})
	network.SetDropPullHint(1, true)
	require.ErrorIs(t, network.PullHint(context.Background(), 1, PullHintRequest{}), ch.ErrNotReady)
	require.ErrorIs(t, network.Notify(context.Background(), 1, NotifyRequest{}), ch.ErrNotReady)
	require.Equal(t, 2, network.DroppedPullHints(1))

	network.SetDropPullHint(1, false)
	network.SetDropNotify(1, true)
	require.ErrorIs(t, network.Notify(context.Background(), 1, NotifyRequest{}), ch.ErrNotReady)
	require.Equal(t, 3, network.DroppedPullHints(1))

	network.SetDropNotify(1, false)
	require.NoError(t, network.PullHint(context.Background(), 1, PullHintRequest{}))
	require.NoError(t, network.Notify(context.Background(), 1, NotifyRequest{}))
}

func TestLocalNetworkPullHintRoutesToServer(t *testing.T) {
	net := NewLocalNetwork()
	srv := &captureServer{}
	net.Register(2, srv)

	req := PullHintRequest{ChannelKey: ch.ChannelKey("1:a"), ChannelID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 2, Leader: 1, LeaderLEO: 7, ActivityVersion: 7, Reason: PullHintReasonAppend}
	require.NoError(t, net.PullHint(context.Background(), 2, req))
	require.Equal(t, req, srv.pullHint)
}

func TestLocalNetworkDropPullHint(t *testing.T) {
	net := NewLocalNetwork()
	net.Register(2, &captureServer{})
	net.SetDropPullHint(2, true)

	err := net.PullHint(context.Background(), 2, PullHintRequest{ChannelKey: ch.ChannelKey("1:a")})
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Equal(t, 1, net.DroppedPullHints(2))
}

func TestLocalNetworkLegacyDropNotifyFieldDropsNotify(t *testing.T) {
	net := NewLocalNetwork()
	net.Register(2, &captureServer{})
	net.DropNotify[2] = true

	err := net.Notify(context.Background(), 2, NotifyRequest{ChannelKey: ch.ChannelKey("1:a")})
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Equal(t, 1, net.DroppedPullHints(2))
}

func TestLocalNetworkLegacySetDropNotifyDropsNotify(t *testing.T) {
	net := NewLocalNetwork()
	srv := &captureServer{}
	net.Register(2, srv)
	net.SetDropNotify(2, true)

	err := net.Notify(context.Background(), 2, NotifyRequest{ChannelKey: ch.ChannelKey("1:a")})
	require.ErrorIs(t, err, ch.ErrNotReady)
	require.Equal(t, 1, net.DroppedPullHints(2))

	net.SetDropNotify(2, false)
	req := NotifyRequest{ChannelKey: ch.ChannelKey("1:a"), LeaderLEO: 4}
	require.NoError(t, net.Notify(context.Background(), 2, req))
	require.Equal(t, req, srv.notify)
}
