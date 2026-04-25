package transport

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/runtime"
	wktransport "github.com/WuKongIM/WuKongIM/pkg/transport"
)

const defaultRPCTimeout = 5 * time.Second

type Options struct {
	LocalNode           channel.NodeID
	Client              *wktransport.Client
	RPCMux              *wktransport.RPCMux
	FetchService        runtime.FetchService
	LongPollService     LongPollService
	RPCTimeout          time.Duration
	MaxPendingFetchRPC  int
	LongPollLaneCount   int
	LongPollMaxWait     time.Duration
	LongPollMaxBytes    int
	LongPollMaxChannels int
}

type Transport struct {
	localNode           channel.NodeID
	client              *wktransport.Client
	rpcMux              *wktransport.RPCMux
	rpcTimeout          time.Duration
	maxPending          int
	longPollLaneCount   int
	longPollMaxWait     time.Duration
	longPollMaxBytes    int
	longPollMaxChannels int

	mu               sync.RWMutex
	handler          func(runtime.Envelope)
	fetchService     runtime.FetchService
	longPollService  LongPollService
	reconcileService runtime.ReconcileProbeService
	closeOnce        sync.Once

	sessions *sessionManager
}

var _ runtime.Transport = (*Transport)(nil)
var _ runtime.PeerSessionManager = (*Transport)(nil)

func New(opts Options) (*Transport, error) {
	if opts.LocalNode == 0 {
		return nil, fmt.Errorf("channeltransport: local node must be set")
	}
	if opts.Client == nil {
		return nil, fmt.Errorf("channeltransport: client must be set")
	}
	if opts.RPCMux == nil {
		return nil, fmt.Errorf("channeltransport: rpc mux must be set")
	}
	if opts.RPCTimeout <= 0 {
		opts.RPCTimeout = defaultRPCTimeout
	}
	if opts.MaxPendingFetchRPC <= 0 {
		opts.MaxPendingFetchRPC = 1
	}

	transport := &Transport{
		localNode:           opts.LocalNode,
		client:              opts.Client,
		rpcMux:              opts.RPCMux,
		rpcTimeout:          opts.RPCTimeout,
		maxPending:          opts.MaxPendingFetchRPC,
		longPollLaneCount:   opts.LongPollLaneCount,
		longPollMaxWait:     opts.LongPollMaxWait,
		longPollMaxBytes:    opts.LongPollMaxBytes,
		longPollMaxChannels: opts.LongPollMaxChannels,
		fetchService:        opts.FetchService,
		longPollService:     opts.LongPollService,
	}
	if lp, ok := opts.FetchService.(LongPollService); ok {
		transport.longPollService = lp
	} else if lp, ok := opts.FetchService.(runtime.LanePollService); ok {
		transport.longPollService = runtimeLanePollServiceAdapter{service: lp}
	}
	if service, ok := opts.FetchService.(runtime.ReconcileProbeService); ok {
		transport.reconcileService = service
	}
	transport.sessions = newSessionManager(transport)
	opts.RPCMux.Handle(RPCServiceFetch, transport.handleRPC)
	opts.RPCMux.Handle(RPCServiceLongPollFetch, transport.handleLongPollFetchRPC)
	opts.RPCMux.Handle(RPCServiceReconcileProbe, transport.handleReconcileProbeRPC)
	return transport, nil
}

type runtimeLanePollServiceAdapter struct {
	service runtime.LanePollService
}

func (a runtimeLanePollServiceAdapter) ServeLongPollFetch(ctx context.Context, req LongPollFetchRequest) (LongPollFetchResponse, error) {
	resp, err := a.service.ServeLanePoll(ctx, runtime.LanePollRequestEnvelope{
		ReplicaID:             req.PeerID,
		LaneID:                req.LaneID,
		LaneCount:             req.LaneCount,
		SessionID:             req.SessionID,
		SessionEpoch:          req.SessionEpoch,
		Op:                    runtime.LanePollOp(req.Op),
		ProtocolVersion:       req.ProtocolVersion,
		MaxWait:               time.Duration(req.MaxWaitMs) * time.Millisecond,
		MaxBytes:              int(req.MaxBytes),
		MaxChannels:           int(req.MaxChannels),
		MembershipVersionHint: req.MembershipVersionHint,
		FullMembership:        toRuntimeLaneMembership(req.FullMembership),
		CursorDelta:           toRuntimeLaneCursorDelta(req.CursorDelta),
	})
	if err != nil {
		return LongPollFetchResponse{}, err
	}
	return LongPollFetchResponse{
		Status:        LanePollStatus(resp.Status),
		SessionID:     resp.SessionID,
		SessionEpoch:  resp.SessionEpoch,
		TimedOut:      resp.TimedOut,
		MoreReady:     resp.MoreReady,
		ResetRequired: resp.ResetRequired,
		ResetReason:   LongPollResetReason(resp.ResetReason),
		Items:         toTransportLongPollItems(resp.Items),
	}, nil
}

func toRuntimeLaneMembership(items []LongPollMembership) []runtime.LaneMembership {
	if len(items) == 0 {
		return nil
	}
	out := make([]runtime.LaneMembership, 0, len(items))
	for _, item := range items {
		out = append(out, runtime.LaneMembership{
			ChannelKey:   item.ChannelKey,
			ChannelEpoch: item.ChannelEpoch,
		})
	}
	return out
}

