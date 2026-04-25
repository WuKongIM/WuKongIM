package transport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
)

type sessionManager struct {
	adapter *Transport

	mu       sync.Mutex
	sessions map[channel.NodeID]*peerSession
}

type peerSession struct {
	adapter *Transport
	peer    channel.NodeID

	mu              sync.Mutex
	pendingRequests int
	pendingBytes    int64
	closed          bool
}

var _ runtime.PeerSessionManager = (*sessionManager)(nil)
var _ runtime.PeerSession = (*peerSession)(nil)

func newSessionManager(adapter *Transport) *sessionManager {
	return &sessionManager{
		adapter:  adapter,
		sessions: make(map[channel.NodeID]*peerSession),
	}
}

func (m *sessionManager) Session(peer channel.NodeID) runtime.PeerSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[peer]; ok {
		return session
	}
	session := &peerSession{adapter: m.adapter, peer: peer}
	m.sessions[peer] = session
	return session
}

func (s *peerSession) Send(env runtime.Envelope) error {
	switch env.Kind {
	case runtime.MessageKindFetchRequest:
		return s.sendFetchRequest(env)
	case runtime.MessageKindLanePollRequest:
		return s.sendLongPollRequest(env)
	case runtime.MessageKindReconcileProbeRequest:
		return s.sendReconcileProbe(env)
	default:
		return fmt.Errorf("channeltransport: unsupported envelope kind %d", env.Kind)
	}
}

func (s *peerSession) sendFetchRequest(env runtime.Envelope) error {
	if env.FetchRequest == nil {
		return fmt.Errorf("channeltransport: missing fetch request payload")
	}

	body, err := encodeFetchRequest(*env.FetchRequest)
	if err != nil {
		return err
	}
	pendingBytes := int64(len(body))
	s.trackPending(pendingBytes)
	pendingReleased := false
	defer func() {
		if !pendingReleased {
			s.releasePending(pendingBytes)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), s.adapter.rpcTimeout)
	defer cancel()

	respBody, err := s.adapter.client.RPCService(ctx, uint64(s.peer), fetchRPCShardKey(env.ChannelKey), RPCServiceFetch, body)
	if err != nil {
		return err
	}
	resp, err := decodeFetchResponse(respBody)
	if err != nil {
		return err
	}
	s.releasePending(pendingBytes)
	pendingReleased = true

	s.deliverFetchResponse(env.RequestID, resp)
	return nil
}

func (s *peerSession) sendLongPollRequest(env runtime.Envelope) error {
	if env.LanePollRequest == nil {
		return fmt.Errorf("channeltransport: missing lane poll request payload")
	}

	req := LongPollFetchRequest{
		PeerID:                s.adapter.localNode,
		LaneID:                env.LanePollRequest.LaneID,
		LaneCount:             env.LanePollRequest.LaneCount,
		SessionID:             env.LanePollRequest.SessionID,
		SessionEpoch:          env.LanePollRequest.SessionEpoch,
		Op:                    LanePollOp(env.LanePollRequest.Op),
		ProtocolVersion:       env.LanePollRequest.ProtocolVersion,
		MaxWaitMs:             uint32(env.LanePollRequest.MaxWait / time.Millisecond),
		MaxBytes:              uint32(env.LanePollRequest.MaxBytes),
		MaxChannels:           uint32(env.LanePollRequest.MaxChannels),
		MembershipVersionHint: env.LanePollRequest.MembershipVersionHint,
		Capabilities:          LongPollCapabilityQuorumAck | LongPollCapabilityLocalAck,
		FullMembership:        toTransportLaneMembership(env.LanePollRequest.FullMembership),
		CursorDelta:           toTransportLaneCursorDelta(env.LanePollRequest.CursorDelta),
	}
	startedAt := time.Now()
	longPollWait := time.Duration(req.MaxWaitMs) * time.Millisecond
	rpcDeadline := s.adapter.rpcTimeout
	if longPollWait+s.adapter.rpcTimeout > rpcDeadline {
		rpcDeadline = longPollWait + s.adapter.rpcTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), rpcDeadline)
	defer cancel()

	resp, err := s.adapter.LongPollFetch(ctx, s.peer, req)
	finishedAt := time.Now()
	sendtrace.Record(sendtrace.Event{
		Stage:      sendtrace.StageRuntimeLanePollRequestSend,
		At:         startedAt,
		Duration:   sendtrace.Elapsed(startedAt, finishedAt),
		NodeID:     uint64(s.adapter.localNode),
		PeerNodeID: uint64(s.peer),
		ChannelKey: string(env.ChannelKey),
	})
	if len(req.CursorDelta) > 0 {
		channelKey := ""
		if len(req.CursorDelta) > 0 {
			channelKey = string(req.CursorDelta[0].ChannelKey)
		}
		sendtrace.Record(sendtrace.Event{
			Stage:      sendtrace.StageRuntimeLaneCursorDeltaSend,
			At:         startedAt,
			Duration:   sendtrace.Elapsed(startedAt, finishedAt),
			NodeID:     uint64(s.adapter.localNode),
			PeerNodeID: uint64(s.peer),
			ChannelKey: channelKey,
		})
	}
	if err != nil {
		return err
	}
	s.adapter.deliver(runtime.Envelope{
		Peer: s.peer,
		Kind: runtime.MessageKindLanePollResponse,
		Sync: true,
		LanePollResponse: &runtime.LanePollResponseEnvelope{
			LaneID:        req.LaneID,
			Status:        runtime.LanePollStatus(resp.Status),
			SessionID:     resp.SessionID,
			SessionEpoch:  resp.SessionEpoch,
			TimedOut:      resp.TimedOut,
			MoreReady:     resp.MoreReady,
			ResetRequired: resp.ResetRequired,
			ResetReason:   runtime.LanePollResetReason(resp.ResetReason),
			Items:         toRuntimeLaneResponseItems(resp.Items),
		},
	})
	return nil
}

