package channels

import (
	"context"
	"slices"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

const mqttRepairConcurrent = 4

type mqttRepairForwarder interface {
	ForwardMQTTReplayRepair(context.Context, ch.MQTTReplayRepairRequest) (ch.MQTTReplayPrefix, error)
	FetchMQTTReplayRepair(context.Context, ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error)
}

// mqttRepairMeta permits immutable recovery while a migration blocks new writes.
// It still requires fresh authority, strict-majority configuration and placement.
func (s *Service) mqttRepairMeta(ctx context.Context, q ch.MQTTReplayRepairRequest) (ch.Meta, error) {
	reader, ok := s.metaSource.(FreshChannelMetaSource)
	if !ok {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	m, err := reader.ResolveChannelMetaFresh(ctx, q.Request.ChannelID)
	if err != nil {
		return ch.Meta{}, err
	}
	if err = ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	if len(m.Replicas) > 256 || len(m.ISR) > 256 {
		return ch.Meta{}, ch.ErrNotReady
	}
	unfenced := m
	unfenced.WriteFence = ch.WriteFence{}
	if err = validateMQTTCopyMeta(q.Request, unfenced); err != nil {
		return ch.Meta{}, err
	}
	if !slices.Contains(m.Replicas, q.Target) || !slices.Contains(m.Replicas, q.Donor) {
		return ch.Meta{}, ch.ErrNotReplica
	}
	return m, nil
}

func (s *Service) recheckMQTTRepairMeta(ctx context.Context, q ch.MQTTReplayRepairRequest, before ch.Meta) error {
	current, err := s.mqttRepairMeta(ctx, q)
	if err != nil {
		return err
	}
	if mqttCopyAuthority(current) != mqttCopyAuthority(before) || !sameWriteFence(current.WriteFence, before.WriteFence) {
		return ch.ErrStaleMeta
	}
	return nil
}

// RepairMQTTReplay routes once to the exact receiver. Each receiver establishes
// its own committed anchor before fetching, then rechecks authority around import.
func (s *Service) RepairMQTTReplay(ctx context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPrefix, error) {
	var empty ch.MQTTReplayPrefix
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if s == nil || !q.Valid() {
		return empty, ch.ErrInvalidConfig
	}
	select {
	case s.mqttRepairReceivers <- struct{}{}:
		defer func() { <-s.mqttRepairReceivers }()
	default:
		return empty, ch.ErrBackpressured
	}
	m, err := s.mqttRepairMeta(ctx, q)
	if err != nil {
		return empty, err
	}
	var prefix ch.MQTTReplayPrefix
	if q.Target == s.localNode {
		prefix, err = s.importMQTTReplayRepair(ctx, q, m)
	} else if forward, ok := s.forward.(mqttRepairForwarder); ok {
		prefix, err = forward.ForwardMQTTReplayRepair(ctx, q)
	} else {
		return empty, ch.ErrInvalidConfig
	}
	if err != nil {
		return empty, err
	}
	if err = s.recheckMQTTRepairMeta(ctx, q, m); err != nil {
		return empty, err
	}
	if !q.AcceptsPrefix(prefix) {
		return empty, ch.ErrLogConflict
	}
	return prefix, nil
}

func (s *Service) importMQTTReplayRepair(ctx context.Context, q ch.MQTTReplayRepairRequest, m ch.Meta) (ch.MQTTReplayPrefix, error) {
	var empty ch.MQTTReplayPrefix
	if s.store == nil {
		return empty, ch.ErrInvalidConfig
	}
	handle, err := s.store.ChannelStore(ch.ChannelKeyForID(q.Request.ChannelID), q.Request.ChannelID)
	if err != nil {
		return empty, err
	}
	defer handle.Close()
	reader, ok := handle.(channelstore.MQTTReplayAnchorReader)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	transfer, ok := handle.(channelstore.MQTTReplayAnchorTransfer)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	proof, found, err := reader.LoadMQTTReplayAnchor(ctx, q.AnchorPosition)
	if err != nil {
		return empty, err
	}
	if !found {
		return empty, ch.ErrNotReady
	}
	if !q.AcceptsAnchor(proof) {
		return empty, ch.ErrLogConflict
	}
	forward, ok := s.forward.(mqttRepairForwarder)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	page, err := forward.FetchMQTTReplayRepair(ctx, q)
	if err != nil {
		return empty, err
	}
	if !page.ValidFor(q.Request.Range) || page.After != proof.Prefix() {
		return empty, ch.ErrLogConflict
	}
	if err = s.recheckMQTTRepairMeta(ctx, q, m); err != nil {
		return empty, err
	}
	// Storage reloads its own anchor under append/checkpoint ownership, closing
	// the gap between this preflight and the actual durable content commit.
	prefix, err := transfer.ImportMQTTReplayAnchor(ctx, q.AnchorPosition, page)
	if err != nil {
		return empty, err
	}
	if prefix != proof.Prefix() {
		return empty, ch.ErrLogConflict
	}
	return prefix, nil
}

// exportMQTTReplayRepair uses a separate admission pool, so cross-node receives
// cannot occupy every slot needed by their nested donor reads. It performs no I/O
// on a reactor and creates no per-Channel goroutine or waiting queue.
func (s *Service) exportMQTTReplayRepair(ctx context.Context, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
	var empty ch.MQTTReplayPage
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if s == nil || !q.Valid() {
		return empty, ch.ErrInvalidConfig
	}
	if s.localNode != q.Donor {
		return empty, ch.ErrNotReplica
	}
	select {
	case s.mqttRepairDonors <- struct{}{}:
		defer func() { <-s.mqttRepairDonors }()
	default:
		return empty, ch.ErrBackpressured
	}
	m, err := s.mqttRepairMeta(ctx, q)
	if err != nil {
		return empty, err
	}
	if s.store == nil {
		return empty, ch.ErrInvalidConfig
	}
	handle, err := s.store.ChannelStore(ch.ChannelKeyForID(q.Request.ChannelID), q.Request.ChannelID)
	if err != nil {
		return empty, err
	}
	defer handle.Close()
	transfer, ok := handle.(channelstore.MQTTReplayAnchorTransfer)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	page, err := transfer.ExportMQTTReplayAnchor(ctx, q.AnchorPosition, q.Request.Range)
	if err != nil {
		return empty, err
	}
	if err = s.recheckMQTTRepairMeta(ctx, q, m); err != nil {
		return empty, err
	}
	if !page.ValidFor(q.Request.Range) || !q.AcceptsPrefix(page.After) {
		return empty, ch.ErrLogConflict
	}
	return page, nil
}

func (s *Service) handleMQTTReplayRepair(ctx context.Context, q mqttRepairRPCRequest) (mqttRepairRPCResult, error) {
	if s == nil {
		return mqttRepairRPCResult{}, ch.ErrNotReady
	}
	if q.Export {
		p, err := s.exportMQTTReplayRepair(ctx, q.Request)
		return mqttRepairRPCResult{Page: p}, err
	}
	if s.localNode != q.Request.Target {
		return mqttRepairRPCResult{}, ch.ErrNotReplica
	}
	p, err := s.RepairMQTTReplay(ctx, q.Request)
	return mqttRepairRPCResult{Prefix: p}, err
}

func (g *ServiceGateway) handleMQTTReplayRepair(ctx context.Context, q mqttRepairRPCRequest) (mqttRepairRPCResult, error) {
	s, err := g.service()
	if err != nil {
		return mqttRepairRPCResult{}, err
	}
	return s.handleMQTTReplayRepair(ctx, q)
}

var _ ch.MQTTReplayRepairer = (*Service)(nil)
