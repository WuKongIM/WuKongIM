package channels

import (
	"context"
	"slices"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

const mqttRecoveryDonorAttempts = 4
const mqttRecoveryDonorTimeout = 750 * time.Millisecond
const mqttRecoveryRetireLimit = 64

type mqttRecoveryForwarder interface {
	ForwardMQTTReplayRecoveryStep(context.Context, ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error)
}

// StepMQTTReplayRecovery routes once to the exact receiver and shares repair's
// bounded admission. Neither the caller nor a donor chooses local content progress.
func (s *Service) StepMQTTReplayRecovery(ctx context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	var empty ch.MQTTReplayRecoveryResult
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
	authority := ch.MQTTReplayRequest{ChannelID: q.Source.ChannelID, ExpectedChannelEpoch: q.Source.ExpectedChannelEpoch, ExpectedLeaderEpoch: q.Source.ExpectedLeaderEpoch, ExpectedRouteGeneration: q.Source.ExpectedRouteGeneration}
	m, err := s.mqttRepairAuthority(ctx, authority, q.Target)
	if err != nil {
		return empty, err
	}
	var result ch.MQTTReplayRecoveryResult
	if q.Target == s.localNode {
		result, err = s.stepLocalMQTTRecovery(ctx, q, authority, m)
	} else if forward, ok := s.forward.(mqttRecoveryForwarder); ok {
		result, err = forward.ForwardMQTTReplayRecoveryStep(ctx, q)
	} else {
		return empty, ch.ErrInvalidConfig
	}
	if err != nil {
		return empty, err
	}
	if err = s.recheckMQTTRepairAuthority(ctx, authority, m, q.Target); err != nil {
		return empty, err
	}
	if !result.ValidFor(q) || (result.DonorAfter != 0 && !slices.Contains(m.Replicas, result.DonorAfter)) {
		return empty, ch.ErrLogConflict
	}
	return result, nil
}

// stepLocalMQTTRecovery keeps one store lease and never recursively reserves a
// receiver slot. Every donor page is bounded by a receiver-verified anchor delta.
func (s *Service) stepLocalMQTTRecovery(ctx context.Context, q ch.MQTTReplayRecoveryRequest, authority ch.MQTTReplayRequest, m ch.Meta) (ch.MQTTReplayRecoveryResult, error) {
	var empty ch.MQTTReplayRecoveryResult
	if s.store == nil {
		return empty, ch.ErrInvalidConfig
	}
	handle, err := s.store.ChannelStore(ch.ChannelKeyForID(q.Source.ChannelID), q.Source.ChannelID)
	if err != nil {
		return empty, err
	}
	defer handle.Close()
	planner, ok := handle.(channelstore.MQTTReplayRepairPlanner)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	pending := false
	if q.ApplyRetirement {
		pending, err = s.applyMQTTRecoveryRetirement(ctx, handle, q, authority, m)
		if err != nil {
			return empty, err
		}
	}
	plan, err := planner.PlanMQTTReplayRepair(ctx, ch.MQTTReplayRepairScan{Generation: q.Source.Generation, TargetAnchor: q.TargetAnchor, AfterAnchor: q.AfterAnchor, Limit: q.ScanLimit})
	if err != nil {
		return empty, err
	}
	if !q.AcceptsPlan(plan) {
		return empty, ch.ErrLogConflict
	}
	result := ch.MQTTReplayRecoveryResult{Plan: plan, RetirementPending: pending}
	if !plan.HasNext {
		if plan.Complete && q.ReleaseSource {
			release, ok := handle.(channelstore.MQTTSourceReleaser)
			if !ok {
				return empty, ch.ErrInvalidConfig
			}
			if err = s.recheckMQTTRepairAuthority(ctx, authority, m, q.Target); err != nil {
				return empty, err
			}
			// The plan is scheduling evidence, not a substitute for the store's
			// independent atomic revalidation of this exact committed anchor.
			if err = release.ReleaseMQTTSourceAtAnchor(ctx, q.Source.Generation, q.TargetAnchor); err != nil {
				return empty, err
			}
			result.SourceReleased = true
		}
		return result, nil
	}
	interval, has, err := plan.NextRange()
	if err != nil {
		return empty, err
	}
	if !has {
		return empty, ch.ErrLogConflict
	}
	donors := mqttRecoveryDonors(m, q.Target)
	if len(donors) == 0 {
		return empty, ch.ErrNotReady
	}
	transfer, ok := handle.(channelstore.MQTTReplayAnchorTransfer)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	forward, ok := s.forward.(mqttRepairForwarder)
	if !ok {
		return empty, ch.ErrInvalidConfig
	}
	start := 0
	if at := slices.Index(donors, q.DonorAfter); at >= 0 {
		start = (at + 1) % len(donors)
	}
	repair := ch.MQTTReplayRepairRequest{Target: q.Target, AnchorPosition: plan.Next.Manifest.LastOffset, Request: authority}
	repair.Request.Range = interval
	for attempt := 0; attempt < min(len(donors), mqttRecoveryDonorAttempts); attempt++ {
		repair.Donor = donors[(start+attempt)%len(donors)]
		if err = s.recheckMQTTRepairMeta(ctx, repair, m); err != nil {
			return empty, err
		}
		page, fetchErr := fetchMQTTRecoveryDonor(ctx, forward, repair)
		if err = ctx.Err(); err != nil {
			return empty, err
		}
		if fetchErr != nil || !page.ValidFor(interval) || page.After != plan.Next.Prefix() {
			result.DonorAfter = repair.Donor
			continue
		}
		if err = s.recheckMQTTRepairMeta(ctx, repair, m); err != nil {
			return empty, err
		}
		// Import atomically reloads the receiver's committed proof. Local failures
		// remain errors even when other donors exist; only fetch failures rotate.
		prefix, err := transfer.ImportMQTTReplayAnchor(ctx, repair.AnchorPosition, page)
		if err != nil {
			return empty, err
		}
		if prefix != plan.Next.Prefix() {
			return empty, ch.ErrLogConflict
		}
		result.Repaired = true
		result.DonorAfter = 0
		return result, nil
	}
	return result, nil
}

// applyMQTTRecoveryRetirement consumes only receiver-owned committed evidence.
// The bounded store mutation independently rechecks it under append ownership;
// repeated turns resume its durable cursor without any caller cleanup position.
func (s *Service) applyMQTTRecoveryRetirement(ctx context.Context, handle channelstore.ChannelStore, q ch.MQTTReplayRecoveryRequest, authority ch.MQTTReplayRequest, m ch.Meta) (bool, error) {
	reader, ok := handle.(channelstore.MQTTReplayLatestRetirementReader)
	retirer, canRetire := handle.(channelstore.MQTTReplayRetirer)
	if !ok || !canRetire {
		return false, ch.ErrInvalidConfig
	}
	proof, found, err := reader.LoadLatestMQTTReplayRetirement(ctx, q.Source.Generation)
	if err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	if !found {
		if proof != (ch.MQTTReplayRetirementProof{}) {
			return false, ch.ErrLogConflict
		}
		return false, nil
	}
	if !q.AcceptsRetirement(proof) {
		return false, ch.ErrLogConflict
	}
	if err = s.recheckMQTTRepairAuthority(ctx, authority, m, q.Target); err != nil {
		return false, err
	}
	r, err := retirer.RetireMQTTReplay(ctx, q.Source.Generation, proof.Manifest.LastOffset, mqttRecoveryRetireLimit)
	if err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	a, p := proof.Retirement.Anchor, r.Retired
	if r.RetirementPosition < proof.Manifest.LastOffset || p.Generation != q.Source.Generation || p.StartAfter != a.StartAfter ||
		p.Through < a.Through || p.Through >= r.RetirementPosition || p.TotalBytes < a.TotalBytes || p.TotalStoredBytes < a.TotalStoredBytes ||
		p.TotalBytes > p.TotalStoredBytes || p.Digest == [32]byte{} || (p.Through == a.Through && (p.TotalBytes != a.TotalBytes || p.TotalStoredBytes != a.TotalStoredBytes || p.Digest != a.Digest)) ||
		r.Deleted < 0 || r.Deleted > mqttRecoveryRetireLimit || r.DeletedThrough < p.StartAfter || r.DeletedThrough > p.Through || r.Done != (r.DeletedThrough == p.Through) {
		return false, ch.ErrLogConflict
	}
	return !r.Done, nil
}

func fetchMQTTRecoveryDonor(ctx context.Context, forward mqttRepairForwarder, q ch.MQTTReplayRepairRequest) (ch.MQTTReplayPage, error) {
	attempt, cancel := context.WithTimeout(ctx, mqttRecoveryDonorTimeout)
	defer cancel()
	return forward.FetchMQTTReplayRepair(attempt, q)
}

// mqttRecoveryDonors orders bounded current placement: leader, other ISR, then
// learners. A hint rotates this list without changing any accepted content cursor.
func mqttRecoveryDonors(m ch.Meta, target ch.NodeID) []ch.NodeID {
	donors := make([]ch.NodeID, 0, len(m.Replicas))
	seen := make(map[ch.NodeID]bool, len(m.Replicas))
	add := func(n ch.NodeID) {
		if n != target && !seen[n] {
			seen[n] = true
			donors = append(donors, n)
		}
	}
	add(m.Leader)
	for _, n := range m.ISR {
		add(n)
	}
	for _, n := range m.Replicas {
		add(n)
	}
	return donors
}

func (s *Service) handleMQTTReplayRecoveryStep(ctx context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	if s == nil {
		return ch.MQTTReplayRecoveryResult{}, ch.ErrNotReady
	}
	if s.localNode != q.Target {
		return ch.MQTTReplayRecoveryResult{}, ch.ErrNotReplica
	}
	return s.StepMQTTReplayRecovery(ctx, q)
}
func (g *ServiceGateway) handleMQTTReplayRecoveryStep(ctx context.Context, q ch.MQTTReplayRecoveryRequest) (ch.MQTTReplayRecoveryResult, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTReplayRecoveryResult{}, err
	}
	return s.handleMQTTReplayRecoveryStep(ctx, q)
}

var _ ch.MQTTReplayRecoveryStepper = (*Service)(nil)
