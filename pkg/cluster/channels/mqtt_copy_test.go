package channels

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/stretchr/testify/require"
)

type mqttCopyStore struct {
	channelstore.ChannelStore
	source         ch.MQTTSourceSnapshot
	found          bool
	loadErr        error
	closed, copies int
	prepare        func(context.Context, ch.MQTTReplayRange) (ch.MQTTReplayPage, error)
}

func (s *mqttCopyStore) LoadCommittedMQTTSource(context.Context, uint64) (ch.MQTTSourceSnapshot, bool, error) {
	return s.source, s.found, s.loadErr
}
func (s *mqttCopyStore) PrepareMQTTReplay(c context.Context, q ch.MQTTReplayRange) (ch.MQTTReplayPage, error) {
	s.copies++
	return s.prepare(c, q)
}
func (s *mqttCopyStore) Close() error { s.closed++; return nil }

// Any attempt to manufacture a follower checkpoint from a request must fail.
func (s *mqttCopyStore) StoreCheckpoint(context.Context, ch.Checkpoint) error {
	panic("receiver advanced HW")
}

type mqttCopyFactory struct {
	channelstore.Factory
	handle channelstore.ChannelStore
	opens  int
}

func (f *mqttCopyFactory) ChannelStore(ch.ChannelKey, ch.ChannelID) (channelstore.ChannelStore, error) {
	f.opens++
	return f.handle, nil
}

func mqttCopyFixture(t *testing.T) (*Service, *mqttFreshMeta, *mqttCopyStore, *mqttCopyFactory, mqttCopyRequest, ch.MQTTReplayPage) {
	t.Helper()
	s, m, _, q, p := mqttRoutedReplayFixture(t)
	q.Range.Through = p.After.Through
	q.Range.Limit = len(p.Records)
	q.Range.MaxBytes = int(p.After.TotalStoredBytes - p.Before.TotalStoredBytes)
	request := mqttCopyRequest{Target: 2, Leader: 2, Request: q, Authority: mqttCopyAuthority(m.meta), Before: p.Before, After: p.After}
	st := &mqttCopyStore{found: true, source: ch.MQTTSourceSnapshot{Generation: p.Before.Generation, StartAfter: p.Before.StartAfter, CommittedThrough: p.After.Through}, prepare: func(context.Context, ch.MQTTReplayRange) (ch.MQTTReplayPage, error) { return p, nil }}
	f := &mqttCopyFactory{handle: st}
	s.store = f
	return s, m, st, f, request, p
}

func TestMQTTCopyReceiverProofsAndCleanup(t *testing.T) {
	for _, mode := range []string{"ok", "lag", "absent", "generation", "start", "content", "before", "empty", "source_error", "copy_error", "panic", "route_before", "membership_before", "leader", "target", "learner", "weak_quorum", "route_after", "membership_after", "cancel_after", "saturated"} {
		t.Run(mode, func(t *testing.T) {
			s, m, st, f, q, p := mqttCopyFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "lag":
				st.source.CommittedThrough = 0
			case "absent":
				st.found = false
			case "generation":
				st.source.Generation = "wrong"
			case "start":
				st.source.StartAfter++
			case "content":
				q.After.Digest[1]++
			case "before":
				q.Before.StartAfter++
			case "empty":
				st.prepare = func(context.Context, ch.MQTTReplayRange) (ch.MQTTReplayPage, error) { return ch.MQTTReplayPage{}, nil }
			case "source_error":
				st.loadErr = ch.ErrNotReady
			case "copy_error":
				st.prepare = func(context.Context, ch.MQTTReplayRange) (ch.MQTTReplayPage, error) {
					return ch.MQTTReplayPage{}, ch.ErrLogConflict
				}
			case "panic":
				st.prepare = func(context.Context, ch.MQTTReplayRange) (ch.MQTTReplayPage, error) { panic("copy") }
			case "route_before":
				m.meta.RouteGeneration++
			case "membership_before":
				m.meta.Replicas = append(m.meta.Replicas, 4)
			case "leader":
				q.Leader = 3
			case "target":
				q.Target = 3
			case "learner":
				m.meta.ISR = []ch.NodeID{1, 3}
				m.meta.Leader = 3
				q.Leader = 3
				q.Authority = mqttCopyAuthority(m.meta)
			case "weak_quorum":
				m.meta.MinISR = 1
				q.Authority = mqttCopyAuthority(m.meta)
			case "route_after", "membership_after", "cancel_after":
				m.after = func(n int) {
					if n != 2 {
						return
					}
					switch mode {
					case "route_after":
						m.meta.RouteGeneration++
					case "membership_after":
						m.meta.ISR = []ch.NodeID{2, 3}
					case "cancel_after":
						cancel()
					}
				}
			case "saturated":
				for range cap(s.mqttCopyReceivers) {
					s.mqttCopyReceivers <- struct{}{}
				}
			}
			if mode == "panic" {
				require.Panics(t, func() { _, _ = s.confirmMQTTReplayCopy(ctx, q) })
				require.Equal(t, 1, st.closed)
				require.Empty(t, s.mqttCopyReceivers)
				return
			}
			ack, err := s.confirmMQTTReplayCopy(ctx, q)
			if mode == "ok" {
				require.NoError(t, err)
				require.Equal(t, ch.NodeID(2), ack)
				require.Equal(t, 2, m.calls)
				require.Equal(t, p.After, q.After)
			} else {
				require.Error(t, err)
				require.Zero(t, ack)
			}
			require.Equal(t, f.opens, st.closed)
			if mode == "lag" || mode == "absent" || mode == "source_error" || mode == "generation" || mode == "start" {
				require.Zero(t, st.copies)
			}
			if mode == "saturated" || mode == "target" || mode == "learner" || mode == "route_before" || mode == "membership_before" || mode == "weak_quorum" {
				require.Zero(t, f.opens)
			}
		})
	}
}

