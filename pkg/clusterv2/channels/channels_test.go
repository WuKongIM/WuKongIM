package channels

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
)

func TestStaticMetaSourceResolvesAndDerivesKey(t *testing.T) {
	id := ch.ChannelID{ID: "room", Type: 1}
	source := NewStaticMetaSource([]ch.Meta{{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}})
	meta, err := source.ResolveChannelMeta(context.Background(), id)
	if err != nil {
		t.Fatalf("ResolveChannelMeta() error = %v", err)
	}
	if meta.Key != ch.ChannelKeyForID(id) {
		t.Fatalf("key = %q, want derived", meta.Key)
	}
}

func TestServiceRequiresCombinedRuntime(t *testing.T) {
	_, err := NewService(Config{Runtime: clusterOnlyRuntime{}})
	if err == nil {
		t.Fatal("NewService() error = nil, want combined runtime error")
	}
	svc, err := NewService(Config{Runtime: &fakeRuntime{}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if svc.Runtime() == nil || svc.Server() == nil {
		t.Fatal("service did not retain cluster and transport server surfaces")
	}
}

func TestCodecRoundTripsPull(t *testing.T) {
	req := channeltransport.PullRequest{ChannelKey: "1:room", ChannelID: ch.ChannelID{ID: "room", Type: 1}, Epoch: 1, LeaderEpoch: 2, Follower: 3, NextOffset: 4, MaxBytes: 1024}
	data, err := EncodePullRequest(req)
	if err != nil {
		t.Fatalf("EncodePullRequest() error = %v", err)
	}
	got, err := DecodePullRequest(data)
	if err != nil {
		t.Fatalf("DecodePullRequest() error = %v", err)
	}
	if got.ChannelKey != req.ChannelKey || got.NextOffset != req.NextOffset {
		t.Fatalf("decoded = %#v, want %#v", got, req)
	}
}

func TestTransportClientDispatchesPull(t *testing.T) {
	network := clusternet.NewLocalNetwork()
	runtime := &fakeRuntime{pull: channeltransport.PullResponse{ChannelKey: "1:room", LeaderHW: 9}}
	RegisterHandlers(network, 2, runtime)
	client := NewTransportClient(network)
	resp, err := client.Pull(context.Background(), 2, channeltransport.PullRequest{ChannelKey: "1:room"})
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if resp.LeaderHW != 9 || runtime.pullCalls != 1 {
		t.Fatalf("resp=%#v calls=%d, want HW 9 one call", resp, runtime.pullCalls)
	}
}

func TestServiceDelegatesAppend(t *testing.T) {
	runtime := &fakeRuntime{}
	svc, err := NewService(Config{Runtime: runtime})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = svc.Append(context.Background(), ch.AppendRequest{ChannelID: ch.ChannelID{ID: "room", Type: 1}})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if runtime.appendCalls != 1 {
		t.Fatalf("append calls = %d, want 1", runtime.appendCalls)
	}
}

type clusterOnlyRuntime struct{}

func (clusterOnlyRuntime) ApplyMeta(ch.Meta) error { return nil }
func (clusterOnlyRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	return ch.AppendResult{}, nil
}
func (clusterOnlyRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	return ch.AppendBatchResult{}, nil
}
func (clusterOnlyRuntime) Fetch(context.Context, ch.FetchRequest) (ch.FetchResult, error) {
	return ch.FetchResult{}, nil
}
func (clusterOnlyRuntime) Tick(context.Context) error { return nil }
func (clusterOnlyRuntime) Close() error               { return nil }

type fakeRuntime struct {
	pull        channeltransport.PullResponse
	pullCalls   int
	appendCalls int
}

func (f *fakeRuntime) ApplyMeta(ch.Meta) error { return nil }
func (f *fakeRuntime) Append(context.Context, ch.AppendRequest) (ch.AppendResult, error) {
	f.appendCalls++
	return ch.AppendResult{}, nil
}
func (f *fakeRuntime) AppendBatch(context.Context, ch.AppendBatchRequest) (ch.AppendBatchResult, error) {
	return ch.AppendBatchResult{}, nil
}
func (f *fakeRuntime) Fetch(context.Context, ch.FetchRequest) (ch.FetchResult, error) {
	return ch.FetchResult{}, nil
}
func (f *fakeRuntime) Tick(context.Context) error { return nil }
func (f *fakeRuntime) Close() error               { return nil }
func (f *fakeRuntime) HandlePull(context.Context, channeltransport.PullRequest) (channeltransport.PullResponse, error) {
	f.pullCalls++
	return f.pull, nil
}
func (f *fakeRuntime) HandleAck(context.Context, channeltransport.AckRequest) error { return nil }
func (f *fakeRuntime) HandlePullHint(context.Context, channeltransport.PullHintRequest) error {
	return nil
}
func (f *fakeRuntime) HandleNotify(context.Context, channeltransport.NotifyRequest) error { return nil }
