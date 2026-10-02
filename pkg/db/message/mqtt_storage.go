package message

import (
	"context"
	"encoding/binary"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/keycodec"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTStorageGrantReply carries only the node's authoritative escrow evidence.
type MQTTStorageGrantReply struct {
	Revision, Bytes, Limit, Total uint64
	// RosterRevision fences spending against older Controller membership views.
	RosterRevision      uint64
	Status              string
	Ready, MembersMatch bool
	Debt                uint64
	Initialized         bool
}

// MQTTStorageObserver receives fixed aggregate gauges and closed event names.
// Calls must be non-blocking and must not retain publication identities.
type MQTTStorageObserver interface {
	ObserveMQTTStorage(used, granted, nodeLimit, clusterLimit uint64)
	ObserveMQTTStorageEvent(string)
}

// MQTTStorageGrantRequest captures one immutable storage roster with its CAS.
type MQTTStorageGrantRequest struct {
	MembershipRevision      uint64
	Revision, Bytes, Target uint64
	Members                 []uint64
	InitialDebt             *uint64
}

// MQTTStorageOptions bounds source-shared capacity independently of Session
// quotas. Adjust must commit one exact non-expiring cluster grant before reply.
type MQTTStorageOptions struct {
	NodeBytes, ClusterBytes uint64
	Observer                MQTTStorageObserver
	Members                 func() ([]uint64, uint64)
	Adjust                  func(ctx context.Context, request MQTTStorageGrantRequest) (MQTTStorageGrantReply, error)
}

// mqttStorageBudget serializes reservations and short atomic capacity commits.
// Its one grant-growth call is bounded and amortized across up to 8 MiB.
type mqttStorageBudget struct {
	mu   sync.Mutex
	opts MQTTStorageOptions
	// used includes live rows and unresolved durable prebooking responsibilities.
	used uint64
	// revision and granted echo the last authoritative non-expiring node escrow.
	revision, granted uint64
	// spendable never exceeds the confirmed grant and closes before a refund RPC.
	spendable uint64
	// pendingRefund retains one unknown physical deletion until an independently
	// proved marker or exact synchronous retry settles its conservative debit.
	pendingRefund *mqttStoragePendingRefund
	readyErr      error
	// members is immutable; roster changes close cached spending before RPC.
	members                     []uint64
	membershipRevision          uint64
	publishedMembershipRevision uint64
	registered, ready           bool
	// lastReserve retains grant chunks while publication traffic is flowing.
	lastReserve time.Time
}

type mqttStorageCharge struct{ position, bytes uint64 }

// ConfigureMQTTStorage runs before Channel runtime admission. Existing protected
// data is retained and charged even if it already exceeds the new limit.
func (e *Engine) ConfigureMQTTStorage(opts MQTTStorageOptions) {
	if e == nil || e.db == nil || opts.NodeBytes == 0 || opts.ClusterBytes == 0 || opts.Adjust == nil || opts.Members == nil {
		return
	}
	b := &mqttStorageBudget{opts: opts}
	e.db.mqttStorage = b
	b.readyErr = e.db.rebuildMQTTStorage(context.Background())
	b.observe()
	if b.readyErr != nil {
		b.event("rebuild_failed")
	}
}

func mqttStorageChargeKey(key ChannelKey, position uint64) []byte {
	return binary.BigEndian.AppendUint64(encodeMessageSystemPrefix(key, messageSystemIDMQTTStorage), position)
}

func decodeMQTTStorageCharge(key, value []byte) (uint64, error) {
	e, err := rowcodec.UnwrapBorrowed(key, value)
	if err != nil {
		return 0, err
	}
	if e.Version != 1 || e.Codec != rowcodec.CodecFixed || e.Flags != rowcodec.FlagChecksum || len(e.Payload) != 8 {
		return 0, dberrors.ErrCorruptValue
	}
	n := binary.BigEndian.Uint64(e.Payload)
	if n == 0 {
		return 0, dberrors.ErrCorruptValue
	}
	return n, nil
}

func stageMQTTStorageCharges(batch *engine.Batch, key ChannelKey, charges []mqttStorageCharge) error {
	for _, c := range charges {
		k := mqttStorageChargeKey(key, c.position)
		if err := batch.Set(k, rowcodec.Wrap(k, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, binary.BigEndian.AppendUint64(nil, c.bytes))); err != nil {
			return err
		}
	}
	return nil
}

// mqttStorageRowBytes reserves the original and its future replay envelope,
// keys, replay meter and charge receipt. The fixed allowance is conservative;
// metadata control entries remain in the existing bounded maintenance path.
func mqttStorageRowBytes(key ChannelKey, generation string, row messageRow) uint64 {
	return 2*uint64(encodedMessageHeaderLen(row)) + 512 + 4*uint64(len(key)) + 2*uint64(len(generation))
}

// mqttStorageRowCharge exempts only explicit native maintenance identities.
// New proposals supply their already-validated manifest; reconstruction/copy
// independently verifies canonical paired evidence before granting the exemption.
func (e *channelEntry) mqttStorageRowCharge(row messageRow, generation string, proposals []durableProposalRecord) (uint64, error) {
	for _, p := range proposals {
		if row.MessageSeq > p.manifest.BaseOffset && row.MessageSeq <= p.manifest.LastOffset {
			if quorumlog.InternalProposalVersion(p.manifest.Version) {
				return 0, nil
			}
			return mqttStorageRowBytes(e.key, generation, row), nil
		}
	}
	entry, m, err := mqttReplayCommittedEntry(e.db.engine, e.key, row.MessageSeq, math.MaxUint64)
	if err != nil {
		return 0, err
	}
	if !verifyBackupRowIdentity(entry, row) {
		return 0, dberrors.ErrCorruptState
	}
	if quorumlog.InternalProposalVersion(m.Version) {
		return 0, nil
	}
	return mqttStorageRowBytes(e.key, generation, row), nil
}

func (e *channelEntry) mqttStorageSource(proposals []durableProposalRecord) (MQTTSourceState, bool, error) {
	s, found, err := e.loadMQTTSourceState(context.Background())
	if err != nil || found {
		return s, found, err
	}
	p, present, err := loadMQTTActivation(e.db.engine, e.key)
	if err != nil {
		return s, false, err
	}
	if !present {
		for _, candidate := range proposals {
			if candidate.manifest.Version == quorumlog.MQTTSourceProposalManifestVersion {
				p = candidate.manifest
				present = true
				break
			}
		}
	}
	if !present {
		return s, false, nil
	}
	return MQTTSourceState{Generation: quorumlog.MQTTSourceGeneration(p.CommandID), StartAfter: p.BaseOffset}, true, nil
}

func (e *channelEntry) prepareMQTTStorageBooking(ctx context.Context, rows []messageRow, proposals []durableProposalRecord) ([]mqttStorageCharge, uint64, error) {
	if e.db.mqttStorage == nil || len(rows) == 0 {
		return nil, 0, nil
	}
	s, found, err := e.mqttStorageSource(proposals)
	if err != nil || !found {
		return nil, 0, err
	}
	var charges []mqttStorageCharge
	var total uint64
	for _, row := range rows {
		if row.MessageSeq <= s.StartAfter {
			continue
		}
		n, err := e.mqttStorageRowCharge(row, s.Generation, proposals)
		if err != nil {
			return nil, 0, err
		}
		if n == 0 {
			continue
		}
		k := mqttStorageChargeKey(e.key, row.MessageSeq)
		value, exists, err := e.db.engine.Get(k)
		if err != nil {
			return nil, 0, err
		}
		if exists {
			old, err := decodeMQTTStorageCharge(k, value)
			if err != nil {
				return nil, 0, err
			}
			if old != n {
				return nil, 0, dberrors.ErrCorruptState
			}
			continue
		}
		if n > math.MaxUint64-total {
			return nil, 0, dberrors.ErrCorruptState
		}
		total += n
		charges = append(charges, mqttStorageCharge{row.MessageSeq, n})
	}
	if err = e.db.mqttStorage.reserve(ctx, total); err != nil {
		return nil, 0, err
	}
	return charges, total, nil
}

// prepareMQTTStorage accepts new rows only after an exact durable prebooking.
func (e *channelEntry) prepareMQTTStorage(ctx context.Context, rows []messageRow, proposals []durableProposalRecord) ([]mqttStorageCharge, uint64, error) {
	if e.db.mqttStorage == nil || len(rows) == 0 {
		return nil, 0, nil
	}
	source, found, err := e.mqttStorageSource(proposals)
	if err != nil || !found {
		return nil, 0, err
	}
	var business bool
	for _, row := range rows {
		if row.MessageSeq > source.StartAfter {
			n, err := e.mqttStorageRowCharge(row, source.Generation, proposals)
			if err != nil {
				return nil, 0, err
			}
			if n == 0 {
				continue
			}
			business = true
			break
		}
	}
	if !business {
		return nil, 0, nil
	}
	ticket, present, err := loadMQTTStorageTicket(e.db.engine, e.key)
	if err != nil {
		return nil, 0, err
	}
	if !present || ticket.Phase != mqttStoragePrepared {
		return nil, 0, channel.ErrBackpressured
	}
	var exact bool
	for _, p := range proposals {
		if p.manifest == ticket.Manifest {
			exact = true
		}
	}
	if !exact {
		return nil, 0, channel.ErrBackpressured
	}
	// Every newly admitted charge must already exist; booking may never run in
	// an original mutation because that could leave an unfunded partial proposal.
	for _, row := range rows {
		if row.MessageSeq <= source.StartAfter {
			continue
		}
		n, err := e.mqttStorageRowCharge(row, source.Generation, proposals)
		if err != nil {
			return nil, 0, err
		}
		if n == 0 {
			continue
		}
		k := mqttStorageChargeKey(e.key, row.MessageSeq)
		v, present, err := e.db.engine.Get(k)
		if err != nil {
			return nil, 0, err
		}
		if !present {
			return nil, 0, dberrors.ErrCorruptState
		}
		got, err := decodeMQTTStorageCharge(k, v)
		if err != nil || got != n {
			return nil, 0, dberrors.ErrCorruptState
		}
	}
	return nil, 0, e.db.mqttStorage.ensureFunded(ctx)
}

// ensureFunded also reconciles rebuilt/legacy debt before exact retry receipts.
func (b *mqttStorageBudget) ensureFunded(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	defer b.observe()
	if b.readyErr != nil {
		return b.readyErr
	}
	if err := b.reconcileRefund(); err != nil {
		return err
	}
	if err := b.register(ctx); err != nil {
		return err
	}
	// Existing tickets may finish within their confirmed grant after a limit decrease.
	if b.used > b.spendable {
		return b.adjust(ctx, b.used, nil)
	}
	return ctxErr(ctx)
}

func (b *mqttStorageBudget) observe() {
	if b.opts.Observer != nil {
		b.opts.Observer.ObserveMQTTStorage(b.used, b.granted, b.opts.NodeBytes, b.opts.ClusterBytes)
	}
}
func (b *mqttStorageBudget) event(event string) {
	if b.opts.Observer != nil {
		b.opts.Observer.ObserveMQTTStorageEvent(event)
	}
}

func (b *mqttStorageBudget) reserve(ctx context.Context, n uint64) error {
	if n == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	defer b.observe()
	if b.readyErr != nil {
		return b.readyErr
	}
	if err := b.reconcileRefund(); err != nil {
		return err
	}
	if b.pendingRefund != nil {
		return channel.ErrBackpressured
	}
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if b.used > b.opts.NodeBytes || n > b.opts.NodeBytes-b.used {
		b.event("node_full")
		return channel.ErrBackpressured
	}
	if err := b.register(ctx); err != nil {
		return err
	}
	if !b.ready {
		if err := b.adjust(ctx, min(b.used, b.opts.ClusterBytes), nil); err != nil {
			return err
		}
		if !b.ready {
			return channel.ErrBackpressured
		}
	}
	need := b.used + n
	if need > b.spendable {
		quantum := min(uint64(8<<20), b.opts.ClusterBytes/64, b.opts.NodeBytes)
		if quantum == 0 {
			quantum = 1
		}
		target := need
		if quantum-1 <= math.MaxUint64-need {
			target = ((need + quantum - 1) / quantum) * quantum
		}
		target = min(target, b.opts.NodeBytes, b.opts.ClusterBytes)
		if target < need {
			return channel.ErrBackpressured
		}
		if err := b.adjust(ctx, target, nil); err != nil {
			return err
		}
		if !b.ready || b.spendable < need {
			return channel.ErrBackpressured
		}
	}
	b.used = need
	b.lastReserve = time.Now()
	return nil
}

// adjust never spends an uncertain allocation. A contraction closes local
// reuse before its RPC, so a lost release reply cannot spend refunded credit.
func (b *mqttStorageBudget) adjust(ctx context.Context, target uint64, initialDebt *uint64) error {
	if target < b.spendable {
		b.spendable = target
	}
	call, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		out, err := b.opts.Adjust(call, MQTTStorageGrantRequest{Revision: b.revision, Bytes: b.granted, Target: target, Members: b.members, MembershipRevision: b.membershipRevision, InitialDebt: initialDebt})
		if err != nil {
			return err
		}
		if out.Limit == 0 || out.Total > out.Limit || out.Bytes > out.Total {
			return dberrors.ErrCorruptValue
		}
		b.revision, b.granted = out.Revision, out.Bytes
		b.publishedMembershipRevision = out.RosterRevision
		b.ready = out.Ready && out.MembersMatch && out.Limit == b.opts.ClusterBytes
		if initialDebt != nil && out.MembersMatch && out.Initialized && out.Debt == *initialDebt {
			b.registered = true
		}
		if out.Limit == b.opts.ClusterBytes && out.MembersMatch {
			b.spendable = min(out.Bytes, target)
		} else {
			b.spendable = min(b.spendable, out.Bytes)
		}
		switch out.Status {
		case "applied", "unchanged":
			if out.Bytes != target {
				return dberrors.ErrCorruptValue
			}
			return nil
		case "conflict":
			continue
		case "full", "config_mismatch":
			b.event(out.Status)
			return channel.ErrBackpressured
		default:
			return dberrors.ErrCorruptValue
		}
	}
	return channel.ErrBackpressured
}

