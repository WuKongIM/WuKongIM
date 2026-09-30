//go:build integration

package message

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type pendingMessageBatch struct {
	items    []SendBatchItem
	complete func([]SendBatchItemResult)
}
type messageAdmissionProbe struct {
	jobs   []pendingMessageBatch
	reject error
	inline bool
}

func (p *messageAdmissionProbe) Submit(items []SendBatchItem, complete func([]SendBatchItemResult)) error {
	if p.reject != nil {
		return p.reject
	}
	job := pendingMessageBatch{append([]SendBatchItem(nil), items...), complete}
	p.jobs = append(p.jobs, job)
	if p.inline {
		job.succeed()
	}
	return nil
}
func (p pendingMessageBatch) succeed() {
	results := make([]SendBatchItemResult, len(p.items))
	for i := range results {
		results[i].Result = SendResult{Reason: ReasonSuccess, MessageID: uint64(i + 1)}
	}
	p.complete(results)
}
func asyncMessageItems(n int) []SendBatchItem {
	items := make([]SendBatchItem, n)
	for i := range items {
		items[i] = SendBatchItem{Deadline: time.Now().Add(time.Minute), Command: SendCommand{FromUID: "u", ChannelID: fmt.Sprint("g", i), ChannelType: channelTypeGroup, ClientMsgNo: fmt.Sprint(i), SenderNodeID: 1, SenderSessionID: 2}}
	}
	return items
}
func waitMessageCompletion(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("completion did not join")
		return nil
	}
}

