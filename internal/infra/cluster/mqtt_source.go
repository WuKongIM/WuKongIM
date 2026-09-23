package cluster

import (
	"context"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	sessioncase "github.com/WuKongIM/WuKongIM/internal/usecase/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// MQTTSourceNode is the foreground-gated distributed facade, never local storage.
type MQTTSourceNode interface {
	GetChannelRuntimeMetaFresh(context.Context, string, int64) (meta.ChannelRuntimeMeta, error)
	EnsureChannelMQTTSource(context.Context, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error)
}

type MQTTSourceProtectorOptions struct {
	Node MQTTSourceNode
	// MessageIDs must be the app's globally unique durable message-ID allocator.
	MessageIDs interface{ Next() uint64 }
	Now        func() time.Time
}

// MQTTSourceProtector translates one bounded source request without policy,
// metadata creation, retries or authority fallback.
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
