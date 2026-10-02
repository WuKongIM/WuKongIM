package cluster

import (
	"context"
	"errors"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// MQTTSourceNode is the foreground-gated distributed facade, never local storage.
type MQTTSourceNode interface {
	GetChannelRuntimeMetaFresh(context.Context, string, int64) (meta.ChannelRuntimeMeta, error)
	// ResolveChannelAppendAuthority reuses bounded Slot runtime initialization.
	// Its result may be cached and is not fresh source-protection authority.
	ResolveChannelAppendAuthority(context.Context, ch.ChannelID) (ch.Meta, error)
	EnsureChannelMQTTSource(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error)
}

type MQTTSourceProtectorOptions struct {
	Node MQTTSourceNode
	// MessageIDs must be the app's globally unique durable message-ID allocator.
	MessageIDs interface{ Next() uint64 }
	Now        func() time.Time
}

// MQTTSourceProtector translates one bounded source request. Confirmed absence
// initializes runtime infrastructure through the existing Channel service; it
// never creates business metadata, changes policy or retries uncertain effects.
type MQTTSourceProtector struct{ options MQTTSourceProtectorOptions }

func NewMQTTSourceProtector(o MQTTSourceProtectorOptions) (*MQTTSourceProtector, error) {
	if o.Node == nil || o.MessageIDs == nil {
		return nil, sessioncase.ErrInvalid
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &MQTTSourceProtector{options: o}, nil
}

func (p *MQTTSourceProtector) ProtectMQTTSource(ctx context.Context, id sessioncase.SourceChannel) (sessioncase.ProtectedSource, error) {
	if p == nil || ctx == nil || !contract.ValidIdentity(id.ID, 1024) || id.Type == 0 {
		return sessioncase.ProtectedSource{}, sessioncase.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return sessioncase.ProtectedSource{}, err
	}
	metadata, err := p.options.Node.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	if errors.Is(err, meta.ErrNotFound) {
		if err = ctx.Err(); err != nil {
			return sessioncase.ProtectedSource{}, err
		}
		if _, err = p.options.Node.ResolveChannelAppendAuthority(ctx, ch.ChannelID{ID: id.ID, Type: id.Type}); err != nil {
			return sessioncase.ProtectedSource{}, err
		}
		if err = ctx.Err(); err != nil {
			return sessioncase.ProtectedSource{}, err
		}
		// Initialization owns capacity, placement and coalescing. Only a new
		// foreground Slot read can supply the fences for subsequent protection.
		metadata, err = p.options.Node.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	}
	if err != nil {
		return sessioncase.ProtectedSource{}, err
	}
	if err = ctx.Err(); err != nil {
		return sessioncase.ProtectedSource{}, err
	}
	if metadata.ChannelID != id.ID || metadata.ChannelType != int64(id.Type) || metadata.ChannelEpoch == 0 || metadata.LeaderEpoch == 0 || metadata.RouteGeneration == 0 {
		return sessioncase.ProtectedSource{}, sessioncase.ErrEvidence
	}
	req := ch.MQTTSourceRequest{ChannelID: ch.ChannelID{ID: id.ID, Type: id.Type}, ExpectedChannelEpoch: metadata.ChannelEpoch, ExpectedLeaderEpoch: metadata.LeaderEpoch, ExpectedRouteGeneration: metadata.RouteGeneration, MessageID: p.options.MessageIDs.Next(), ServerTimestampMS: p.options.Now().UnixMilli()}
	if !req.Valid() {
		return sessioncase.ProtectedSource{}, sessioncase.ErrEvidence
	}
	source, err := p.options.Node.EnsureChannelMQTTSource(ctx, req)
	if err != nil {
		return sessioncase.ProtectedSource{}, err
	}
	if err = ctx.Err(); err != nil {
		return sessioncase.ProtectedSource{}, err
	}
	if !contract.ValidIdentity(source.Generation, 128) || source.StartAfter >= source.CommittedThrough {
		return sessioncase.ProtectedSource{}, sessioncase.ErrEvidence
	}
	return sessioncase.ProtectedSource{Channel: id, Generation: source.Generation, ProtectedAfter: source.StartAfter, CommittedThrough: source.CommittedThrough}, nil
}

var _ sessioncase.SourceProtector = (*MQTTSourceProtector)(nil)
