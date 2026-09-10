package migration

import (
	"context"
	"errors"
	"fmt"
)

// resolveDuplicateTerminal proves each edge against a selected original mapping.
// Strictly increasing sequences prevent cycles. An ambiguous terminal, excluded
// winner, or changed identity fails; older messages are never resurrected.
func resolveDuplicateTerminal(ctx context.Context, w Workspace, root MessageSequenceMapping) (DedupeMessage, error) {
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
		m, found, err := transformGet[MessageSequenceMapping](ctx, w, fmt.Sprintf("mapping/%s/%020d", channelKey, v.Sequence))
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
		// Bound malformed operator inputs independently of the total archive size.
		if len(seen) > 100000 {
			return terminal, errors.New("duplicate replacement chain exceeds 100000 nodes")
		}
		if m.Omitted == "" && m.TargetSeq > 0 {
			if terminal.Sequence != 0 && terminal.Sequence != v.Sequence {
				return terminal, errors.New("duplicate replacement chain has multiple surviving terminals")
			}
			terminal = v
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
	return terminal, nil
}
