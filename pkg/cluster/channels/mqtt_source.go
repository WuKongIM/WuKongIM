package channels

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// FreshChannelMetaSource resolves a point read after a fresh Slot quorum/apply barrier.
// Source admission must not substitute ordinary metadata or append caches.
type FreshChannelMetaSource interface {
	ResolveChannelMetaFresh(context.Context, ch.ChannelID) (ch.Meta, error)
}

// RuntimeMetaFreshReader supplies the stronger Slot point-read contract.
type RuntimeMetaFreshReader interface {
	GetChannelRuntimeMetaFresh(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
}

// ResolveChannelMetaFresh preserves exact identity without create-if-missing.
func (s *SlotMetaSource) ResolveChannelMetaFresh(ctx context.Context, id ch.ChannelID) (ch.Meta, error) {
	if err := ctxErr(ctx); err != nil {
		return ch.Meta{}, err
	}
	if s == nil {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	reader, ok := s.reader.(RuntimeMetaFreshReader)
	if !ok {
		return ch.Meta{}, ch.ErrInvalidConfig
	}
	meta, err := reader.GetChannelRuntimeMetaFresh(ctx, id.ID, int64(id.Type))
	if errors.Is(err, metadb.ErrNotFound) {
		return ch.Meta{}, ch.ErrChannelNotFound
	}
	if err != nil {
		return ch.Meta{}, err
	}
	if meta.ChannelID != id.ID || meta.ChannelType != int64(id.Type) {
		return ch.Meta{}, ch.ErrStaleMeta
	}
	return projectRuntimeMeta(meta), nil
}

type mqttSourceForwardRequest struct {
	// Leader binds the request to a single serving node; forwarding cannot recurse.
	Leader  ch.NodeID
	Request ch.MQTTSourceRequest
}

type mqttSourceForwarder interface {
	ForwardMQTTSource(context.Context, ch.NodeID, ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error)
}

func validMQTTSourceRequest(req ch.MQTTSourceRequest) bool {
	return req.Valid() && len(req.ChannelID.ID) <= 1024 && utf8.ValidString(req.ChannelID.ID) && !strings.ContainsRune(req.ChannelID.ID, 0)
}

func validMQTTSourceSnapshot(source ch.MQTTSourceSnapshot) bool {
	var command ch.CommandID
	const prefix = "mqtt-log-v1:"
	if len(source.Generation) != len(prefix)+hex.EncodedLen(len(command)) || !strings.HasPrefix(source.Generation, prefix) {
		return false
	}
	if _, err := hex.Decode(command[:], []byte(source.Generation[len(prefix):])); err != nil {
		return false
	}
	return source.Generation == quorumlog.MQTTSourceGeneration(command) && source.StartAfter < source.CommittedThrough
}

// EnsureMQTTSource establishes or confirms protection on the exact current leader.
// Both authority reads are fresh; the returned boundary is not a subscription receipt.
func (s *Service) EnsureMQTTSource(ctx context.Context, req ch.MQTTSourceRequest) (ch.MQTTSourceSnapshot, error) {
	return s.ensureMQTTSource(ctx, req, 0)
}

func (s *Service) handleForwardMQTTSource(ctx context.Context, req mqttSourceForwardRequest) (ch.MQTTSourceSnapshot, error) {
	if s == nil || req.Leader == 0 || req.Leader != s.localNode {
		return ch.MQTTSourceSnapshot{}, ch.ErrNotLeader
	}
	return s.ensureMQTTSource(ctx, req.Request, req.Leader)
}

func (s *Service) ensureMQTTSource(ctx context.Context, req ch.MQTTSourceRequest, serving ch.NodeID) (ch.MQTTSourceSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if s == nil || !validMQTTSourceRequest(req) {
		return ch.MQTTSourceSnapshot{}, ch.ErrInvalidConfig
	}
	reader, ok := s.metaSource.(FreshChannelMetaSource)
	if !ok {
		return ch.MQTTSourceSnapshot{}, ch.ErrInvalidConfig
	}
	meta, err := reader.ResolveChannelMetaFresh(ctx, req.ChannelID)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if err = validateMQTTSourceAuthority(req, meta); err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if serving != 0 && meta.Leader != serving {
		return ch.MQTTSourceSnapshot{}, ch.ErrNotLeader
	}
	var source ch.MQTTSourceSnapshot
	if meta.Leader == s.localNode {
		activator, ok := s.runtime.(ch.MQTTSourceActivator)
		if !ok {
			return ch.MQTTSourceSnapshot{}, ch.ErrInvalidConfig
		}
		if err = s.applyRuntimeMetaContext(ctx, meta, true, true); err != nil {
			return ch.MQTTSourceSnapshot{}, err
		}
		source, err = activator.EnsureMQTTSource(ctx, req)
	} else {
		forward, ok := s.forward.(mqttSourceForwarder)
		if !ok {
			return ch.MQTTSourceSnapshot{}, ch.ErrInvalidConfig
		}
		source, err = forward.ForwardMQTTSource(ctx, meta.Leader, req)
	}
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if err = ctx.Err(); err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	current, err := reader.ResolveChannelMetaFresh(ctx, req.ChannelID)
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if err = validateMQTTSourceAuthority(req, current); err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if current.Leader != meta.Leader {
		return ch.MQTTSourceSnapshot{}, ch.ErrStaleMeta
	}
	if err = ctx.Err(); err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	if !validMQTTSourceSnapshot(source) {
		return ch.MQTTSourceSnapshot{}, ch.ErrLogConflict
	}
	return source, nil
}

func validateMQTTSourceAuthority(req ch.MQTTSourceRequest, meta ch.Meta) error {
	return validateMQTTChannelAuthority(req.ChannelID, req.ExpectedChannelEpoch, req.ExpectedLeaderEpoch, req.ExpectedRouteGeneration, meta)
}

// validateMQTTChannelAuthority requires a valid exact placement before either
// source activation or replay preparation can reach the Channel runtime.
func validateMQTTChannelAuthority(id ch.ChannelID, epoch, leaderEpoch, route uint64, meta ch.Meta) error {
	if !cacheableAppendMeta(id, meta) {
		return ch.ErrNotReady
	}
	for _, members := range [][]ch.NodeID{meta.Replicas, meta.ISR} {
		seen := make(map[ch.NodeID]struct{}, len(members))
		for _, node := range members {
			if _, exists := seen[node]; node == 0 || exists {
				return ch.ErrNotReady
			}
			seen[node] = struct{}{}
		}
	}
	if meta.Epoch != epoch || meta.LeaderEpoch != leaderEpoch || meta.RouteGeneration != route {
		return ch.ErrStaleMeta
	}
	if meta.WriteFence.Set() {
		return ch.ErrWriteFenced
	}
	return nil
}

func (g *ServiceGateway) handleForwardMQTTSource(ctx context.Context, req mqttSourceForwardRequest) (ch.MQTTSourceSnapshot, error) {
	s, err := g.service()
	if err != nil {
		return ch.MQTTSourceSnapshot{}, err
	}
	return s.handleForwardMQTTSource(ctx, req)
}

var _ ch.MQTTSourceActivator = (*Service)(nil)
var _ FreshChannelMetaSource = (*SlotMetaSource)(nil)