func TestMQTTCopyReceiverContinuesPartialCoverage(t *testing.T) {
	s, _, st, _, q, p := mqttCopyFixture(t)
	second := p
	second.Before = p.After
	second.Records = append([]ch.MQTTReplayRecord(nil), p.Records...)
	second.Records[0].Position = 2
	second.Records[0].TotalBytes *= 2
	second.Records[0].TotalStoredBytes *= 2
	second.Records[0].Digest = [32]byte{3}
	second.After.Through = 2
	second.After.TotalBytes *= 2
	second.After.TotalStoredBytes *= 2
	second.After.Digest = [32]byte{3}
	q.After = second.After
	q.Request.Range.Through = 2
	q.Request.Range.Limit = 2
	q.Request.Range.MaxBytes *= 2
	st.source.CommittedThrough = 2
	st.prepare = func(_ context.Context, r ch.MQTTReplayRange) (ch.MQTTReplayPage, error) {
		if r.From == 1 {
			return p, nil
		}
		require.Equal(t, uint64(2), r.From)
		require.Equal(t, 1, r.Limit)
		require.Equal(t, len(p.Records[0].Content), r.MaxBytes)
		return second, nil
	}
	ack, err := s.confirmMQTTReplayCopy(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, ch.NodeID(2), ack)
	require.Equal(t, 2, st.copies)
	require.Equal(t, 1, st.closed)
}

type mqttCopyForward struct {
	ForwardClient
	prepare func(context.Context, ch.NodeID, ch.MQTTReplayRequest) (ch.MQTTReplayPage, error)
	confirm func(context.Context, ch.NodeID, mqttCopyRequest) (ch.NodeID, error)
}

func (f mqttCopyForward) ForwardMQTTReplay(c context.Context, n ch.NodeID, q ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) {
	return f.prepare(c, n, q)
}
func (f mqttCopyForward) ConfirmMQTTReplayCopy(c context.Context, n ch.NodeID, q mqttCopyRequest) (ch.NodeID, error) {
	return f.confirm(c, n, q)
}

