package message

import (
	"context"
	"errors"
	meta "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"testing"
)

func TestLookupUsesMembershipFloorAndRejectsIncorrectIndexEvidence(t *testing.T) {
	var reads []CommittedMessageQuery
	reader := scanFunction(func(_ context.Context, q []CommittedMessageQuery) ([]CommittedMessageResult, error) {
		reads = append(reads, q...)
		r := q[0]
		return []CommittedMessageResult{{Messages: []SyncedMessage{{ChannelID: r.ChannelID.ID, ChannelType: r.ChannelID.Type, MessageID: 11, MessageSeq: 8, ClientMsgNo: "key", Payload: []byte("hello")}}}}, nil
	})
	memberships := &recordingSyncMembershipStore{ok: true, row: meta.UserChannelMembership{JoinSeq: 3, DeletedToSeq: 7}}
	a := New(Options{Reader: &recordingChannelMessageReader{}, LookupReader: reader, Memberships: memberships})
	q := LookupMessagesQuery{LoginUID: "a", ChannelID: "g", ChannelType: 2, MessageIDs: []uint64{11}, ClientMsgNos: []string{"key"}, MessageSeqs: []uint64{1, 8}}
	out, e := a.LookupMessages(context.Background(), q)
	if e != nil || len(out.Messages) != 1 {
		t.Fatalf("result %+v %v", out, e)
	}
	if len(reads) != 3 {
		t.Fatalf("reads %d", len(reads))
	}
	for _, r := range reads {
		if r.MinSeq != 8 {
			t.Fatalf("floor %+v", r)
		}
	}
	memberships.row.Tombstone = true
	reads = nil
	_, e = a.LookupMessages(context.Background(), q)
	if !errors.Is(e, ErrSyncMembershipRequired) || len(reads) != 0 {
		t.Fatalf("tombstone %v", e)
	}
	memberships.row.Tombstone = false
	q.MessageIDs = []uint64{999}
	q.MessageSeqs = nil
	q.ClientMsgNos = nil
	_, e = a.LookupMessages(context.Background(), q)
	if !errors.Is(e, ErrSyncBatchResultMismatch) {
		t.Fatalf("incorrect identity: %v", e)
	}
	q.MessageIDs = make([]uint64, maxLookupSelectors+1)
	_, e = a.LookupMessages(context.Background(), q)
	if !errors.Is(e, ErrLookupBounds) {
		t.Fatalf("selector bound: %v", e)
	}
}
