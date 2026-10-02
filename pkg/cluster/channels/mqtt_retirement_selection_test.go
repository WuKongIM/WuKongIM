package channels

import (
	"bytes"
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

type mqttSelectionStore struct {
	mqttCopyStore
	selectAnchor func(context.Context, ch.MQTTReplayRetirementScan) (ch.MQTTReplayRetirementSelection, error)
}

func (s *mqttSelectionStore) SelectMQTTReplayRetirementAnchor(ctx context.Context, q ch.MQTTReplayRetirementScan) (ch.MQTTReplayRetirementSelection, error) {
	return s.selectAnchor(ctx, q)
}

func mqttRoutedSelectionFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttSelectionStore, *mqttCopyFactory, ch.MQTTReplayRetirementSelectionRequest, ch.MQTTReplayRetirementSelection) {
	s, m, _, retire, _ := mqttRoutedRetirementFixture(t)
	q := mqttRetirementForward(retire).Selection
	p := ch.MQTTReplayRetirementSelection{Captured: q.Captured, Candidate: q.Captured, HasCandidate: true, Done: true}
	st := &mqttSelectionStore{selectAnchor: func(ctx context.Context, scan ch.MQTTReplayRetirementScan) (ch.MQTTReplayRetirementSelection, error) {
		require.Equal(t, q.Scan(), scan)
		_, bounded := ctx.Deadline()
		require.True(t, bounded)
		return p, nil
	}}
	f := &mqttCopyFactory{handle: st}
	s.store = f
	return s, m, st, f, q, p
}

func TestMQTTRetirementSelectionRoutingFencesBoundsAndClosesLease(t *testing.T) {
	for _, mode := range []string{"success", "stable_fence", "renewed_fence", "cleared_fence", "route_before", "route_after", "members_after", "status_after", "cancel", "read_error", "wrong_capture", "upward", "panic", "unsupported", "saturated"} {
		t.Run(mode, func(t *testing.T) {
			s, m, st, f, q, want := mqttRoutedSelectionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "stable_fence" || mode == "renewed_fence" || mode == "cleared_fence" {
				m.meta.WriteFence = ch.WriteFence{Token: "moving", Version: 1, Until: time.Unix(100, 0)}
			}
			m.after = func(n int) {
				switch mode {
				case "route_before", "route_after":
					if n == 2 || mode == "route_before" {
						m.meta.RouteGeneration++
					}
				case "members_after":
					if n == 2 {
						m.meta.Replicas = append(m.meta.Replicas, 4)
					}
				case "status_after":
					if n == 2 {
						m.meta.Status = ch.StatusCreating
					}
				case "renewed_fence":
					if n == 2 {
						m.meta.WriteFence.Until = m.meta.WriteFence.Until.Add(time.Second)
					}
				case "cleared_fence":
					if n == 2 {
						m.meta.WriteFence = ch.WriteFence{}
					}
				case "cancel":
					if n == 2 {
						cancel()
					}
				}
			}
			orig := st.selectAnchor
			st.selectAnchor = func(c context.Context, scan ch.MQTTReplayRetirementScan) (ch.MQTTReplayRetirementSelection, error) {
				if mode == "panic" {
					panic("selection")
				}
				p, e := orig(c, scan)
				switch mode {
				case "read_error":
					e = ch.ErrNotReady
				case "wrong_capture":
					p.Captured.Manifest.Digest[0]++
				case "upward":
					p.Candidate.Anchor.Through++
				}
				return p, e
			}
			if mode == "unsupported" {
				f.handle = &mqttCopyStore{}
			}
			if mode == "saturated" {
				for i := 0; i < cap(s.mqttRepairDonors); i++ {
					s.mqttRepairDonors <- struct{}{}
				}
			}
			if mode == "panic" {
				require.Panics(t, func() { _, _ = s.SelectMQTTReplayRetirement(ctx, q) })
				require.Equal(t, 1, st.closed)
				require.Empty(t, s.mqttRepairDonors)
				return
			}
			p, e := s.SelectMQTTReplayRetirement(ctx, q)
			if mode == "success" || mode == "stable_fence" {
				require.NoError(t, e)
				require.Equal(t, want, p)
				require.Equal(t, 2, m.calls)
			} else {
				require.Error(t, e)
				require.Zero(t, p)
			}
			if mode == "route_before" || mode == "saturated" {
				require.Zero(t, f.opens)
			} else if mode != "unsupported" {
				require.Equal(t, 1, st.closed)
			}
			if mode != "saturated" {
				require.Empty(t, s.mqttRepairDonors)
			}
		})
	}
}

func TestMQTTRetirementSelectionRPCAndGatewayKeepClosedContinuation(t *testing.T) {
	s, m, st, _, q, selected := mqttRoutedSelectionFixture(t)
	req := mqttRetirementSelectionForwardRequest{Leader: 2, Request: q}
	body, err := encodeMQTTRetirementSelectionRequest(req)
	require.NoError(t, err)
	decoded, err := decodeMQTTRetirementSelectionRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, decoded)
	for i := 0; i < len(body); i++ {
		_, e := decodeMQTTRetirementSelectionRequest(body[:i])
		require.Error(t, e)
	}
	for _, bad := range [][]byte{append(bytes.Clone(body), 0), make([]byte, mqttRetirementRPCMaxBytes+1)} {
		_, e := decodeMQTTRetirementSelectionRequest(bad)
		require.Error(t, e)
	}
	for _, out := range []ch.MQTTReplayRetirementSelection{selected, {Captured: q.Captured, Done: true}, {Captured: q.Captured, BeforeAnchor: q.Captured.Manifest.LastOffset}} {
		reply, e := encodeMQTTRetirementSelectionReply(req, out, nil)
		require.NoError(t, e)
		got, e := decodeMQTTRetirementSelectionReply(reply, req)
		require.NoError(t, e)
		require.Equal(t, out, got)
		for i := 0; i < len(reply); i++ {
			_, e := decodeMQTTRetirementSelectionReply(reply[:i], req)
			require.Error(t, e)
		}
		changed := req
		changed.Request.Through--
		_, e = decodeMQTTRetirementSelectionReply(reply, changed)
		require.Error(t, e)
		_, e = decodeMQTTRetirementSelectionReply(append(bytes.Clone(reply), 0), req)
		require.Error(t, e)
	}
	for _, e := range mqttSourceStatuses[1:] {
		b, err := encodeMQTTRetirementSelectionReply(req, ch.MQTTReplayRetirementSelection{}, e)
		require.NoError(t, err)
		p, err := decodeMQTTRetirementSelectionReply(b, req)
		require.ErrorIs(t, err, e)
		require.Zero(t, p)
	}
	network := clusternet.NewLocalNetwork()
	gateway := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, gateway)
	origin, err := NewService(Config{LocalNode: 1, MetaSource: m, Runtime: &fakeRuntime{}, Forward: NewTransportClient(network)})
	require.NoError(t, err)
	p, err := origin.SelectMQTTReplayRetirement(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, selected, p)
	require.Equal(t, 1, st.closed)
	gateway.Clear()
	_, err = origin.SelectMQTTReplayRetirement(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotReady)
	other, err := NewService(Config{LocalNode: 3, MetaSource: m, Runtime: &fakeRuntime{}})
	require.NoError(t, err)
	gateway.Replace(other)
	_, err = origin.SelectMQTTReplayRetirement(context.Background(), q)
	require.ErrorIs(t, err, ch.ErrNotLeader)
}
