package delivery

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	gatewaysession "github.com/WuKongIM/WuKongIM/pkg/gateway/session"
	gatewaytransport "github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

var errRecvMessageIDOverflow = errors.New("internal/infra/delivery: delivery message id overflows recv packet")

const pendingAckExpiryInterval = time.Second
const pendingAckExpiryInProgress = int64(1<<63 - 1)

// LocalOwnerPusherOptions configures owner-local delivery adaptation.
type LocalOwnerPusherOptions struct {
	// Online resolves exact active owner-local gateway sessions.
	Online *online.Registry
	// AckManager owns pending RECVACK reservations and cleanup.
	AckManager *runtimedelivery.Manager
	// PendingAckTTL bounds stale pending RECVACK retention during delivery activity.
	PendingAckTTL time.Duration
	// Now supplies the clock used by the bounded pending-ACK expiry scheduler.
	Now func() time.Time
	// Logger records bounded owner-local delivery failures.
	Logger wklog.Logger
}

// LocalOwnerPusher adapts runtime delivery commands to exact owner-local
// gateway sessions while preserving pending-ACK transaction semantics.
type LocalOwnerPusher struct {
	// online resolves owner-local concrete sessions.
	online *online.Registry
	// delivery tracks pending recvacks after successful local writes.
	delivery *runtimedelivery.Manager
	// pendingAckTTL bounds stale pending recvack cleanup during delivery activity.
	pendingAckTTL time.Duration
	// now provides the wall clock for bounded pending-ack expiry scheduling.
	now func() time.Time
	// pendingAckExpiryNext gates the O(pending acks) expiry scan to one caller per interval.
	pendingAckExpiryNext atomic.Int64
	// logger records owner-local delivery failures before they become retryable or dropped results.
	logger wklog.Logger
}

var _ runtimedelivery.Pusher = (*LocalOwnerPusher)(nil)

// NewLocalOwnerPusher creates an owner-local delivery adapter.
func NewLocalOwnerPusher(opts LocalOwnerPusherOptions) *LocalOwnerPusher {
	return &LocalOwnerPusher{
		online:        opts.Online,
		delivery:      opts.AckManager,
		pendingAckTTL: opts.PendingAckTTL,
		now:           opts.Now,
		logger:        opts.Logger,
	}
}

// SetAckManager installs the pending-ACK manager after delivery fanout wiring.
// It must be called before concurrent Push calls begin.
func (p *LocalOwnerPusher) SetAckManager(manager *runtimedelivery.Manager) {
	if p == nil {
		return
	}
	p.delivery = manager
}

// localOwnerDuplicateAckStackRoutes covers both the channelappend default
// owner-push batch (256) and the delivery runtime default push batch (512)
// without adding a heap allocation to the unique-route fast path.
const localOwnerDuplicateAckStackRoutes = 512

type localOwnerAckKey struct {
	uid       string
	sessionID uint64
	messageID uint64
}

