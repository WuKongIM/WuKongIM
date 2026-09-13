package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	clusterchannels "github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
)

// PersistedMessageReadNode exposes authority-routed disk reads without runtime activation.
type PersistedMessageReadNode interface {
	ReadChannelPersistedBatch(context.Context, []clusterchannels.CommittedRead) ([]clusterchannels.CommittedReadResult, error)
}

// PersistedMessageReader supplies record scans exclusively for conversation previews.
type PersistedMessageReader struct{ node PersistedMessageReadNode }

func NewPersistedMessageReader(node PersistedMessageReadNode) *PersistedMessageReader {
	return &PersistedMessageReader{node: node}
}

// ReadPersistedMessages preserves byte-limited continuation and all item failures.
func (r *PersistedMessageReader) ReadPersistedMessages(ctx context.Context, queries []message.MessageScanQuery) ([]message.MessageScanResult, error) {
	if r == nil || r.node == nil {
		return nil, message.ErrMessageReaderRequired
	}
	reads := messageScanReads(queries)
	batch, err := r.node.ReadChannelPersistedBatch(ctx, reads)
	if err != nil {
		return nil, mapAppendError(err)
	}
	if len(batch) != len(reads) {
		return nil, message.ErrSyncBatchResultMismatch
	}
	results := make([]message.MessageScanResult, len(batch))
	for i, item := range batch {
		results[i].Err = mapAppendError(item.Err)
		if item.Err != nil {
			continue
		}
		results[i].Messages = committedMessagesFromChannel(item.Read.Messages)
		// A short byte-limited scan is not proof that the requested range ended.
		if len(item.Read.Messages) > 0 {
			q := queries[i]
			if q.Reverse {
				results[i].HasMore = item.Read.NextSeq >= max(uint64(1), q.MinSeq)
			} else {
				results[i].HasMore = item.Read.NextSeq > 0 && (q.MaxSeq == 0 || item.Read.NextSeq <= q.MaxSeq)
			}
		}
	}
	return results, nil
}
