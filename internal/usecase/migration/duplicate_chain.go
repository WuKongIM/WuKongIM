package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/WuKongIM/WuKongIM/pkg/db/transfer"
)

// duplicateResolver reuses only suffixes certified during this reconstruction.
// Its private generation prevents conversion or an earlier attempt from supplying
// cached evidence to an independent verification pass. Cache rows stay on disk.
type duplicateResolver struct {
	w      Workspace
	prefix string
}

func newDuplicateResolver(w Workspace) (*duplicateResolver, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return &duplicateResolver{w: w, prefix: "duplicate-resolved/" + hex.EncodeToString(nonce[:]) + "/"}, nil
}

// resolveDuplicateTerminal proves each edge against a selected original mapping.
// Strictly increasing sequences prevent cycles. An ambiguous terminal, excluded
// winner, or changed identity fails; older messages are never resurrected.
func (r *duplicateResolver) resolveDuplicateTerminal(ctx context.Context, root MessageSequenceMapping) (DedupeMessage, error) {
	type edge struct {
		from   uint64
		winner DedupeMessage
	}
	queue := make([]edge, 0, len(root.Winners))
	for _, v := range root.Winners {
		queue = append(queue, edge{root.OriginalSeq, v})
	}
	seen := map[uint64]bool{}
	channelKey := channelTuple(root.Channel)
	channelSHA := diagnosticSHA([]byte(channelKey))
	var terminal DedupeMessage
	mergeTerminal := func(v DedupeMessage) error {
		if terminal.Sequence != 0 && terminal.Sequence != v.Sequence {
			return errors.New("duplicate replacement chain has multiple surviving terminals")
		}
		terminal = v
		return nil
	}
	// At most one traversal's bounded frontier is retained in memory.
	uncached := make([]uint64, 0)
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return terminal, err
		}
		e := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		v := e.winner
		if v.Sequence <= e.from || v.ChannelSHA256 != channelSHA || v.NodeID != root.SourceNodeID {
			return terminal, errors.New("duplicate replacement edge is not increasing within the selected channel")
		}
		m, found, err := transformGet[MessageSequenceMapping](ctx, r.w, fmt.Sprintf("mapping/%s/%020d", channelKey, v.Sequence))
		if err != nil {
			return terminal, err
		}
		if !found || m.MessageID != v.ID || m.SourceSHA256 != v.SHA256 || m.Channel != root.Channel || m.SourceNodeID != root.SourceNodeID {
			return terminal, errors.New("duplicate replacement differs from original mapping")
		}
		if seen[v.Sequence] {
			continue
		}
		seen[v.Sequence] = true
		cached, found, err := transformGet[DedupeMessage](ctx, r.w, r.prefix+channelKey+fmt.Sprintf("/%020d", v.Sequence))
		if err != nil {
			return terminal, err
		}
		if found {
			if err := mergeTerminal(cached); err != nil {
				return terminal, err
			}
			continue
		}
		uncached = append(uncached, v.Sequence)
		// Bound uncached work per root; previously certified suffixes need no traversal.
		if len(seen) > 100000 {
			return terminal, errors.New("duplicate replacement chain exceeds 100000 nodes")
		}
		if m.Omitted == "" && m.TargetSeq > 0 {
			if err := mergeTerminal(v); err != nil {
				return terminal, err
			}
			continue
		}
		if m.Omitted != "duplicate" || len(m.Winners) == 0 {
			return terminal, errors.New("duplicate replacement terminal was excluded")
		}
		for _, next := range m.Winners {
			queue = append(queue, edge{m.OriginalSeq, next})
		}
	}
	if terminal.Sequence == 0 {
		return terminal, errors.New("duplicate replacement chain has no surviving terminal")
	}
	// Publish only after every branch has one identical, surviving terminal.
	// Original direct edges and mapping identities are never overwritten.
	data, err := MarshalState(terminal)
	if err != nil {
		return terminal, err
	}
	batch := captureBatch{ctx: ctx, workspace: r.w}
	for _, seq := range uncached {
		if err := batch.add(transfer.SpoolRow{Key: []byte(r.prefix + channelKey + fmt.Sprintf("/%020d", seq)), Value: data}); err != nil {
			return terminal, err
		}
	}
	if err := batch.flush(); err != nil {
		return terminal, err
	}
	return terminal, nil
}
