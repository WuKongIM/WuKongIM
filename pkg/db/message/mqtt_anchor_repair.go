package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// committedMQTTReplayPrefix derives expected content only from this replica's
// installed committed journal. A donor page is never an input to this proof.
func committedMQTTReplayPrefix(view proposalReadView, key ChannelKey, position uint64) (MQTTReplayState, error) {
	proof, found, err := loadMQTTReplayAnchorFrom(view, key, position)
	if err != nil {
		return MQTTReplayState{}, err
	}
	if !found {
		return MQTTReplayState{}, dberrors.ErrConflict
	}
	a := proof.Anchor
	return MQTTReplayState{Generation: quorumlog.MQTTSourceGeneration(a.SourceCommand), StartAfter: a.StartAfter,
		Through: a.Through, TotalBytes: a.TotalBytes, TotalStoredBytes: a.TotalStoredBytes, Digest: a.Digest}, nil
}

// ExportMQTTReplayAnchor exports a complete bounded interval ending at one
// locally committed anchor. It rejects short pages: their endpoint would not be
// authenticated by that anchor. Original history and current membership are
// unnecessary; routing, release and learner readiness belong to the caller.
func (l *ChannelLog) ExportMQTTReplayAnchor(ctx context.Context, position, from uint64, opts ReadOptions) (MQTTReplayTransfer, error) {
	if position == 0 || from == 0 || from >= position || opts.Limit <= 0 || opts.Limit > mqttReplayMaxRows || opts.MaxBytes <= 0 || opts.MaxBytes > mqttReplayMaxBytes {
		return MQTTReplayTransfer{}, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayTransfer{}, err
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayTransfer{}, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	defer view.Close()
	expected, err := committedMQTTReplayPrefix(view, l.key, position)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	if from <= expected.StartAfter || from > expected.Through || expected.Through-from >= uint64(opts.Limit) {
		return MQTTReplayTransfer{}, dberrors.ErrInvalidArgument
	}
	page, err := exportMQTTReplayFrom(ctx, view, l.key, expected.Generation, from, expected.Through, opts)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	if page.After.Through != expected.Through {
		return MQTTReplayTransfer{}, dberrors.ErrInvalidArgument
	}
	if page.After != expected {
		return MQTTReplayTransfer{}, dberrors.ErrCorruptState
	}
	return page, nil
}

// ImportMQTTReplayAnchor verifies a donor page against this replica's committed
// anchor and log identities, atomically retaining rows, meters and the frontier.
// The caller keeps page bytes immutable until return. This operation changes no
// checkpoint, source-release decision or ordinary history, and proves no quorum.
func (l *ChannelLog) ImportMQTTReplayAnchor(ctx context.Context, position uint64, page MQTTReplayTransfer) (MQTTReplayState, error) {
	if err := validateMQTTReplayTransfer(page); err != nil {
		return MQTTReplayState{}, err
	}
	if position == 0 || position <= page.After.Through {
		return MQTTReplayState{}, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return MQTTReplayState{}, err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	l.checkpointMu.Lock()
	defer l.checkpointMu.Unlock()
	if err := ctxErr(ctx); err != nil {
		return MQTTReplayState{}, err
	}
	expected, err := committedMQTTReplayPrefix(l.db.engine, l.key, position)
	if err != nil {
		return MQTTReplayState{}, err
	}
	if expected != page.After {
		return MQTTReplayState{}, dberrors.ErrConflict
	}
	return l.importMQTTReplayLocked(ctx, expected, page)
}

// ExportMQTTReplayAnchor retains the compatibility lease during the pinned read.
func (s *ChannelStore) ExportMQTTReplayAnchor(ctx context.Context, position, from uint64, opts ReadOptions) (MQTTReplayTransfer, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayTransfer{}, err
	}
	defer s.endUse()
	page, err := s.log.ExportMQTTReplayAnchor(ctx, position, from, opts)
	return page, toChannelError(err)
}

// ImportMQTTReplayAnchor retains the compatibility lease through durable commit.
func (s *ChannelStore) ImportMQTTReplayAnchor(ctx context.Context, position uint64, page MQTTReplayTransfer) (MQTTReplayState, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayState{}, err
	}
	defer s.endUse()
	prefix, err := s.log.ImportMQTTReplayAnchor(ctx, position, page)
	return prefix, toChannelError(err)
}