func (s *peerSession) sendReconcileProbe(env runtime.Envelope) error {
	if env.ReconcileProbeRequest == nil {
		return fmt.Errorf("channeltransport: missing reconcile probe payload")
	}

	body, err := encodeReconcileProbeRequest(*env.ReconcileProbeRequest)
	if err != nil {
		return err
	}
	pendingBytes := int64(len(body))
	s.trackPending(pendingBytes)
	pendingReleased := false
	defer func() {
		if !pendingReleased {
			s.releasePending(pendingBytes)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), s.adapter.rpcTimeout)
	defer cancel()

	respBody, err := s.adapter.client.RPCService(ctx, uint64(s.peer), fetchRPCShardKey(env.ChannelKey), RPCServiceReconcileProbe, body)
	if err != nil {
		return err
	}
	resp, err := decodeReconcileProbeResponse(respBody)
	if err != nil {
		return err
	}
	s.releasePending(pendingBytes)
	pendingReleased = true
	s.deliverReconcileProbeResponse(env.RequestID, resp)
	return nil
}

func toTransportLaneMembership(items []runtime.LaneMembership) []LongPollMembership {
	if len(items) == 0 {
		return nil
	}
	out := make([]LongPollMembership, 0, len(items))
	for _, item := range items {
		out = append(out, LongPollMembership{
			ChannelKey:   item.ChannelKey,
			ChannelEpoch: item.ChannelEpoch,
		})
	}
	return out
}

func toTransportLaneCursorDelta(items []runtime.LaneCursorDelta) []LongPollCursorDelta {
	if len(items) == 0 {
		return nil
	}
	out := make([]LongPollCursorDelta, 0, len(items))
	for _, item := range items {
		out = append(out, LongPollCursorDelta{
			ChannelKey:   item.ChannelKey,
			ChannelEpoch: item.ChannelEpoch,
			MatchOffset:  item.MatchOffset,
			OffsetEpoch:  item.OffsetEpoch,
		})
	}
	return out
}

func toRuntimeLaneResponseItems(items []LongPollItem) []runtime.LaneResponseItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]runtime.LaneResponseItem, 0, len(items))
	for _, item := range items {
		out = append(out, runtime.LaneResponseItem{
			ChannelKey:   item.ChannelKey,
			ChannelEpoch: item.ChannelEpoch,
			LeaderEpoch:  item.LeaderEpoch,
			Flags:        runtime.LanePollItemFlags(item.Flags),
			Records:      item.Records,
			LeaderHW:     item.LeaderHW,
			TruncateTo:   item.TruncateTo,
		})
	}
	return out
}

func (s *peerSession) deliverFetchResponse(requestID uint64, resp runtime.FetchResponseEnvelope) {
	s.adapter.deliver(runtime.Envelope{
		Peer:          s.peer,
		ChannelKey:    resp.ChannelKey,
		Epoch:         resp.Epoch,
		Generation:    resp.Generation,
		RequestID:     requestID,
		Kind:          runtime.MessageKindFetchResponse,
		Sync:          true,
		FetchResponse: &resp,
	})
}

func (s *peerSession) deliverFetchFailure(env runtime.Envelope, err error) {
	failed := runtime.Envelope{
		Peer:       s.peer,
		ChannelKey: env.ChannelKey,
		Epoch:      env.Epoch,
		Generation: env.Generation,
		RequestID:  env.RequestID,
		Kind:       runtime.MessageKindFetchFailure,
		Sync:       true,
	}
	if err != nil {
		failed.Payload = []byte(err.Error())
	}
	s.adapter.deliver(failed)
}

func (s *peerSession) deliverReconcileProbeResponse(requestID uint64, resp runtime.ReconcileProbeResponseEnvelope) {
	s.adapter.deliver(runtime.Envelope{
		Peer:                   s.peer,
		ChannelKey:             resp.ChannelKey,
		Epoch:                  resp.Epoch,
		Generation:             resp.Generation,
		RequestID:              requestID,
		Kind:                   runtime.MessageKindReconcileProbeResponse,
		Sync:                   true,
		ReconcileProbeResponse: &resp,
	})
}

func (s *peerSession) Backpressure() runtime.BackpressureState {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := runtime.BackpressureState{
		PendingRequests: s.pendingRequests,
		PendingBytes:    s.pendingBytes,
	}
	if s.adapter.maxPending > 0 && s.pendingRequests >= s.adapter.maxPending {
		state.Level = runtime.BackpressureHard
	}
	return state
}

func (s *peerSession) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *peerSession) trackPending(bytes int64) {
	s.mu.Lock()
	s.pendingRequests++
	s.pendingBytes += bytes
	s.mu.Unlock()
}

func (s *peerSession) releasePending(bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pendingRequests > 0 {
		s.pendingRequests--
	}
	s.pendingBytes -= bytes
	if s.pendingBytes < 0 {
		s.pendingBytes = 0
	}
}

func fetchRPCShardKey(channelKey channel.ChannelKey) uint64 {
	const (
		fnvOffset64 = 14695981039346656037
		fnvPrime64  = 1099511628211
	)

	hash := uint64(fnvOffset64)
	for i := 0; i < len(channelKey); i++ {
		hash ^= uint64(channelKey[i])
		hash *= fnvPrime64
	}
	return hash
}