func toRuntimeLaneCursorDelta(items []LongPollCursorDelta) []runtime.LaneCursorDelta {
	if len(items) == 0 {
		return nil
	}
	out := make([]runtime.LaneCursorDelta, 0, len(items))
	for _, item := range items {
		out = append(out, runtime.LaneCursorDelta{
			ChannelKey:   item.ChannelKey,
			ChannelEpoch: item.ChannelEpoch,
			MatchOffset:  item.MatchOffset,
			OffsetEpoch:  item.OffsetEpoch,
		})
	}
	return out
}

func toTransportLongPollItems(items []runtime.LaneResponseItem) []LongPollItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]LongPollItem, 0, len(items))
	for _, item := range items {
		out = append(out, LongPollItem{
			ChannelKey:   item.ChannelKey,
			ChannelEpoch: item.ChannelEpoch,
			LeaderEpoch:  item.LeaderEpoch,
			Flags:        LongPollItemFlags(item.Flags),
			Records:      item.Records,
			LeaderHW:     item.LeaderHW,
			TruncateTo:   item.TruncateTo,
		})
	}
	return out
}

func (t *Transport) Close() error {
	t.closeOnce.Do(func() {
		if t.rpcMux == nil {
			return
		}
		t.rpcMux.Unhandle(RPCServiceFetch)
		t.rpcMux.Unhandle(RPCServiceLongPollFetch)
		t.rpcMux.Unhandle(RPCServiceReconcileProbe)
	})
	return nil
}

func (t *Transport) BindFetchService(service runtime.FetchService) {
	t.mu.Lock()
	t.fetchService = service
	if probeService, ok := service.(runtime.ReconcileProbeService); ok {
		t.reconcileService = probeService
	} else {
		t.reconcileService = nil
	}
	if longPollService, ok := service.(LongPollService); ok {
		t.longPollService = longPollService
	} else if lanePollService, ok := service.(runtime.LanePollService); ok {
		t.longPollService = runtimeLanePollServiceAdapter{service: lanePollService}
	} else {
		t.longPollService = nil
	}
	t.mu.Unlock()
}

func (t *Transport) BindLongPollService(service LongPollService) {
	t.mu.Lock()
	t.longPollService = service
	t.mu.Unlock()
}

func (t *Transport) RegisterHandler(fn func(runtime.Envelope)) {
	t.mu.Lock()
	t.handler = fn
	t.mu.Unlock()
}

func (t *Transport) Send(peer channel.NodeID, env runtime.Envelope) error {
	return t.Session(peer).Send(env)
}

func (t *Transport) Session(peer channel.NodeID) runtime.PeerSession {
	return t.sessions.Session(peer)
}

func (t *Transport) SessionManager() runtime.PeerSessionManager {
	return t
}

func (t *Transport) handleRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeFetchRequest(body)
	if err != nil {
		return nil, err
	}
	service, err := t.boundFetchService()
	if err != nil {
		return nil, err
	}
	resp, err := service.ServeFetch(ctx, req)
	if err != nil {
		return nil, err
	}
	return encodeFetchResponse(resp)
}

func (t *Transport) handleLongPollFetchRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeLongPollFetchRequest(body)
	if err != nil {
		return nil, err
	}
	service, err := t.boundLongPollService()
	if err != nil {
		return nil, err
	}
	resp, err := service.ServeLongPollFetch(ctx, req)
	if err != nil {
		return nil, err
	}
	return encodeLongPollFetchResponse(resp)
}

func (t *Transport) handleReconcileProbeRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeReconcileProbeRequest(body)
	if err != nil {
		return nil, err
	}
	service, err := t.boundReconcileProbeService()
	if err != nil {
		return nil, err
	}
	resp, err := service.ServeReconcileProbe(ctx, req)
	if err != nil {
		return nil, err
	}
	return encodeReconcileProbeResponse(resp)
}

func (t *Transport) deliver(env runtime.Envelope) {
	t.mu.RLock()
	handler := t.handler
	t.mu.RUnlock()
	if handler != nil {
		handler(env)
	}
}

func (t *Transport) boundFetchService() (runtime.FetchService, error) {
	t.mu.RLock()
	service := t.fetchService
	t.mu.RUnlock()
	if service == nil {
		return nil, fmt.Errorf("channeltransport: fetch service must be bound")
	}
	return service, nil
}

func (t *Transport) boundReconcileProbeService() (runtime.ReconcileProbeService, error) {
	t.mu.RLock()
	service := t.reconcileService
	t.mu.RUnlock()
	if service == nil {
		return nil, fmt.Errorf("channeltransport: reconcile probe service must be bound")
	}
	return service, nil
}

func (t *Transport) boundLongPollService() (LongPollService, error) {
	t.mu.RLock()
	service := t.longPollService
	t.mu.RUnlock()
	if service == nil {
		return nil, fmt.Errorf("channeltransport: long poll service must be bound")
	}
	return service, nil
}

func (t *Transport) LongPollFetch(ctx context.Context, peer channel.NodeID, req LongPollFetchRequest) (LongPollFetchResponse, error) {
	body, err := encodeLongPollFetchRequest(req)
	if err != nil {
		return LongPollFetchResponse{}, err
	}
	respBody, err := t.client.RPCService(ctx, uint64(peer), longPollRPCShardKey(req.LaneID), RPCServiceLongPollFetch, body)
	if err != nil {
		return LongPollFetchResponse{}, err
	}
	return decodeLongPollFetchResponse(respBody)
}
