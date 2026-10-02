package message

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// ReleaseMQTTSourceAtAnchor materializes a committed anchor's source cleanup
// boundary only after verifying this replica's durable shared prefix. It accepts
// no caller watermark/digest, advances no HW and deletes no content. Routing and
// current-authority admission remain the runtime's responsibility.
func (l *ChannelLog) ReleaseMQTTSourceAtAnchor(ctx context.Context, generation string, position uint64) (MQTTSourceState, error) {
	var empty MQTTSourceState
	if position == 0 || !(MQTTSourceState{Generation: generation, Revision: 1}).valid() {
		return empty, dberrors.ErrInvalidArgument
	}
	if err := l.beginUse(); err != nil {
		return empty, err
	}
	defer l.endUse()
	l.appendMu.Lock()
	defer l.appendMu.Unlock()
	l.checkpointMu.Lock()
	defer l.checkpointMu.Unlock()
	if err := ctxErr(ctx); err != nil {
		return empty, err
	}
	proof, found, err := loadMQTTReplayAnchorFrom(l.db.engine, l.key, position)
	if err != nil {
		return empty, err
	}
	if !found || mqttAnchorPrefix(proof).Generation != generation {
		return empty, dberrors.ErrConflict
	}
	current, present, err := loadMQTTReplayState(l.db.engine, l.key)
	if err != nil {
		return empty, err
	}
	if !present {
		return empty, dberrors.ErrConflict
	}
	evidence, err := mqttReplayTransferEvidence(l.db.engine, l.key, current)
	if err != nil {
		return empty, err
	}
	if err = validateMQTTReplayTail(l.db.engine, l.key, current); err != nil {
		return empty, err
	}
	if err = verifyMQTTRepairCovered(l.db.engine, l.key, current, proof); err != nil {
		return empty, err
	}
	source := evidence.source
	if source.CopiedThrough > current.Through {
		return empty, dberrors.ErrConflict
	}
	retention, _, err := l.loadRetentionState(ctx)
	if err != nil {
		return empty, err
	}
	if retention.PhysicalRetentionThroughSeq > source.CopiedThrough {
		return empty, dberrors.ErrCorruptState
	}
	if err = ctxErr(ctx); err != nil {
		return empty, err
	}
	// Even old retries must pass the committed and local-content proof above.
	// Independent replicas may skip intermediate anchors without sharing a CAS
	// revision; only the immutable boundary and receipt describe released data.
	if source.CopiedThrough >= proof.Anchor.Through {
		return source, nil
	}
	if source.Revision == math.MaxUint64 {
		return empty, dberrors.ErrConflict
	}
	source.Revision++
	source.CopiedThrough = proof.Anchor.Through
	source.ReceiptDigest = proof.Manifest.Digest
	key := mqttSourceKey(l.key)
	batch := l.db.engine.NewBatch()
	defer batch.Close()
	if err = batch.Set(key, encodeMQTTSourceState(key, source)); err != nil {
		return empty, err
	}
	if err = l.stageCatalog(batch); err != nil {
		return empty, err
	}
	if err = batch.Commit(true); err != nil {
		return empty, err
	}
	return source, nil
}

// ReleaseMQTTSourceAtAnchor retains the compatibility lease through proof and
// durable commit, preserving the core's replica-local release semantics.
func (s *ChannelStore) ReleaseMQTTSourceAtAnchor(ctx context.Context, generation string, position uint64) (MQTTSourceState, error) {
	if err := s.beginUse(); err != nil {
		return MQTTSourceState{}, err
	}
	defer s.endUse()
	state, err := s.log.ReleaseMQTTSourceAtAnchor(ctx, generation, position)
	return state, toChannelError(err)
}