// Push writes one owner-node batch to exact active local sessions.
func (p *LocalOwnerPusher) Push(_ context.Context, cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	p.expirePendingAcksIfDue()
	if len(cmd.Routes) == 1 {
		return p.pushSingleRoute(cmd)
	}
	payload := append([]byte(nil), cmd.Envelope.Payload...)
	timestamp := int32(time.Now().Unix())
	var result runtimedelivery.PushResult
	pendings := make([]runtimedelivery.PendingRecvAck, len(cmd.Routes))
	var duplicateAckStack [localOwnerDuplicateAckStackRoutes]bool
	var duplicateAcks []bool
	if len(cmd.Routes) <= len(duplicateAckStack) {
		duplicateAcks = duplicateAckStack[:len(cmd.Routes)]
	} else {
		duplicateAcks = make([]bool, len(cmd.Routes))
	}
	var ackKeyBuckets [4]uint64
	validCount := 0
	for i, route := range cmd.Routes {
		_, ok := p.localSession(route)
		if !ok {
			continue
		}
		if cmd.Envelope.MessageID > uint64(1<<63-1) {
			p.loggerOrNop().Warn("delivery recv packet build failed",
				wklog.Event("internal.app.delivery.recv_packet_build_failed"),
				wklog.UID(route.UID),
				wklog.SessionID(route.SessionID),
				wklog.ChannelID(cmd.Envelope.ChannelID),
				wklog.ChannelType(int64(cmd.Envelope.ChannelType)),
				wklog.Uint64("messageID", cmd.Envelope.MessageID),
				wklog.MessageSeq(cmd.Envelope.MessageSeq),
				wklog.Error(errRecvMessageIDOverflow),
			)
			continue
		}
		pendings[i] = runtimedelivery.PendingRecvAck{
			UID:         route.UID,
			SessionID:   route.SessionID,
			MessageID:   cmd.Envelope.MessageID,
			MessageSeq:  cmd.Envelope.MessageSeq,
			ChannelID:   cmd.Envelope.ChannelID,
			ChannelType: cmd.Envelope.ChannelType,
		}
		bucket := localOwnerAckKeyBucket(route.SessionID, cmd.Envelope.MessageID)
		word := bucket / 64
		bit := uint64(1) << (bucket % 64)
		if ackKeyBuckets[word]&bit != 0 {
			duplicateAcks[i] = localOwnerHasPriorAckKey(pendings[:i], localOwnerAckKey{
				uid:       route.UID,
				sessionID: route.SessionID,
				messageID: cmd.Envelope.MessageID,
			})
		}
		ackKeyBuckets[word] |= bit
		validCount++
	}

	var bindResult runtimedelivery.AckBindBatchResult
	if p.delivery != nil && validCount > 0 {
		bindResult = p.delivery.BindPendingAcks(pendings)
	}
	var acceptedIndexStack [localOwnerDuplicateAckStackRoutes]int
	var acceptedIndexes []int
	rollbackCount := 0
	if p.delivery != nil {
		if len(cmd.Routes) <= len(acceptedIndexStack) {
			acceptedIndexes = acceptedIndexStack[:0]
		} else {
			acceptedIndexes = make([]int, 0, len(cmd.Routes))
		}
	}
	for i, route := range cmd.Routes {
		pending := pendings[i]
		if pending.UID == "" {
			result.Dropped = append(result.Dropped, route)
			continue
		}
		if p.delivery != nil && (i >= len(bindResult.Tokens) || !bindResult.Tokens[i].Valid()) {
			p.loggerOrNop().Warn("delivery pending ack limit reached",
				wklog.Event("internal.app.delivery.pending_ack_limit_reached"),
				wklog.UID(route.UID),
				wklog.SessionID(route.SessionID),
				wklog.ChannelID(cmd.Envelope.ChannelID),
				wklog.ChannelType(int64(cmd.Envelope.ChannelType)),
				wklog.Uint64("messageID", cmd.Envelope.MessageID),
				wklog.MessageSeq(cmd.Envelope.MessageSeq),
			)
			result.Dropped = append(result.Dropped, route)
			continue
		}
		var bindToken runtimedelivery.AckBindToken
		if p.delivery != nil {
			bindToken = bindResult.Tokens[i]
		}
		// Duplicate recipient rows intentionally produce duplicate writes, but all
		// writes for one UID/session/message share the same recvack key. A prior
		// duplicate can fail or receive a fast ACK after the batch bind, so refresh
		// only later duplicate keys immediately before their write. Unique routes
		// retain the one-lock batch fast path.
		if p.delivery != nil && duplicateAcks[i] {
			// This duplicate receives a fresh just-before-write reservation. Drop
			// its earlier batch reservation first so a failed duplicate cannot
			// leave an unconfirmed token behind when another write already owns
			// the shared identity.
			if p.rollbackPendingAck(pending, bindToken) {
				rollbackCount++
			}
			duplicateBind := p.delivery.BindPendingAckResult(pending)
			if !duplicateBind.Bound {
				p.loggerOrNop().Warn("delivery pending ack limit reached",
					wklog.Event("internal.app.delivery.pending_ack_limit_reached"),
					wklog.UID(route.UID),
					wklog.SessionID(route.SessionID),
					wklog.ChannelID(cmd.Envelope.ChannelID),
					wklog.ChannelType(int64(cmd.Envelope.ChannelType)),
					wklog.Uint64("messageID", cmd.Envelope.MessageID),
					wklog.MessageSeq(cmd.Envelope.MessageSeq),
				)
				result.Dropped = append(result.Dropped, route)
				continue
			}
			bindToken = duplicateBind.Token
			bindResult.Tokens[i] = bindToken
		}
		// Revalidate after the final bind for this exact attempt. In particular,
		// duplicate rebind observers or a concurrent close may unregister the
		// route after the original batch reservation was created.
		session, ok := p.localSession(route)
		if !ok {
			if p.rollbackPendingAck(pending, bindToken) {
				rollbackCount++
			}
			result.Dropped = append(result.Dropped, route)
			continue
		}
		packet, err := buildRecvPacket(cmd.Envelope, route.UID, payload, timestamp)
		if err != nil {
			if p.rollbackPendingAck(pending, bindToken) {
				rollbackCount++
			}
			result.Dropped = append(result.Dropped, route)
			continue
		}
		if err := session.Session.WriteDelivery(packet); err != nil {
			if p.rollbackPendingAck(pending, bindToken) {
				rollbackCount++
			}
			terminal := terminalLocalDeliveryWriteError(err)
			p.loggerOrNop().Warn("delivery write failed",
				wklog.Event("internal.app.delivery.write_failed"),
				wklog.UID(route.UID),
				wklog.SessionID(route.SessionID),
				wklog.ChannelID(cmd.Envelope.ChannelID),
				wklog.ChannelType(int64(cmd.Envelope.ChannelType)),
				wklog.Uint64("messageID", cmd.Envelope.MessageID),
				wklog.MessageSeq(cmd.Envelope.MessageSeq),
				wklog.Bool("terminal", terminal),
				wklog.Error(err),
			)
			if terminal {
				result.Dropped = append(result.Dropped, route)
			} else {
				result.Retryable = append(result.Retryable, route)
			}
			continue
		}
		result.Accepted = append(result.Accepted, route)
		if p.delivery != nil {
			acceptedIndexes = append(acceptedIndexes, i)
		}
	}
	if p.delivery != nil && validCount > 0 {
		p.delivery.FinishPendingAcks(pendings, bindResult.Tokens, acceptedIndexes, rollbackCount)
	}
	return result, nil
}

