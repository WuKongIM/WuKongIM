package service_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/service"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestPublicationSurvivesSingleNodeClusterAppendAndOwnedRead(t *testing.T) {
	metadata, err := hex.DecodeString("01010100000000000003e800016e00016300017400030600017800036f6e65020000003c06000178000374776f")
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{1 << 20, 10} {
		t.Run(strconv.Itoa(budget), func(t *testing.T) {
			c, err := service.New(service.Config{LocalNode: 1, ReactorCount: 1, Store: store.NewMemoryFactory(), AppendQueueMaxBytes: budget})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			meta := ch.Meta{Key: "2:publication", ID: ch.ChannelID{ID: "publication", Type: 2}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}
			if err := c.ApplyMeta(meta); err != nil {
				t.Fatal(err)
			}
			input := bytes.Clone(metadata)
			res, err := c.Append(context.Background(), ch.AppendRequest{ChannelID: meta.ID, Message: ch.Message{MessageID: 101, FromUID: "sender", ClientMsgNo: "client", Payload: []byte("body"), PublicationMetadata: input, ServerTimestampMS: 2000}})
			if budget == 10 {
				if !errors.Is(err, ch.ErrBackpressured) {
					t.Fatalf("metadata bypassed append budget: %v", err)
				}
				return
			}
			if err != nil || !bytes.Equal(res.Message.PublicationMetadata, metadata) {
				t.Fatalf("append result lost metadata: %+v %v", res.Message, err)
			}
			clear(input)
			clear(res.Message.PublicationMetadata)
			got, found, err := c.(ch.CommittedMessageLookup).LookupCommittedMessage(context.Background(), meta.ID, 101)
			if err != nil || !found || !bytes.Equal(got.PublicationMetadata, metadata) {
				t.Fatalf("committed metadata: %+v %v", got, err)
			}
			clear(got.PublicationMetadata)
			got, found, err = c.(ch.CommittedMessageLookup).LookupCommittedMessage(context.Background(), meta.ID, 101)
			if err != nil || !found || !bytes.Equal(got.PublicationMetadata, metadata) {
				t.Fatal("committed metadata aliases a prior result")
			}
		})
	}
}
