package message

import (
	"context"
	"encoding/binary"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

// AuthorityPromise is one replica's durable promise not to accept writes from
// a lower Channel authority. It replaces the in-log current-term barrier: the
// distributed log sequence is message_seq, so the fence lives in the Channel's
// system space (ID 19) and never consumes a log index. Ordering matches the
// replication authority: ChannelEpoch, then LeaderTerm, then FenceVersion.
type AuthorityPromise struct {
	ChannelEpoch uint64
	LeaderTerm   uint64
	FenceVersion uint64
}

// IsZero reports an absent authority.
func (p AuthorityPromise) IsZero() bool { return p == AuthorityPromise{} }

// Compare returns -1, 0 or 1 ordering p against other.
func (p AuthorityPromise) Compare(other AuthorityPromise) int {
	for _, pair := range [3][2]uint64{
		{p.ChannelEpoch, other.ChannelEpoch},
		{p.LeaderTerm, other.LeaderTerm},
		{p.FenceVersion, other.FenceVersion},
	} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}
	return 0
}

const (
	authorityPromiseCodecVersion byte = 1
	authorityPromiseValueSize         = 1 + 3*8
)

func encodeAuthorityPromise(p AuthorityPromise) []byte {
	value := make([]byte, authorityPromiseValueSize)
	value[0] = authorityPromiseCodecVersion
	binary.BigEndian.PutUint64(value[1:9], p.ChannelEpoch)
	binary.BigEndian.PutUint64(value[9:17], p.LeaderTerm)
	binary.BigEndian.PutUint64(value[17:25], p.FenceVersion)
	return value
}

func decodeAuthorityPromise(value []byte) (AuthorityPromise, error) {
	if len(value) != authorityPromiseValueSize || value[0] != authorityPromiseCodecVersion {
		return AuthorityPromise{}, dberrors.ErrCorruptState
	}
	p := AuthorityPromise{
		ChannelEpoch: binary.BigEndian.Uint64(value[1:9]),
		LeaderTerm:   binary.BigEndian.Uint64(value[9:17]),
		FenceVersion: binary.BigEndian.Uint64(value[17:25]),
	}
	if p.IsZero() {
		return AuthorityPromise{}, dberrors.ErrCorruptState
	}
	return p, nil
}

// authorityPromiseCache retains the durable promise under appendMu. It is
// valid only for the database mutation generation it was loaded under, so
// imports and other wholesale rewrites force a reload; the hot append path
// otherwise pays one atomic comparison per Channel batch.
type authorityPromiseCache struct {
	generation uint64
	loaded     bool
	present    bool
	promise    AuthorityPromise
}

// loadAuthorityPromiseLocked returns the durable promise. appendMu must be held.
func (e *channelEntry) loadAuthorityPromiseLocked() (AuthorityPromise, bool, error) {
	generation, stable := e.db.retentionReadGeneration()
	if stable && e.authorityPromise.loaded && e.authorityPromise.generation == generation {
		return e.authorityPromise.promise, e.authorityPromise.present, nil
	}
	value, ok, err := e.db.engine.Get(encodeAuthorityPromiseKey(e.key))
	if err != nil {
		return AuthorityPromise{}, false, err
	}
	var promise AuthorityPromise
	if ok {
		if promise, err = decodeAuthorityPromise(value); err != nil {
			return AuthorityPromise{}, false, err
		}
	}
	if stable {
		e.authorityPromise = authorityPromiseCache{generation: generation, loaded: true, present: ok, promise: promise}
	}
	return promise, ok, nil
}

// stageAuthorityPromise adds the promise to batch.
func (e *channelEntry) stageAuthorityPromise(batch *engine.Batch, promise AuthorityPromise) error {
	return batch.Set(encodeAuthorityPromiseKey(e.key), encodeAuthorityPromise(promise))
}

// publishAuthorityPromiseLocked publishes a committed promise. appendMu must be
// held and the promise must already be durable.
func (e *channelEntry) publishAuthorityPromiseLocked(promise AuthorityPromise) {
	generation, stable := e.db.retentionReadGeneration()
	if !stable {
		e.authorityPromise = authorityPromiseCache{}
		return
	}
	e.authorityPromise = authorityPromiseCache{generation: generation, loaded: true, present: true, promise: promise}
}

// AuthorityPromise returns this replica's durable promise for the Channel.
func (s *ChannelStore) AuthorityPromise(ctx context.Context) (AuthorityPromise, bool, error) {
	if err := s.beginUse(); err != nil {
		return AuthorityPromise{}, false, err
	}
	defer s.endUse()
	if err := ctxErr(ctx); err != nil {
		return AuthorityPromise{}, false, err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	promise, ok, err := s.log.loadAuthorityPromiseLocked()
	if err != nil {
		return AuthorityPromise{}, false, toChannelError(err)
	}
	return promise, ok, nil
}

// PromiseAuthority durably records promise before returning. A repeated equal
// promise is idempotent; a promise lower than the current one fails with
// ErrStaleAuthority and writes nothing. It is serialized with appends by
// appendMu, so an append validated against an older promise can never commit
// after a newer promise has been reported durable.
func (s *ChannelStore) PromiseAuthority(ctx context.Context, promise AuthorityPromise) error {
	if promise.IsZero() {
		return channel.ErrInvalidArgument
	}
	if err := s.beginUse(); err != nil {
		return err
	}
	defer s.endUse()
	if err := ctxErr(ctx); err != nil {
		return err
	}
	s.log.appendMu.Lock()
	defer s.log.appendMu.Unlock()
	current, ok, err := s.log.loadAuthorityPromiseLocked()
	if err != nil {
		return toChannelError(err)
	}
	if ok {
		switch promise.Compare(current) {
		case -1:
			return channel.ErrStaleAuthority
		case 0:
			return nil
		}
	}
	batch := s.log.db.engine.NewBatch()
	defer batch.Close()
	if err := s.log.stageAuthorityPromise(batch, promise); err != nil {
		return toChannelError(err)
	}
	if err := s.log.stageCatalog(batch); err != nil {
		return toChannelError(err)
	}
	if err := batch.Commit(true); err != nil {
		return toChannelError(err)
	}
	s.log.publishAuthorityPromiseLocked(promise)
	return nil
}