// pushSingleRoute keeps the common one-route owner push on the allocation-free
// single-bind path while preserving existing pending state on a failed refresh.
func (p *LocalOwnerPusher) pushSingleRoute(cmd runtimedelivery.PushCommand) (runtimedelivery.PushResult, error) {
	route := cmd.Routes[0]
	var result runtimedelivery.PushResult
	payload := append([]byte(nil), cmd.Envelope.Payload...)
	pending := runtimedelivery.PendingRecvAck{
		UID:         route.UID,
		SessionID:   route.SessionID,
		MessageID:   cmd.Envelope.MessageID,
		MessageSeq:  cmd.Envelope.MessageSeq,
		ChannelID:   cmd.Envelope.ChannelID,
		ChannelType: cmd.Envelope.ChannelType,
	}
	packet, err := buildRecvPacket(cmd.Envelope, route.UID, payload, int32(time.Now().Unix()))
	if err != nil {
		p.loggerOrNop().Warn("delivery recv packet build failed",
			wklog.Event("internal.app.delivery.recv_packet_build_failed"),
			wklog.UID(route.UID),
			wklog.SessionID(route.SessionID),
			wklog.ChannelID(cmd.Envelope.ChannelID),
			wklog.ChannelType(int64(cmd.Envelope.ChannelType)),
			wklog.Uint64("messageID", cmd.Envelope.MessageID),
			wklog.MessageSeq(cmd.Envelope.MessageSeq),
			wklog.Error(err),
		)
		result.Dropped = append(result.Dropped, route)
		return result, nil
	}
	var bindToken runtimedelivery.AckBindToken
	if p.delivery != nil {
		bindResult := p.delivery.BindPendingAckResult(pending)
		if !bindResult.Bound {
			p.loggerOrNop().Warn("delivery pending ack limit reached",
				wklog.Event("internal.app.delivery.pending_ack_limit_reached"),
				wklog.UID(route.UID),
				wklog.SessionID(route.SessionID),
				wklog.ChannelID(cmd.Envelope.ChannelID),
				wklog.ChannelType(int64(cmd.Envelope.ChannelType)),
				wklog.Uint64("messageID", cmd.Envelope.MessageID),
				wklog.MessageSeq(cmd.Envelope.MessageSeq),
			)
			result.Dropped = append(result.Dropped, route)
			return result, nil
		}
		bindToken = bindResult.Token
	}
	session, ok := p.localSession(route)
	if !ok {
		p.rollbackPendingAck(pending, bindToken)
		result.Dropped = append(result.Dropped, route)
		return result, nil
	}
	if err := session.Session.WriteDelivery(packet); err != nil {
		p.rollbackPendingAck(pending, bindToken)
		terminal := terminalLocalDeliveryWriteError(err)
		p.loggerOrNop().Warn("delivery write failed",
			wklog.Event("internal.app.delivery.write_failed"),
			wklog.UID(route.UID),
			wklog.SessionID(route.SessionID),
			wklog.ChannelID(cmd.Envelope.ChannelID),
			wklog.ChannelType(int64(cmd.Envelope.ChannelType)),
			wklog.Uint64("messageID", cmd.Envelope.MessageID),
			wklog.MessageSeq(cmd.Envelope.MessageSeq),
			wklog.Bool("terminal", terminal),
			wklog.Error(err),
		)
		if terminal {
			result.Dropped = append(result.Dropped, route)
		} else {
			result.Retryable = append(result.Retryable, route)
		}
		return result, nil
	}
	if p.delivery != nil {
		p.delivery.FinishPendingAck(pending, bindToken)
	}
	result.Accepted = append(result.Accepted, route)
	return result, nil
}