func (b *mqttStorageBudget) cancelReservation(n uint64) {
	if b == nil || n == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	defer b.observe()
	if n > b.used {
		b.readyErr = dberrors.ErrCorruptState
		return
	}
	b.used -= n
}

func (b *mqttStorageBudget) release(ctx context.Context, n uint64) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	defer b.observe()
	if err := b.reconcileRefund(); err != nil {
		return err
	}
	if n > b.used {
		b.readyErr = dberrors.ErrCorruptState
		return b.readyErr
	}
	b.used -= n
	if n > 0 {
		b.event("retired")
	}
	return ctxErr(ctx)
}

// register requires every storage node to fund its reconstructed startup debt.
// Members publishes immutable sorted slices and makes no per-message copy.
func (b *mqttStorageBudget) register(ctx context.Context) error {
	members, revision := b.opts.Members()
	if len(members) == 0 {
		b.spendable = 0
		b.ready = false
		return channel.ErrBackpressured
	}
	b.membershipRevision = max(b.membershipRevision, revision)
	if !slices.Equal(members, b.members) {
		b.members = members
		b.registered = false
		b.ready = false
		b.spendable = 0
	}
	if b.registered {
		return nil
	}
	debt := b.used
	return b.adjust(ctx, min(debt, b.opts.ClusterBytes), &debt)
}