func TestMQTTCopyRequiresLeaderAndDistinctCurrentQuorum(t *testing.T) {
	for _, mode := range []string{"ok", "unavailable_peer", "leader_missing", "foreign_ack", "no_quorum", "weak_quorum", "duplicate", "route_after", "membership_after", "saturated", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, m, _, _, _, p := mqttCopyFixture(t)
			s.localNode = 4
			q := ch.MQTTReplayRequest{ChannelID: m.meta.ID, ExpectedChannelEpoch: 2, ExpectedLeaderEpoch: 3, ExpectedRouteGeneration: 4, Range: ch.MQTTReplayRange{Generation: p.Before.Generation, From: 1, Through: 4, Limit: 256, MaxBytes: 1 << 20}}
			var active atomic.Int32
			s.forward = mqttCopyForward{prepare: func(context.Context, ch.NodeID, ch.MQTTReplayRequest) (ch.MQTTReplayPage, error) { return p, nil }, confirm: func(c context.Context, n ch.NodeID, r mqttCopyRequest) (ch.NodeID, error) {
				active.Add(1)
				defer active.Add(-1)
				if r.Target != n || r.After != p.After || r.Before != p.Before {
					return 0, ch.ErrLogConflict
				}
				switch mode {
				case "unavailable_peer":
					if n == 3 {
						<-c.Done()
						return 0, c.Err()
					}
				case "leader_missing":
					if n == 2 {
						return 0, ch.ErrNotReady
					}
				case "foreign_ack":
					if n != 2 {
						return 2, nil
					}
				case "no_quorum":
					if n != 2 {
						return 0, ch.ErrNotReady
					}
				}
				return n, nil
			}}
			switch mode {
			case "weak_quorum":
				m.meta.MinISR = 1
			case "duplicate":
				m.meta.ISR = []ch.NodeID{2, 2, 3}
			case "route_after", "membership_after":
				m.after = func(n int) {
					if n != 4 {
						return
					}
					if mode == "route_after" {
						m.meta.RouteGeneration++
					} else {
						m.meta.ISR = []ch.NodeID{2, 3}
					}
				}
			case "saturated":
				for range cap(s.mqttCopyCoordinators) {
					s.mqttCopyCoordinators <- struct{}{}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			receipt, err := s.CopyMQTTReplay(ctx, q)
			if mode == "ok" || mode == "unavailable_peer" {
				require.NoError(t, err)
				require.Equal(t, p.After, receipt.After)
				require.Equal(t, p.Before, receipt.Before)
				require.Contains(t, receipt.Copies, ch.NodeID(2))
				require.GreaterOrEqual(t, len(receipt.Copies), 2)
				require.Equal(t, mqttCopyAuthority(m.meta), receipt.Authority)
				require.Equal(t, 2, receipt.WriteQuorum)
			} else {
				require.Error(t, err)
				require.Zero(t, receipt)
			}
			require.Zero(t, active.Load(), "all workers must be joined")
		})
	}
}

func TestMQTTCopyRPCClosedEchoAndGateway(t *testing.T) {
	s, _, _, _, q, _ := mqttCopyFixture(t)
	wire, err := encodeMQTTCopyRequest(q)
	require.NoError(t, err)
	decoded, err := decodeMQTTCopyRequest(wire)
	require.NoError(t, err)
	require.Equal(t, q, decoded)
	for i := 0; i < len(wire); i++ {
		_, err = decodeMQTTCopyRequest(wire[:i])
		require.Error(t, err)
	}
	badVersion := bytes.Clone(wire)
	badVersion[4]++
	for _, bad := range [][]byte{badVersion, append(bytes.Clone(wire), 0), make([]byte, mqttCopyRPCMaxBytes+1)} {
		_, err = decodeMQTTCopyRequest(bad)
		require.Error(t, err)
	}
	reply, err := encodeMQTTCopyReply(q, 2, nil)
	require.NoError(t, err)
	ack, err := decodeMQTTCopyReply(reply, q)
	require.NoError(t, err)
	require.Equal(t, ch.NodeID(2), ack)
	for i := 0; i < len(reply); i++ {
		_, err = decodeMQTTCopyReply(reply[:i], q)
		require.Error(t, err)
	}
	for _, change := range []func(*mqttCopyRequest){func(r *mqttCopyRequest) { r.Target++ }, func(r *mqttCopyRequest) { r.Leader++ }, func(r *mqttCopyRequest) { r.Authority[0]++ }, func(r *mqttCopyRequest) { r.After.Digest[0]++ }, func(r *mqttCopyRequest) { r.Request.ExpectedLeaderEpoch++ }} {
		other := q
		change(&other)
		_, err = decodeMQTTCopyReply(reply, other)
		require.Error(t, err)
	}
	_, err = decodeMQTTCopyReply(append(bytes.Clone(reply), 0), q)
	require.Error(t, err)
	_, err = encodeMQTTCopyReply(q, 3, nil)
	require.Error(t, err)
	for _, failure := range []error{ch.ErrNotReady, ch.ErrStaleMeta, context.Canceled, errors.New("remote secret")} {
		b, e := encodeMQTTCopyReply(q, 0, failure)
		require.NoError(t, e)
		_, e = decodeMQTTCopyReply(b, q)
		require.Error(t, e)
		require.NotContains(t, e.Error(), "remote secret")
		_, e = decodeMQTTCopyReply(append(b, 0), q)
		require.Error(t, e)
	}
	network := clusternet.NewLocalNetwork()
	gateway := NewServiceGateway(s)
	RegisterServiceHandlersOn(localNetworkRegistrar{network: network, nodeID: 2}, gateway)
	client := NewTransportClient(network)
	ack, err = client.ConfirmMQTTReplayCopy(context.Background(), 2, q)
	require.NoError(t, err)
	require.Equal(t, ch.NodeID(2), ack)
	gateway.Clear()
	_, err = client.ConfirmMQTTReplayCopy(context.Background(), 2, q)
	require.Error(t, err)
	gateway.Replace(s)
	_, err = client.ConfirmMQTTReplayCopy(context.Background(), 2, q)
	require.NoError(t, err)
}
