package cluster

import (
	"context"
	"encoding/hex"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestPublicationMetadataCountsTowardEditedReadBudget(t *testing.T) {
	metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	require.NoError(t, err)
	for _, reverse := range []bool{false, true} {
		for _, budget := range []int{len(metadata) + 6, 6} {
			msgs := []ch.Message{{MessageSeq: 10, Payload: []byte("longer"), PublicationMetadata: metadata}, {MessageSeq: 11, Payload: []byte("longer"), PublicationMetadata: metadata}}
			if reverse {
				msgs[0].MessageSeq, msgs[1].MessageSeq = 11, 10
			}
			reads := []channels.CommittedRead{{Request: store.ReadCommittedRequest{MaxBytes: budget, Reverse: reverse}}}
			results := []channels.CommittedReadResult{{Read: store.ReadCommittedResult{Messages: msgs}}}
			require.NoError(t, (&Node{}).overlayMessageReads(context.Background(), reads, results))
			result := results[0]
			if budget == 6 {
				require.ErrorIs(t, result.Err, metadb.ErrInvalidArgument)
				require.Empty(t, result.Read.Messages)
				continue
			}
			require.NoError(t, result.Err)
			require.Len(t, result.Read.Messages, 1)
			require.True(t, result.ContentTruncated)
			next := uint64(11)
			if reverse {
				next = 10
			}
			require.Equal(t, next, result.Read.NextSeq)
			require.Equal(t, metadata, result.Read.Messages[0].PublicationMetadata)
		}
	}
}
