package transport

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

type captureServer struct {
	pullHint PullHintRequest
	notify   NotifyRequest
}

func (s *captureServer) HandlePull(ctx context.Context, req PullRequest) (PullResponse, error) {
	return PullResponse{}, nil
}

func (s *captureServer) HandleAck(ctx context.Context, req AckRequest) error {
	return nil
}

func (s *captureServer) HandleNotify(ctx context.Context, req NotifyRequest) error {
	s.notify = req
	return nil
}

func (s *captureServer) HandlePullHint(ctx context.Context, req PullHintRequest) error {
	s.pullHint = req
	return nil
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