// MaintainMQTTStorage reuses the node periodic owner. Idle refunds retain chunk
// amortization during continuous publish/ACK traffic; no Channel timer is added.
func (e *Engine) MaintainMQTTStorage(ctx context.Context) error {
	if e == nil || e.db == nil {
		return channel.ErrClosed
	}
	if e.db.mqttStorage == nil {
		return nil
	}
	if err := e.retryMQTTStorageCancellation(ctx); err != nil {
		return err
	}
	b := e.db.mqttStorage
	if !b.mu.TryLock() {
		return channel.ErrBackpressured
	}
	defer b.mu.Unlock()
	defer b.observe()
	if b.readyErr != nil {
		return b.readyErr
	}
	if err := b.reconcileRefund(); err != nil {
		return err
	}
	if err := b.register(ctx); err != nil {
		return err
	}
	if b.membershipRevision > b.publishedMembershipRevision {
		if err := b.adjust(ctx, b.granted, nil); err != nil {
			return err
		}
	}
	if !b.ready || b.used > b.granted || b.granted > b.used && time.Since(b.lastReserve) >= 5*time.Second {
		debt := b.used
		return b.adjust(ctx, min(b.used, b.opts.ClusterBytes), &debt)
	}
	return nil
}

// rebuildMQTTStorage scans canonical source/replay rows in bounded batches.
// It reconstructs derived charge receipts after upgrade, restart or restore;
// ordinary history below a proved replay deletion floor is never recharged.
func (db *MessageDB) rebuildMQTTStorage(ctx context.Context) error {
	var total uint64
	var after ChannelKey
	for {
		catalog, next, more, err := db.listChannelsPage(ctx, after, 64)
		if err != nil {
			return err
		}
		for _, entry := range catalog {
			log, err := db.Channel(entry.Key, entry.ID)
			if err != nil {
				return err
			}
			n, err := log.rebuildMQTTStorage(ctx)
			_ = log.Close()
			if err != nil {
				return err
			}
			if n > math.MaxUint64-total {
				return dberrors.ErrCorruptState
			}
			total += n
		}
		if !more {
			break
		}
		after = next
	}
	db.mqttStorage.used = total
	return nil
}

