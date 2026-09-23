package channels

import (
	"context"
	"slices"
	"sync"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/replication"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

const mqttCopyConcurrent = 4

// mqttCopyRequest contains only the leader-prepared full-content prefix intent.
// The receiver independently derives it; a sender's hash is never import proof.
type mqttCopyRequest struct {
	Target, Leader ch.NodeID
	Request        ch.MQTTReplayRequest
	Authority      [32]byte
	Before, After  ch.MQTTReplayPrefix
}

type mqttCopyForwarder interface {
	ConfirmMQTTReplayCopy(context.Context, ch.NodeID, mqttCopyRequest) (ch.NodeID, error)
}

func (q mqttCopyRequest) valid() bool {
	r, b, a := q.Request.Range, q.Before, q.After
	if q.Target == 0 || q.Leader == 0 || q.Authority == [32]byte{} || !validMQTTReplayRequest(q.Request) ||
		b.Generation != r.Generation || a.Generation != r.Generation || b.StartAfter != a.StartAfter || b.Through < b.StartAfter ||
		b.Through != r.From-1 || a.Through != r.Through || a.Through-b.Through != uint64(r.Limit) ||
		a.TotalBytes < b.TotalBytes || a.TotalStoredBytes <= b.TotalStoredBytes || a.TotalStoredBytes-b.TotalStoredBytes != uint64(r.MaxBytes) ||
		a.TotalBytes > a.TotalStoredBytes || a.TotalBytes-b.TotalBytes > a.TotalStoredBytes-b.TotalStoredBytes || a.Digest == [32]byte{} {
		return false
	}
	if b.Through == b.StartAfter {
		return b.TotalBytes == 0 && b.TotalStoredBytes == 0 && b.Digest == [32]byte{}
	}
	return b.TotalStoredBytes > 0 && b.TotalBytes <= b.TotalStoredBytes && b.Digest != [32]byte{}
}

// mqttCopyAuthority deliberately omits leases and logical retention: neither
// changes immutable originals. Ordered membership is included without cache use.
func mqttCopyAuthority(m ch.Meta) [32]byte { return ch.MQTTReplayCopyAuthority(m) }

func validateMQTTCopyMeta(q ch.MQTTReplayRequest, m ch.Meta) error {
	if err := validateMQTTChannelAuthority(q.ChannelID, q.ExpectedChannelEpoch, q.ExpectedLeaderEpoch, q.ExpectedRouteGeneration, m); err != nil {
		return err
	}
	if len(m.Replicas) > 256 || len(m.ISR) > 256 || int(m.MinISR)*2 <= len(m.ISR) {
		return ch.ErrNotReady
	}
	return nil
}

func (s *Service) mqttCopyMeta(ctx context.Context, q ch.MQTTReplayRequest) (ch.Meta, error) {
	reader, ok := s.metaSource.(FreshChannelMetaSource)
	if !ok {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	m, err := reader.ResolveChannelMetaFresh(ctx, q.ChannelID)
	if err != nil {
		return ch.Meta{}, err
	}
	if err = ctx.Err(); err != nil {
		return ch.Meta{}, err
	}
	if err = validateMQTTCopyMeta(q, m); err != nil {
		return ch.Meta{}, err
	}
	return m, nil
}

// CopyMQTTReplay obtains durable independent confirmations for a single page.
// Only a current leader plus distinct ISR majority completes the receipt.
func (s *Service) CopyMQTTReplay(ctx context.Context, q ch.MQTTReplayRequest) (ch.MQTTReplayCopyReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var empty ch.MQTTReplayCopyReceipt
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if s == nil || !validMQTTReplayRequest(q) {
		return empty, ch.ErrInvalidConfig
	}
	select {
	case s.mqttCopyCoordinators <- struct{}{}:
		defer func() { <-s.mqttCopyCoordinators }()
	default:
		return empty, ch.ErrBackpressured
	}
	m, err := s.mqttCopyMeta(ctx, q)
	if err != nil {
		return empty, err
	}
	authority := mqttCopyAuthority(m)
	page, err := s.PrepareMQTTReplay(ctx, q)
	if err != nil {
		return empty, err
	}
	// Release the page bodies before fanout; requests and receipts are body-free.
	copyRange := q
	copyRange.Range.Through = page.After.Through
	copyRange.Range.Limit = len(page.Records)
	copyRange.Range.MaxBytes = int(page.After.TotalStoredBytes - page.Before.TotalStoredBytes)
	request := mqttCopyRequest{Leader: m.Leader, Request: copyRange, Authority: authority, Before: page.Before, After: page.After}
	page = ch.MQTTReplayPage{}
	copies, err := s.collectMQTTCopies(ctx, m, request)
	if err != nil {
		return empty, err
	}
	current, err := s.mqttCopyMeta(ctx, q)
	if err != nil {
		return empty, err
	}
	if mqttCopyAuthority(current) != authority {
		return empty, ch.ErrStaleMeta
	}
	return ch.MQTTReplayCopyReceipt{Request: copyRange, Leader: m.Leader, Authority: authority, WriteQuorum: int(m.MinISR), Before: request.Before, After: request.After, Copies: copies}, nil
}

func (s *Service) collectMQTTCopies(ctx context.Context, m ch.Meta, q mqttCopyRequest) ([]ch.NodeID, error) {
	work, cancel := context.WithCancel(ctx)
	jobs := make(chan ch.NodeID, len(m.ISR))
	for _, n := range m.ISR {
		jobs <- n
	}
	close(jobs)
	type result struct {
		node, ack ch.NodeID
		err       error
	}
	results := make(chan result, len(m.ISR))
	workers := min(mqttCopyConcurrent, len(m.ISR))
	var wg sync.WaitGroup
	wg.Add(workers)
	// Cancel and join before releasing coordinator admission or returning evidence.
	defer func() { cancel(); wg.Wait() }()
	goruntimeregistry.SafeGoN(s.goroutines, goruntimeregistry.TaskClusterMQTTCopy, workers, func(int) {
		defer wg.Done()
		for n := range jobs {
			if work.Err() != nil {
				return
			}
			request := q
			request.Target = n
			var ack ch.NodeID
			var err error
			if n == s.localNode {
				ack, err = s.confirmMQTTReplayCopy(work, request)
			} else if f, ok := s.forward.(mqttCopyForwarder); ok {
				ack, err = f.ConfirmMQTTReplayCopy(work, n, request)
			} else {
				err = ch.ErrInvalidConfig
			}
			results <- result{node: n, ack: ack, err: err}
		}
	})
	copies := make([]ch.NodeID, 0, int(m.MinISR))
	leader := false
	for range m.ISR {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-results:
			if r.err == nil && r.ack == r.node {
				copies = append(copies, r.node)
				leader = leader || r.node == m.Leader
			} else if r.node == m.Leader {
				return nil, ch.ErrNotReady
			}
			if leader && len(copies) >= int(m.MinISR) {
				slices.Sort(copies)
				return copies, nil
			}
		}
	}
	return nil, ch.ErrNotReady
}

// confirmMQTTReplayCopy cannot advance HW. It independently checks an already
// committed activation and copies/reads canonical originals before acknowledging.
func (s *Service) confirmMQTTReplayCopy(ctx context.Context, q mqttCopyRequest) (ch.NodeID, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s == nil || !q.valid() {
		return 0, ch.ErrInvalidConfig
	}
	if q.Target != s.localNode {
		return 0, ch.ErrNotReplica
	}
	select {
	case s.mqttCopyReceivers <- struct{}{}:
		defer func() { <-s.mqttCopyReceivers }()
	default:
		return 0, ch.ErrBackpressured
	}
	m, err := s.mqttCopyMeta(ctx, q.Request)
	if err != nil {
		return 0, err
	}
	if m.Leader != q.Leader || mqttCopyAuthority(m) != q.Authority {
		return 0, ch.ErrStaleMeta
	}
	if !slices.Contains(m.ISR, s.localNode) {
		return 0, ch.ErrNotReplica
	}
	if s.store == nil {
		return 0, ch.ErrInvalidConfig
	}
	st, err := s.store.ChannelStore(ch.ChannelKeyForID(q.Request.ChannelID), q.Request.ChannelID)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	reader, ok := st.(channelstore.MQTTSourceReader)
	if !ok {
		return 0, ch.ErrInvalidConfig
	}
	preparer, ok := st.(channelstore.MQTTReplayPreparer)
	if !ok {
		return 0, ch.ErrInvalidConfig
	}
	source, found, err := reader.LoadCommittedMQTTSource(ctx, q.After.Through)
	if err != nil {
		return 0, err
	}
	if !found || source.Generation != q.Before.Generation || source.StartAfter != q.Before.StartAfter || source.CommittedThrough < q.After.Through {
		return 0, ch.ErrNotReady
	}
	prefix := q.Before
	for prefix.Through < q.After.Through {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		r := q.Request.Range
		r.From = prefix.Through + 1
		r.Limit = int(q.After.Through - prefix.Through)
		r.MaxBytes = int(q.After.TotalStoredBytes - prefix.TotalStoredBytes)
		if !r.Valid() {
			return 0, ch.ErrLogConflict
		}
		page, e := preparer.PrepareMQTTReplay(ctx, r)
		if e != nil {
			return 0, e
		}
		if !page.ValidFor(r) || page.Before != prefix || page.After.TotalStoredBytes > q.After.TotalStoredBytes || page.After.TotalBytes > q.After.TotalBytes {
			return 0, ch.ErrLogConflict
		}
		prefix = page.After
	}
	if prefix != q.After {
		return 0, ch.ErrLogConflict
	}
	if s.localNode == m.Leader && s.replicaCommitRefresh != nil {
		authority := replication.Authority{Key: ch.ChannelKeyForID(m.ID), ChannelID: m.ID,
			ID:     replication.AuthorityID{ChannelEpoch: m.Epoch, LeaderTerm: m.LeaderEpoch, FenceVersion: m.RouteGeneration},
			Leader: m.Leader, Voters: m.ISR, WriteQuorum: int(m.MinISR), WriteFence: m.WriteFence}
		for _, node := range m.Replicas {
			if !slices.Contains(m.ISR, node) {
				authority.Learners = append(authority.Learners, node)
			}
		}
		// Native repair owns proof, scheduling and checkpoint propagation. This
		// hint cannot turn the copy request's Through value into committed HW.
		if err = s.replicaCommitRefresh.RequestCommittedReplicaRefresh(ctx, authority); err != nil {
			return 0, err
		}
	}
	current, err := s.mqttCopyMeta(ctx, q.Request)
	if err != nil {
		return 0, err
	}
	if mqttCopyAuthority(current) != q.Authority {
		return 0, ch.ErrStaleMeta
	}
	return s.localNode, nil
}

func (g *ServiceGateway) confirmMQTTReplayCopy(ctx context.Context, q mqttCopyRequest) (ch.NodeID, error) {
	s, err := g.service()
	if err != nil {
		return 0, err
	}
	return s.confirmMQTTReplayCopy(ctx, q)
}

var _ ch.MQTTReplayCopier = (*Service)(nil)
