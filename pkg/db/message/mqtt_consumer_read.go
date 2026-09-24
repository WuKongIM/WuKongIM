package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// ReadMQTTReplayAnchor returns a bounded consumer page within an independently
// committed anchor. Unlike repair export, the page may stop before the anchor;
// its serving replica must nevertheless prove the entire anchored prefix first.
// It never reads ordinary history or changes progress, protection or retention.
func (l *ChannelLog) ReadMQTTReplayAnchor(ctx context.Context, generation string, anchor, from, through uint64, opts ReadOptions) (MQTTReplayTransfer, error) {
	if err := validateMQTTReplayRead(generation, from, through, opts); err != nil {
		return MQTTReplayTransfer{}, err
	}
	if anchor == 0 || through >= anchor {
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
	return readMQTTReplayAnchorFrom(ctx, view, l.key, generation, anchor, from, through, opts)
}

// ReadMQTTReplayAnchor retains the compatibility lease through the pinned read.
func (s *ChannelStore) ReadMQTTReplayAnchor(ctx context.Context, generation string, anchor, from, through uint64, opts ReadOptions) (MQTTReplayTransfer, error) {
	if err := s.beginUse(); err != nil {
		return MQTTReplayTransfer{}, err
	}
	defer s.endUse()
	page, err := s.log.ReadMQTTReplayAnchor(ctx, generation, anchor, from, through, opts)
	return page, toChannelError(err)
}

// readMQTTReplayAnchorFrom requires one pinned snapshot for proof and content.
func readMQTTReplayAnchorFrom(ctx context.Context, view messageBackupReadView, key ChannelKey, generation string, anchor, from, through uint64, opts ReadOptions) (MQTTReplayTransfer, error) {
	expected, err := committedMQTTReplayPrefix(view, key, anchor)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	if expected.Generation != generation || from <= expected.StartAfter || through > expected.Through {
		return MQTTReplayTransfer{}, dberrors.ErrConflict
	}
	current, found, err := loadMQTTReplayState(view, key)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	if !found || current.Generation != generation || current.StartAfter != expected.StartAfter || current.Through < expected.Through {
		return MQTTReplayTransfer{}, dberrors.ErrConflict
	}
	prefix, err := mqttReplayPrefix(view, key, current, expected.Through)
	if err != nil {
		return MQTTReplayTransfer{}, err
	}
	if prefix != expected {
		return MQTTReplayTransfer{}, dberrors.ErrCorruptState
	}
	return exportMQTTReplayFrom(ctx, view, key, generation, from, through, opts)
}
