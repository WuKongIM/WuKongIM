package store

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// AuthorityFence is one complete Channel write authority as compared against a
// replica's durable authority promise. Ordering is ChannelEpoch, then
// LeaderTerm, then FenceVersion. The promise replaces the in-log authority
// barrier, so it never consumes a log index (the log index is message_seq).
type AuthorityFence struct {
	ChannelEpoch uint64
	LeaderTerm   uint64
	FenceVersion uint64
}

// IsZero reports an absent authority.
func (f AuthorityFence) IsZero() bool { return f == AuthorityFence{} }

// Compare returns -1, 0 or 1 ordering f against other.
func (f AuthorityFence) Compare(other AuthorityFence) int {
	for _, pair := range [3][2]uint64{
		{f.ChannelEpoch, other.ChannelEpoch},
		{f.LeaderTerm, other.LeaderTerm},
		{f.FenceVersion, other.FenceVersion},
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

// manifestAuthority is the authority an exact append is fenced by when its
// request names no explicit writer.
func manifestAuthority(manifest ProposalManifest) AuthorityFence {
	return AuthorityFence{ChannelEpoch: manifest.ChannelEpoch, LeaderTerm: manifest.LeaderTerm, FenceVersion: manifest.FenceVersion}
}

// AuthorityPromiser persists one replica's promise not to accept exact appends
// from a lower Channel authority. PromiseAuthority returns only after the
// promise is durable; an equal promise is idempotent and a lower one fails
// with ch.ErrStaleMeta without writing. It is serialized with exact appends,
// so an append validated against an older promise never commits after a newer
// promise has been reported durable.
type AuthorityPromiser interface {
	PromiseAuthority(ctx context.Context, promise AuthorityFence) error
	AuthorityPromise(ctx context.Context) (AuthorityFence, bool, error)
}

// fenceExactAppend applies the promise contract shared by every exact append:
// an explicit writer, or else the proposal's own authority, must not be lower
// than the current promise. It returns the promise to raise with a successful
// write, which only an explicit higher writer produces.
func fenceExactAppend(req AppendLeaderRequest, promise AuthorityFence, present bool) (*AuthorityFence, error) {
	writer := manifestAuthority(req.Proposal)
	explicit := req.WriterAuthority != nil
	if explicit {
		writer = *req.WriterAuthority
		if writer.IsZero() {
			return nil, ch.ErrInvalidConfig
		}
	}
	if present && writer.Compare(promise) < 0 {
		return nil, ch.ErrStaleMeta
	}
	if explicit && (!present || writer.Compare(promise) > 0) {
		return &writer, nil
	}
	return nil, nil
}