func (p *LocalOwnerPusher) rollbackPendingAck(pending runtimedelivery.PendingRecvAck, token runtimedelivery.AckBindToken) bool {
	if p == nil || p.delivery == nil {
		return false
	}
	// A miss is expected when a fast client Recvack or session close already
	// consumed the identity, so rollback stays silent and idempotent to callers.
	return p.delivery.RollbackPendingAck(pending, token)
}

func localOwnerAckKeyBucket(sessionID, messageID uint64) uint64 {
	mixed := sessionID ^ (sessionID >> 32) ^ messageID ^ (messageID >> 32)
	return mixed & 255
}

func localOwnerHasPriorAckKey(pendings []runtimedelivery.PendingRecvAck, key localOwnerAckKey) bool {
	for i := len(pendings) - 1; i >= 0; i-- {
		pending := pendings[i]
		if pending.UID == key.uid && pending.SessionID == key.sessionID && pending.MessageID == key.messageID {
			return true
		}
	}
	return false
}

func (p *LocalOwnerPusher) expirePendingAcksIfDue() {
	if p == nil || p.pendingAckTTL <= 0 || p.delivery == nil {
		return
	}
	now := p.nowTime()
	for {
		next := p.pendingAckExpiryNext.Load()
		if next == pendingAckExpiryInProgress || now.UnixNano() < next {
			return
		}
		if p.pendingAckExpiryNext.CompareAndSwap(next, pendingAckExpiryInProgress) {
			break
		}
	}
	defer func() {
		p.pendingAckExpiryNext.Store(p.nowTime().Add(pendingAckExpiryInterval).UnixNano())
	}()
	p.delivery.ExpirePendingAcks(p.pendingAckTTL)
}

func (p *LocalOwnerPusher) nowTime() time.Time {
	if p != nil && p.now != nil {
		return p.now()
	}
	return time.Now()
}

func (p *LocalOwnerPusher) loggerOrNop() wklog.Logger {
	if p.logger == nil {
		return wklog.NewNop()
	}
	return p.logger
}

func (p *LocalOwnerPusher) localSession(route runtimedelivery.Route) (online.LocalSession, bool) {
	if p.online == nil || route.UID == "" || route.SessionID == 0 || route.OwnerNodeID == 0 || route.OwnerBootID == 0 || route.OwnerSeq == 0 {
		return online.LocalSession{}, false
	}
	session, ok := p.online.LocalSession(route.SessionID)
	if !ok || session.State != online.RouteStateActive || session.Session == nil {
		return online.LocalSession{}, false
	}
	local := session.Route
	if local.UID != route.UID || local.SessionID != route.SessionID {
		return online.LocalSession{}, false
	}
	if local.OwnerNodeID != route.OwnerNodeID || local.OwnerBootID != route.OwnerBootID || local.OwnerSeq != route.OwnerSeq {
		return online.LocalSession{}, false
	}
	return session, true
}

func terminalLocalDeliveryWriteError(err error) bool {
	return errors.Is(err, gatewaysession.ErrSessionClosed) ||
		errors.Is(err, gatewaytransport.ErrOutboundBytesExceeded)
}

func buildRecvPacket(env runtimedelivery.Envelope, uid string, payload []byte, timestamp int32) (*frame.RecvPacket, error) {
	if env.MessageID > uint64(1<<63-1) {
		return nil, errRecvMessageIDOverflow
	}
	channelID := env.ChannelID
	if env.ChannelType == frame.ChannelTypePerson {
		channelID = recipientPersonChannelView(env, uid)
	}
	return &frame.RecvPacket{
		Framer: frame.Framer{
			RedDot: env.RedDot,
		},
		MessageID:   int64(env.MessageID),
		MessageSeq:  env.MessageSeq,
		ClientMsgNo: env.ClientMsgNo,
		Timestamp:   timestamp,
		ChannelID:   channelID,
		ChannelType: env.ChannelType,
		FromUID:     env.FromUID,
		Payload:     payload,
	}, nil
}

func recipientPersonChannelView(env runtimedelivery.Envelope, recipientUID string) string {
	if recipientUID == "" {
		return env.ChannelID
	}
	left, right, err := runtimechannelid.DecodePersonChannel(env.ChannelID)
	if err != nil {
		return env.FromUID
	}
	switch recipientUID {
	case left:
		return right
	case right:
		return left
	default:
		return env.FromUID
	}
}
