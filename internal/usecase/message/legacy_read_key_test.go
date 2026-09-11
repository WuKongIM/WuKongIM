package message

import (
	"context"
	"testing"
)

func TestLookupLegacyReadKeyUsesBoundedIDFallbackOnlyForBlankOriginal(t *testing.T) {
	for _, original := range []string{"", "real-key", "wk3-legacy-99"} {
		t.Run(original, func(t *testing.T) {
			var queries []CommittedMessageQuery
			reader := scanFunction(func(_ context.Context, q []CommittedMessageQuery) ([]CommittedMessageResult, error) {
				r := q[0]
				queries = append(queries, r)
				rows := []SyncedMessage{}
				if r.MessageID == 99 || r.ClientMsgNo == original {
					rows = append(rows, SyncedMessage{ChannelID: "g", ChannelType: 2, MessageID: 99, MessageSeq: 9, ClientMsgNo: original})
				}
				return []CommittedMessageResult{{Messages: rows}}, nil
			})
			a := New(Options{Reader: &recordingChannelMessageReader{}, LookupReader: reader, Memberships: liveSyncMembershipStore()})
			out, err := a.LookupMessages(context.Background(), LookupMessagesQuery{LoginUID: "u", ChannelID: "g", ChannelType: 2, ClientMsgNos: []string{"wk3-legacy-99"}})
			want := 1
			if original == "real-key" {
				want = 0
			}
			if err != nil || len(out.Messages) != want {
				t.Fatalf("lookup=%+v error=%v want %d", out, err, want)
			}
			calls := 2
			if original == "wk3-legacy-99" {
				calls = 1
			}
			if len(queries) != calls {
				t.Fatalf("reads=%d want %d", len(queries), calls)
			}
			if len(out.Messages) > 0 && out.Messages[0].ClientMsgNo != original {
				t.Fatal("lookup mutated stored client number")
			}
		})
	}
}

func TestLegacyReadKeyCanonicalAndNonMutating(t *testing.T) {
	for _, key := range []string{"wk3-legacy-0", "wk3-legacy-01", "wk3-legacy-+1", "wk3-legacy-18446744073709551616", "other"} {
		if _, ok := legacyReadMessageID(key); ok {
			t.Fatalf("accepted malformed alias %q", key)
		}
	}
	for _, key := range []string{"supplied", " ", "wk3-legacy-5"} {
		if got := LegacyReadClientMsgNo(99, key); got != key {
			t.Fatalf("changed original %q", key)
		}
	}
	if LegacyReadClientMsgNo(0, "") != "" {
		t.Fatal("invented identity without committed message ID")
	}
}