func (l *ChannelLog) rebuildMQTTStorage(ctx context.Context) (uint64, error) {
	s, found, err := l.mqttStorageSource(nil)
	if err != nil || !found {
		return 0, err
	}
	leo, err := l.loadLEOLocked(ctx)
	if err != nil {
		return 0, err
	}
	floor := s.StartAfter
	retired, hasRetired, err := loadMQTTReplayRetired(l.db.engine, l.key)
	if err != nil {
		return 0, err
	}
	if hasRetired {
		floor = max(floor, retired.deletedThrough)
	}
	if floor > leo {
		return 0, dberrors.ErrCorruptState
	}
	ticket, hasTicket, err := loadMQTTStorageTicket(l.db.engine, l.key)
	if err != nil {
		return 0, err
	}
	span := keycodec.NewPrefixSpan(encodeMessageSystemPrefix(l.key, messageSystemIDMQTTStorage))
	batch := l.db.engine.NewBatch()
	defer func() { _ = batch.Close() }()
	if err = batch.DeleteRange(engine.Span{Start: span.Start, End: span.End}); err != nil {
		return 0, err
	}
	var total uint64
	var staged int
	for position := floor; position < leo; {
		position++
		if err = ctxErr(ctx); err != nil {
			return 0, err
		}
		v, exists, err := l.db.engine.Get(encodeMessageRowKey(l.key, position, 0))
		if err != nil {
			return 0, err
		}
		if !exists {
			r, err := loadMQTTReplayRecord(l.db.engine, l.key, s.Generation, position)
			if err != nil {
				return 0, err
			}
			v = r.Content
		}
		row, err := mqttReplayOriginalRow(l.key, position, v)
		if err != nil {
			return 0, err
		}
		n, err := l.channelEntry.mqttStorageRowCharge(row, s.Generation, nil)
		if err != nil {
			return 0, err
		}
		if n > math.MaxUint64-total {
			return 0, dberrors.ErrCorruptState
		}
		total += n
		if n != 0 {
			if err = stageMQTTStorageCharges(batch, l.key, []mqttStorageCharge{{position, n}}); err != nil {
				return 0, err
			}
		}
		staged++
		if staged == 256 {
			if err = batch.Commit(true); err != nil {
				return 0, err
			}
			_ = batch.Close()
			batch = l.db.engine.NewBatch()
			staged = 0
		}
	}
	if hasTicket && ticket.Phase == mqttStoragePrepared {
		if ticket.Manifest.BaseOffset < leo {
			original, present, err := loadDurableProposalFrom(l.db.engine, encodeProposalByCommandKey(l.key, ticket.Manifest.CommandID))
			if err != nil {
				return 0, err
			}
			if present {
				if original.manifest != ticket.Manifest {
					return 0, dberrors.ErrCorruptState
				}
				ticket.Phase = mqttStorageConsumed
			} else {
				ticket.Phase = mqttStorageCanceled
			}
			ticket.Charges = nil
			if err = stageMQTTStorageTicket(batch, l.key, ticket); err != nil {
				return 0, err
			}
		}
		for _, c := range ticket.Charges {
			if c.Bytes > math.MaxUint64-total {
				return 0, dberrors.ErrCorruptState
			}
			total += c.Bytes
			if err = stageMQTTStorageCharges(batch, l.key, []mqttStorageCharge{{c.Position, c.Bytes}}); err != nil {
				return 0, err
			}
		}
	}
	return total, batch.Commit(true)
}

func (e *channelEntry) stageMQTTStorageRelease(ctx context.Context, batch *engine.Batch, from, through uint64) (uint64, error) {
	if e.db.mqttStorage == nil || through < from {
		return 0, nil
	}
	var total uint64
	for position := from; ; position++ {
		if err := ctxErr(ctx); err != nil {
			return 0, err
		}
		k := mqttStorageChargeKey(e.key, position)
		v, exists, err := e.db.engine.Get(k)
		if err != nil {
			return 0, err
		}
		if !exists {
			if position == through {
				break
			}
			continue
		}
		n, err := decodeMQTTStorageCharge(k, v)
		if err != nil {
			return 0, err
		}
		if n > math.MaxUint64-total {
			return 0, dberrors.ErrCorruptState
		}
		total += n
		if err = batch.Delete(k); err != nil {
			return 0, err
		}
		if position == through {
			break
		}
	}
	return total, nil
}
