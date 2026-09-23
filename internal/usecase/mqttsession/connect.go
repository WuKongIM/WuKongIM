package mqttsession

import (
	"bytes"
	"context"
	"errors"
	"math"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	owner "github.com/WuKongIM/WuKongIM/internal/runtime/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// Connect authenticates, isolates the observed old owner, commits once and then
// opens local execution. A conflict never loops through further owner evictions.
func (a *App) Connect(ctx context.Context, c ConnectCommand) (out Connection, err error) {
	if a == nil || ctx == nil {
		return out, ErrInvalid
	}
	if err = validateConnect(c); err != nil {
		return out, err
	}
	if c.Will != nil {
		w := *c.Will
		w.Payload = bytes.Clone(w.Payload)
		w.PublicationMetadata = bytes.Clone(w.PublicationMetadata)
		c.Will = &w
	}
	if err = a.authorize(ctx, c); err != nil {
		return out, err
	}
	old, found, err := a.read(ctx, c.Key)
	if err != nil {
		return out, err
	}
	if found && old.UID != c.UID {
		return out, ErrBinding
	}
	now, err := a.now()
	if err != nil {
		return out, err
	}
	if found && (now.UnixMilli() < old.UpdatedAtMS || old.Revision == math.MaxUint64 || old.OwnerGeneration == math.MaxUint64) {
		return out, ErrClock
	}
	if found {
		observed := sessionOwner(old)
		if err = a.opts.Isolation.Quiesce(ctx, observed); err != nil {
			return out, err
		}
		var stillFound bool
		old, stillFound, err = a.read(ctx, c.Key)
		if err != nil {
			return out, err
		}
		if !stillFound || sessionOwner(old) != observed {
			return out, ErrConflict
		}
		now, err = a.now()
		if err != nil {
			return out, err
		}
		if now.UnixMilli() < old.UpdatedAtMS {
			return out, ErrClock
		}
		if old.State == meta.MQTTSessionActive && old.LeaseUntilMS <= now.UnixMilli() {
			// This records the lost execution boundary before reconnect can decide
			// whether the old Will is still cancellable or the lifetime has expired.
			if old.LeaseUntilMS < old.UpdatedAtMS {
				return out, ErrClock
			}
			if err = a.disconnectRow(ctx, old, old.LeaseUntilMS, false, old.SessionExpirySec); err != nil {
				return out, err
			}
			previousRevision := old.Revision
			old, stillFound, err = a.read(ctx, c.Key)
			if err != nil {
				return out, err
			}
			if !stillFound || sessionOwner(old) != observed || old.Revision != previousRevision+1 {
				return out, ErrConflict
			}
		}
	}
	// Isolation can outlast token/permission validity. Recheck before proposing.
	if err = a.authorize(ctx, c); err != nil {
		return out, err
	}
	started, err := a.now()
	if err != nil {
		return out, err
	}
	if found && started.UnixMilli() < old.UpdatedAtMS {
		return out, ErrClock
	}
	fresh := !found || c.CleanStart || old.State == meta.MQTTSessionEnded || old.State == meta.MQTTSessionOffline && old.OfflineExpiresAtMS <= started.UnixMilli() || old.State == meta.MQTTSessionActive && old.SessionExpirySec == 0
	generation, ownerGeneration := uint64(1), uint64(1)
	if found {
		generation, ownerGeneration = old.Generation, old.OwnerGeneration+1
		if fresh {
			if generation == math.MaxUint64 {
				return out, ErrEvidence
			}
			generation++
		}
	}
	if ownerGeneration == 0 || old.Revision == math.MaxUint64 {
		return out, ErrEvidence
	}
	local, err := a.opts.Owners.Reserve(owner.Claim{Key: c.Key, UID: c.UID, SessionGeneration: generation, OwnerGeneration: ownerGeneration}, c.CloseTransport)
	if err != nil {
		return out, err
	}
	activated := false
	defer func() {
		if !activated {
			err = errors.Join(err, a.abandon(local))
		}
	}()
	until := started.Add(a.opts.LeaseDuration)
	next := old
	if fresh {
		next = meta.MQTTSession{Namespace: c.Key.Namespace, ClientID: c.Key.ClientID, UID: c.UID, NextPacketID: 1, NextDeliveryOrder: 1, QuotaMessages: a.opts.QuotaMessages, QuotaBytes: a.opts.QuotaBytes}
	}
	next.Generation, next.Revision = generation, old.Revision+1
	next.OwnerGeneration, next.OwnerNodeID, next.OwnerBootID, next.ConnectionID = local.OwnerGeneration, local.NodeID, local.BootID, local.ConnectionID
	next.State, next.LeaseUntilMS, next.OfflineExpiresAtMS = meta.MQTTSessionActive, leaseUpperBoundMS(until), 0
	next.SessionExpirySec = min(c.SessionExpirySec, a.opts.SessionExpiryLimitSec)
	next.DeviceFlag, next.ReceiveMaximum, next.MaxPacketBytes = uint8(c.DeviceFlag), c.ReceiveMaximum, c.MaxPacketBytes
	next.WindowLimit, next.TerminationReason, next.UpdatedAtMS = a.opts.WindowLimit, 0, started.UnixMilli()
	next.WillGeneration, next.LastLifecycleDigest = 0, ""
	m := lifecycle(old, found, next, meta.MQTTLifecycleConnect)
	m.CleanStart = c.CleanStart
	if c.Will != nil {
		m.Will, err = installWill(next, *c.Will)
		if err != nil {
			return out, err
		}
	}
	r, err := a.commit(ctx, m)
	if err != nil {
		return out, err
	}
	if err = a.opts.Owners.Activate(local, r.CurrentRevision, until); err != nil {
		return out, err
	}
	activated = true
	return Connection{Owner: local, UID: c.UID, DeviceFlag: c.DeviceFlag, SessionPresent: !fresh, SessionExpirySec: next.SessionExpirySec, WillGeneration: r.WillGeneration, Lease: Lease{Revision: r.CurrentRevision, Until: until}}, nil
}

func validateConnect(c ConnectCommand) error {
	if c.Key.Validate() != nil || !contract.ValidIdentity(c.UID, 1024) || len(c.Token) == 0 || len(c.Token) > 16<<10 || c.DeviceFlag > 2 || c.ReceiveMaximum == 0 || c.MaxPacketBytes == 0 || c.CloseTransport == nil {
		return ErrInvalid
	}
	if c.Will == nil {
		return nil
	}
	w := c.Will
	if !contract.ValidIdentity(w.Topic, 2048) || !contract.ValidIdentity(w.TargetID, 1024) || (w.TargetType != 1 && w.TargetType != 2) || !contract.ValidIdentity(w.ClientMsgNo, 1024) || w.QoS > 1 || len(w.Payload) > 65535 || len(w.PublicationMetadata) > publication.MaxWillTemplateBytes {
		return ErrInvalid
	}
	p, e := publication.Decode(w.PublicationMetadata)
	if e != nil || p.Source != publication.SourceWill || p.QoS != w.QoS || p.PublisherNamespace != c.Key.Namespace || p.PublisherClientID != c.Key.ClientID || p.OriginalTopic != w.Topic || p.ServerWillKey != "" || p.AcceptedAtMS != 0 {
		return ErrInvalid
	}
	return nil
}

func (a *App) authorize(ctx context.Context, c ConnectCommand) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := a.opts.Tokens.VerifyToken(ctx, c.UID, c.DeviceFlag, c.Token); err != nil {
		return err
	}
	if c.Will != nil {
		return a.opts.Wills.AuthorizeWill(ctx, c.UID, c.Will.WillTarget)
	}
	return nil
}

func installWill(s meta.MQTTSession, w Will) (*meta.MQTTWill, error) {
	key := meta.MQTTWillKey{Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.Generation, WillGeneration: s.Revision}
	id, err := meta.MQTTWillIdempotencyKey(key)
	if err != nil {
		return nil, ErrInvalid
	}
	out := &meta.MQTTWill{Key: key, UID: s.UID, OwnerGeneration: s.OwnerGeneration, OwnerNodeID: s.OwnerNodeID, OwnerBootID: s.OwnerBootID, ConnectionID: s.ConnectionID, Revision: 1, DecisionRevision: s.Revision, Topic: w.Topic, TargetID: w.TargetID, TargetType: w.TargetType, Payload: w.Payload, PublicationMetadata: w.PublicationMetadata, DelaySeconds: w.DelaySeconds, QoS: w.QoS, ClientMsgNo: w.ClientMsgNo, IdempotencyKey: id, Stage: meta.MQTTWillArmed, UpdatedAtMS: s.UpdatedAtMS}
	if meta.ValidateMQTTWill(*out) != nil {
		return nil, ErrInvalid
	}
	return out, nil
}
