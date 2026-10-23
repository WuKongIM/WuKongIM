//go:build integration

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
)

type appCohortResolver struct{}

func (appCohortResolver) ResolveAppendAuthority(_ context.Context, id channelappend.ChannelID) (channelappend.AuthorityTarget, error) {
	return channelappend.AuthorityTarget{ChannelID: id, LeaderNodeID: 2}, nil
}

type appCohortRemote struct {
	calls chan int
	gate  <-chan struct{}
}

func (r appCohortRemote) ForwardSendBatch(_ context.Context, _ channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	r.calls <- len(items)
	if items[0].Command.ClientMsgNo == "first" {
		<-r.gate
	}
	return make([]channelappend.SendBatchItemResult, len(items))
}

func TestProductSubmissionCohortIndependentOfIngressBatchSize(t *testing.T) {
	cfg := deferredAppConfig()
	cfg.Gateway.Runtime.AsyncSendWorkers = 1
	cfg.Gateway.Session.AsyncSendBatchMaxRecords = 1
	a := &App{cfg: cfg}
	calls, gate := make(chan int, 4), make(chan struct{})
	var release sync.Once
	router := channelappend.NewRouter(channelappend.RouterOptions{LocalNodeID: 1, Resolver: appCohortResolver{}, Remote: appCohortRemote{calls, gate}})
	if err := a.wireChannelSubmissions(router); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := a.closeChannelSubmissions(ctx); err != nil {
			t.Error(err)
		}
	}()
	defer release.Do(func() { close(gate) })
	done := make(chan struct{}, 3)
	await := func(ch <-chan int) int {
		t.Helper()
		select {
		case n := <-ch:
			return n
		case <-time.After(3 * time.Second):
			t.Fatal("cohort made no progress")
			return 0
		}
	}
	for i, number := range []string{"first", "second", "third"} {
		item := channelappend.SendBatchItem{Context: context.Background(), Command: channelappend.SendCommand{FromUID: "sender", ChannelID: "group", ChannelType: 2, ClientMsgNo: number, Payload: []byte("x")}}
		if err := a.channelSubmissions.Submit([]channelappend.SendBatchItem{item}, func([]channelappend.SendBatchItemResult) { done <- struct{}{} }); err != nil {
			t.Fatal(err)
		}
		if i == 0 && await(calls) != 1 {
			t.Fatal("initial request split")
		}
	}
	release.Do(func() { close(gate) })
	if n := await(calls); n != 2 {
		t.Errorf("physical route batch = %d, want two queued jobs despite ingress batch=1", n)
	}
	for range 3 {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("callback not joined")
		}
	}
	if a.cfg.Gateway.Session.AsyncSendBatchMaxRecords != 1 {
		t.Fatal("rewrote ingress workload setting")
	}
}