func TestSubmitBatchPreparationReturnsBeforeDurabilityAndRetainsDeadline(t *testing.T) {
	p := &messageAdmissionProbe{}
	var hooks []string
	a := New(Options{BatchAdmission: p, SendHook: prefixHook{order: &hooks}})
	done := make(chan error, 2)
	items := asyncMessageItems(2)
	var published []int
	if err := a.SubmitBatchEach(items, func(i int, r SendBatchItemResult) error { published = append(published, i); return r.Err }, func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	if len(p.jobs) != 1 || !reflect.DeepEqual(hooks, []string{"0", "1"}) {
		t.Fatalf("preparation not joined: jobs=%d hooks=%v", len(p.jobs), hooks)
	}
	for _, item := range p.jobs[0].items {
		if err := item.Context.Err(); err != nil {
			t.Fatalf("deadline canceled at admission: %v", err)
		}
	}
	select {
	case <-done:
		t.Fatal("completed before append results")
	default:
	}
	// A second batch is prepared in caller session order while the first is pending.
	items2 := asyncMessageItems(1)
	items2[0].Command.ClientMsgNo = "2"
	if err := a.SubmitBatchEach(items2, func(int, SendBatchItemResult) error { return nil }, func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hooks, []string{"0", "1", "2"}) {
		t.Fatal(hooks)
	}
	p.jobs[0].succeed()
	if err := waitMessageCompletion(t, done); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(published, []int{0, 1}) {
		t.Fatal(published)
	}
	for _, item := range p.jobs[0].items {
		if !errors.Is(item.Context.Err(), context.Canceled) {
			t.Fatal("completed context retained")
		}
	}
	if err := p.jobs[1].items[0].Context.Err(); err != nil {
		t.Fatal("another batch was canceled")
	}
	p.jobs[1].succeed()
	if err := waitMessageCompletion(t, done); err != nil {
		t.Fatal(err)
	}
}

type asyncDirectoryProbe struct {
	wave func([]PersonDirectoryAdmission, func([]PersonDirectoryAdmissionOutcome))
}

func (p asyncDirectoryProbe) AdmitPersonChannelDirectory(context.Context, string, int64) error {
	panic("expected waves")
}
func (p asyncDirectoryProbe) AdmitPersonChannelDirectoryWaves(a []PersonDirectoryAdmission, f func([]PersonDirectoryAdmissionOutcome)) {
	p.wave(a, f)
}

func TestSubmitBatchColdPrefixAndConcurrentCompletion(t *testing.T) {
	p := &messageAdmissionProbe{}
	items := asyncMessageItems(4)
	items[1].Command.ChannelID = "u@v"
	items[1].Command.ChannelType = channelTypePerson
	items[3].Command.SenderSessionID = 3
	var published []int
	var hooks []string
	done := make(chan error, 1)
	directory := asyncDirectoryProbe{wave: func(_ []PersonDirectoryAdmission, emit func([]PersonDirectoryAdmissionOutcome)) {
		if len(p.jobs) != 1 || len(p.jobs[0].items) != 2 || p.jobs[0].items[1].Command.ClientMsgNo != "3" {
			t.Fatalf("cold prefix crossed: %+v", p.jobs)
		}
		p.jobs[0].succeed()
		select {
		case <-done:
			t.Fatal("completed during preparation")
		default:
		}
		if err := p.jobs[0].items[0].Context.Err(); err != nil {
			t.Fatal("preparation deadline canceled")
		}
		emit([]PersonDirectoryAdmissionOutcome{{Index: 0}})
	}}
	a := New(Options{BatchAdmission: p, PersonDirectory: directory, SendHook: prefixHook{order: &hooks}})
	if err := a.SubmitBatchEach(items, func(i int, r SendBatchItemResult) error { published = append(published, i); return r.Err }, func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	if len(p.jobs) != 2 || !reflect.DeepEqual(hooks, []string{"0", "3", "1", "2"}) {
		t.Fatalf("prefix failed to advance at admission: jobs=%d hooks=%v", len(p.jobs), hooks)
	}
	p.jobs[1].succeed()
	if err := waitMessageCompletion(t, done); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(published, []int{0, 3, 1, 2}) {
		t.Fatal(published)
	}
}

func TestSubmitBatchResultsSerializeAndWaitForSessionHead(t *testing.T) {
	p := &messageAdmissionProbe{}
	items := asyncMessageItems(3)
	items[1].Command.ChannelID = "u@v"
	items[1].Command.ChannelType = channelTypePerson
	items[2].Command.SenderSessionID = 3
	directory := asyncDirectoryProbe{wave: func(_ []PersonDirectoryAdmission, emit func([]PersonDirectoryAdmissionOutcome)) {
		emit([]PersonDirectoryAdmissionOutcome{{Index: 0}})
	}}
	a := New(Options{BatchAdmission: p, PersonDirectory: directory})
	done := make(chan error, 1)
	var active atomic.Int32
	var indexes []int
	if err := a.SubmitBatchEach(items, func(i int, _ SendBatchItemResult) error {
		if active.Add(1) != 1 {
			t.Error("concurrent emitter")
		}
		indexes = append(indexes, i)
		active.Add(-1)
		return nil
	}, func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	if len(p.jobs) != 2 {
		t.Fatal(len(p.jobs))
	}
	var wg sync.WaitGroup
	wg.Add(2)
	for i := range p.jobs {
		go func(j int) { defer wg.Done(); p.jobs[j].succeed() }(i)
	}
	wg.Wait()
	if err := waitMessageCompletion(t, done); err != nil {
		t.Fatal(err)
	}
	if len(indexes) != 3 {
		t.Fatal(indexes)
	}
	first, second := -1, -1
	for i, v := range indexes {
		if v == 0 {
			first = i
		}
		if v == 1 {
			second = i
		}
	}
	if first >= second {
		t.Fatalf("session head overtaken: %v", indexes)
	}
}

func TestSubmitBatchInlineRejectedAndMalformedResults(t *testing.T) {
	for _, mode := range []string{"inline", "rejected", "short", "missing", "empty"} {
		t.Run(mode, func(t *testing.T) {
			p := &messageAdmissionProbe{inline: mode == "inline"}
			if mode == "rejected" {
				p.reject = ErrChannelBusy
			}
			opts := Options{BatchAdmission: p}
			if mode == "missing" {
				opts.BatchAdmission = nil
			}
			a := New(opts)
			items := asyncMessageItems(2)
			if mode == "empty" {
				items = nil
			}
			var got []SendBatchItemResult
			done := make(chan error, 1)
			err := a.SubmitBatchEach(items, func(_ int, r SendBatchItemResult) error { got = append(got, r); return nil }, func(err error) { done <- err })
			if mode == "missing" {
				if !errors.Is(err, ErrRouteNotReady) {
					t.Fatal(err)
				}
				select {
				case <-done:
					t.Fatal("unaccepted callback")
				default:
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "short" {
				p.jobs[0].complete(nil)
			}
			if err := waitMessageCompletion(t, done); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(items) {
				t.Fatal(got)
			}
			for _, r := range got {
				switch mode {
				case "inline":
					if r.Result.Reason != ReasonSuccess {
						t.Fatal(r)
					}
				case "rejected":
					if !errors.Is(r.Err, ErrChannelBusy) {
						t.Fatal(r)
					}
				case "short":
					if !errors.Is(r.Err, ErrSendBatchEmissionMismatch) {
						t.Fatal(r)
					}
				}
			}
		})
	}
	a := New(Options{BatchAdmission: &messageAdmissionProbe{}})
	if err := a.SubmitBatchEach(nil, nil, func(error) {}); err == nil {
		t.Fatal("nil emitter accepted")
	}
	if err := a.SubmitBatchEach(nil, func(int, SendBatchItemResult) error { return nil }, nil); err == nil {
		t.Fatal("nil completion accepted")
	}
}

func TestSubmitBatchEmitterErrorStillJoinsAndCleansContexts(t *testing.T) {
	p := &messageAdmissionProbe{}
	a := New(Options{BatchAdmission: p})
	done := make(chan error, 1)
	sentinel := errors.New("write failed")
	calls := 0
	if err := a.SubmitBatchEach(asyncMessageItems(2), func(int, SendBatchItemResult) error { calls++; return sentinel }, func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	p.jobs[0].succeed()
	if err := waitMessageCompletion(t, done); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	for _, item := range p.jobs[0].items {
		if !errors.Is(item.Context.Err(), context.Canceled) {
			t.Fatal("deadline leaked")
		}
	}
}

func TestSubmitBatchFreshBansAndHookReauthorization(t *testing.T) {
	base := newFakePermissionStore()
	store := &recordingPermissionBatchStore{base: base}
	p := &messageAdmissionProbe{inline: true}
	base.members[permissionKey("g0", 2)] = map[string]bool{"u": true}
	base.channels[permissionKey("g0", 2)] = metadb.Channel{ChannelID: "g0", ChannelType: 2}
	hook := &recordingSendHook{mutate: func(c SendCommand) (SendCommand, Reason, error) { c.FromUID = "blocked"; return c, ReasonSuccess, nil }}
	a := New(Options{BatchAdmission: p, PermissionStore: store, PermissionBatchStore: store, PermissionCacheTTL: time.Hour})
	run := func(want Reason) {
		t.Helper()
		done := make(chan error, 1)
		var got SendBatchItemResult
		if err := a.SubmitBatchEach(asyncMessageItems(1), func(_ int, r SendBatchItemResult) error { got = r; return nil }, func(err error) { done <- err }); err != nil {
			t.Fatal(err)
		}
		if err := waitMessageCompletion(t, done); err != nil {
			t.Fatal(err)
		}
		if got.Err != nil || got.Result.Reason != want {
			t.Fatalf("got %+v want %v", got, want)
		}
	}
	run(ReasonSuccess)
	base.userPolicies["u"] = metadb.SendBanResult{SendBan: 1}
	run(ReasonSendBan)
	base.userPolicies["u"] = metadb.SendBanResult{}
	base.userPolicies["blocked"] = metadb.SendBanResult{SendBan: 1}
	a.sendHook = hook
	run(ReasonSendBan)
	if len(p.jobs) != 1 {
		t.Fatalf("banned work admitted: %d", len(p.jobs))
	}
}

func TestSubmitBatchRetainsParentCancellation(t *testing.T) {
	p := &messageAdmissionProbe{}
	a := New(Options{BatchAdmission: p})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	items := asyncMessageItems(1)
	items[0].Context = ctx
	done := make(chan error, 1)
	if err := a.SubmitBatchEach(items, func(_ int, r SendBatchItemResult) error { return r.Err }, func(err error) { done <- err }); err != nil {
		t.Fatal(err)
	}
	cancel()
	if !errors.Is(p.jobs[0].items[0].Context.Err(), context.Canceled) {
		t.Fatal("parent cancellation detached")
	}
	p.jobs[0].complete([]SendBatchItemResult{{Err: context.Canceled}})
	if err := waitMessageCompletion(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
