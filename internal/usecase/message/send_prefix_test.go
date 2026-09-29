package message

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// This probe models a gateway batch: every item has the same real session.
// Reverse completions expose publication reordering without timers or sleeps.
func TestSendBatchReadySessionPrefixRetainsBatchAndPublicationOrder(t *testing.T) {
	var hookOrder []string
	submitter := &prefixSubmitter{}
	app := New(Options{Submitter: submitter, SendHook: prefixHook{order: &hookOrder}})
	items := make([]SendBatchItem, 128)
	want := make([]string, len(items))
	for i := range items {
		want[i] = fmt.Sprint(i)
		items[i].Command = SendCommand{FromUID: "u1", ChannelID: fmt.Sprintf("g%d", i%4), ChannelType: channelTypeGroup, ClientMsgNo: want[i], SenderNodeID: 1, SenderSessionID: 2}
	}
	var published []int
	err := app.SendBatchEach(items, func(index int, result SendBatchItemResult) error {
		published = append(published, index)
		if result.Err != nil || result.Result.MessageID != uint64(index+1) {
			t.Errorf("unaligned result at %d: %+v", index, result)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submitter.batches) != 1 || !reflect.DeepEqual(submitter.batches[0], want) {
		t.Fatalf("ready prefix split or reordered: %v", submitter.batches)
	}
	if !reflect.DeepEqual(hookOrder, want) {
		t.Fatalf("hook order: %v", hookOrder)
	}
	for i, got := range published {
		if got != i {
			t.Fatalf("publication %d = %d", i, got)
		}
	}
	if len(published) != len(items) {
		t.Fatalf("published %d of %d", len(published), len(items))
	}
}

func TestSendBatchReadyPrefixStopsAtColdDirectory(t *testing.T) {
	submitter := &prefixSubmitter{}
	directory := &prefixDirectory{checkBlocked: func() {
		if !reflect.DeepEqual(submitter.batches, [][]string{{"3"}}) {
			t.Fatalf("same-session work crossed cold head: %v", submitter.batches)
		}
	}}
	app := New(Options{Submitter: submitter, PersonDirectory: directory})
	items := make([]SendBatchItem, 4)
	for i := range items {
		items[i].Command = SendCommand{FromUID: "u1", ChannelID: "g", ChannelType: channelTypeGroup, ClientMsgNo: fmt.Sprint(i), SenderNodeID: 1, SenderSessionID: 2}
	}
	items[0].Command.ChannelID, items[0].Command.ChannelType = "u1@u2", channelTypePerson
	items[2].Command.ChannelID, items[2].Command.ChannelType = "u1@u3", channelTypePerson
	items[3].Command.SenderSessionID = 3
	results := app.SendBatch(items)
	for i, result := range results {
		if result.Err != nil || result.Result.MessageID != uint64(i+1) {
			t.Fatalf("result %d: %+v", i, result)
		}
	}
	if !reflect.DeepEqual(submitter.batches, [][]string{{"3"}, {"0", "1", "2"}}) {
		t.Fatalf("ready wave lost batch/order: %v", submitter.batches)
	}
}

func TestSendBatchReadyPrefixKeepsRejectedItemPublicationPosition(t *testing.T) {
	var hooks []string
	submitter := &prefixSubmitter{}
	app := New(Options{Submitter: submitter, SendHook: prefixHook{order: &hooks, reject: "1"}})
	items := make([]SendBatchItem, 3)
	for i := range items {
		items[i].Command = SendCommand{FromUID: "u", ChannelID: "g", ChannelType: channelTypeGroup, ClientMsgNo: fmt.Sprint(i), SenderNodeID: 1, SenderSessionID: 2}
	}
	var published []int
	err := app.SendBatchEach(items, func(i int, result SendBatchItemResult) error {
		published = append(published, i)
		if i == 1 && result.Result.Reason != ReasonSendBan {
			t.Errorf("rejection lost: %+v", result)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(submitter.batches, [][]string{{"0", "2"}}) {
		t.Fatalf("rejected item appended or prefix fragmented: %v", submitter.batches)
	}
	if !reflect.DeepEqual(published, []int{0, 1, 2}) {
		t.Fatalf("publication order = %v", published)
	}
	if !reflect.DeepEqual(hooks, []string{"0", "1", "2"}) {
		t.Fatalf("hook order = %v", hooks)
	}
}

type prefixSubmitter struct{ batches [][]string }

func (*prefixSubmitter) Send(context.Context, SendCommand) (SendResult, error) {
	panic("unexpected scalar send")
}
func (*prefixSubmitter) SendBatch([]SendBatchItem) []SendBatchItemResult {
	panic("expected streaming batch")
}
func (s *prefixSubmitter) SendBatchEach(items []SendBatchItem, emit func(int, SendBatchItemResult)) {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.Command.ClientMsgNo
	}
	s.batches = append(s.batches, ids)
	for i := len(items) - 1; i >= 0; i-- {
		var id uint64
		_, _ = fmt.Sscan(items[i].Command.ClientMsgNo, &id)
		emit(i, SendBatchItemResult{Result: SendResult{Reason: ReasonSuccess, MessageID: id + 1}})
	}
}

type prefixHook struct {
	order  *[]string
	reject string
}

func (h prefixHook) BeforeSend(_ context.Context, cmd SendCommand) (SendCommand, Reason, error) {
	*h.order = append(*h.order, cmd.ClientMsgNo)
	if h.reject != "" && cmd.ClientMsgNo == h.reject {
		return cmd, ReasonSendBan, nil
	}
	return cmd, ReasonSuccess, nil
}

type prefixDirectory struct{ checkBlocked func() }

func (*prefixDirectory) AdmitPersonChannelDirectory(context.Context, string, int64) error {
	panic("expected directory waves")
}
func (d *prefixDirectory) AdmitPersonChannelDirectoryWaves(admissions []PersonDirectoryAdmission, emit func([]PersonDirectoryAdmissionOutcome)) {
	if len(admissions) != 2 {
		panic("expected two directory groups")
	}
	emit([]PersonDirectoryAdmissionOutcome{{Index: 1}})
	d.checkBlocked()
	emit([]PersonDirectoryAdmissionOutcome{{Index: 0}})
}
