package bench

import (
	"context"
	"sync"
	"testing"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestRunSendUsesClientPoolAndAggregatesSuccess(t *testing.T) {
	orig := newSendPool
	t.Cleanup(func() { newSendPool = orig })
	fake := &fakeSendPool{}
	newSendPool = func(cfg sendPoolConfig) (sendPool, error) {
		if len(cfg.Addrs) != 2 {
			t.Fatalf("pool addrs = %#v, want two gateways", cfg.Addrs)
		}
		return fake, nil
	}
	cfg, err := normalizeSendConfig(sendConfig{
		GatewayAddrs:  []string{"127.0.0.1:5100", "127.0.0.1:5101"},
		Clients:       2,
		Messages:      5,
		Size:          "4B",
		BatchSize:     2,
		Channels:      2,
		ChannelPrefix: "bench-g",
		ChannelType:   "group",
		ChannelPick:   channelPickRoundRobin,
		UIDPrefix:     "u",
		DevicePrefix:  "d",
		Token:         "token",
	})
	if err != nil {
		t.Fatalf("normalizeSendConfig() error = %v", err)
	}

	result, err := runSend(context.Background(), cfg)

	if err != nil {
		t.Fatalf("runSend() error = %v", err)
	}
	if result.Result != resultPass || result.Success != 5 || result.Errors != 0 {
		t.Fatalf("result = %#v, want pass with 5 successes", result)
	}
	if result.Gateways != 2 || result.Clients != 2 || result.Channels != 2 {
		t.Fatalf("traffic result = %#v", result)
	}
	if result.MinMessagesPerChannel != 2 || result.MaxMessagesPerChannel != 3 || result.AvgMessagesPerChannel != 2.5 {
		t.Fatalf("messages/channel = min %d avg %v max %d", result.MinMessagesPerChannel, result.AvgMessagesPerChannel, result.MaxMessagesPerChannel)
	}
	if got := fake.connectedUIDs(); len(got) != 2 || got[0] != "u-000001" || got[1] != "u-000002" {
		t.Fatalf("connected UIDs = %#v", got)
	}
	if got := fake.totalMessages(); got != 5 {
		t.Fatalf("sent messages = %d, want 5", got)
	}
}

func TestRunSendCountsNonSuccessSendackAsError(t *testing.T) {
	orig := newSendPool
	t.Cleanup(func() { newSendPool = orig })
	fake := &fakeSendPool{reason: frame.ReasonSystemError}
	newSendPool = func(cfg sendPoolConfig) (sendPool, error) {
		return fake, nil
	}
	cfg, err := normalizeSendConfig(sendConfig{
		GatewayAddrs: []string{"127.0.0.1:5100"},
		Clients:      1,
		Messages:     2,
		Size:         "4B",
		Channels:     1,
		Channel:      "g1",
		ChannelType:  "group",
		ChannelPick:  channelPickRoundRobin,
	})
	if err != nil {
		t.Fatalf("normalizeSendConfig() error = %v", err)
	}

	result, err := runSend(context.Background(), cfg)

	if err != nil {
		t.Fatalf("runSend() error = %v", err)
	}
	if result.Result != resultFail || result.Success != 0 || result.Errors != 2 {
		t.Fatalf("result = %#v, want fail with 2 errors", result)
	}
	if result.SendackReasons["system_error"] != 2 {
		t.Fatalf("sendack reasons = %#v, want system_error=2", result.SendackReasons)
	}
}

func TestRunSendScopesClientMsgNoToEachRun(t *testing.T) {
	orig := newSendPool
	t.Cleanup(func() { newSendPool = orig })
	var pools []*fakeSendPool
	newSendPool = func(cfg sendPoolConfig) (sendPool, error) {
		pool := &fakeSendPool{}
		pools = append(pools, pool)
		return pool, nil
	}
	cfg := sendConfig{
		GatewayAddrs: []string{"127.0.0.1:5100"},
		Clients:      1,
		Messages:     2,
		Size:         "4B",
		Channels:     1,
		Channel:      "g1",
		ChannelType:  "group",
		ChannelPick:  channelPickRoundRobin,
	}

	for i := 0; i < 2; i++ {
		result, err := runSend(context.Background(), cfg)
		if err != nil {
			t.Fatalf("runSend() iteration %d error = %v", i, err)
		}
		if result.Result != resultPass {
			t.Fatalf("runSend() iteration %d result = %s, want pass", i, result.Result)
		}
	}

	if len(pools) != 2 {
		t.Fatalf("created pools = %d, want 2", len(pools))
	}
	firstRun := pools[0].clientMsgNos()
	secondRun := pools[1].clientMsgNos()
	if len(firstRun) != 2 || len(secondRun) != 2 {
		t.Fatalf("clientMsgNos = %#v and %#v, want two per run", firstRun, secondRun)
	}
	if firstRun[0] == secondRun[0] {
		t.Fatalf("first ClientMsgNo repeated across runs: %q", firstRun[0])
	}
}

type fakeSendPool struct {
	mu         sync.Mutex
	identities []wkclient.Identity
	messages   []wkclient.RoutedMessage
	reason     frame.ReasonCode
}

func (p *fakeSendPool) Connect(_ context.Context, identities []wkclient.Identity) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.identities = append([]wkclient.Identity(nil), identities...)
	return nil
}

func (p *fakeSendPool) SendBatch(_ context.Context, messages []wkclient.RoutedMessage) ([]wkclient.SendResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, messages...)
	reason := p.reason
	if reason == 0 {
		reason = frame.ReasonSuccess
	}
	results := make([]wkclient.SendResult, len(messages))
	for i, msg := range messages {
		results[i] = wkclient.SendResult{
			ClientSeq:   msg.Message.ClientSeq,
			ClientMsgNo: msg.Message.ClientMsgNo,
			ReasonCode:  reason,
		}
	}
	return results, nil
}

func (p *fakeSendPool) Close() error { return nil }

func (p *fakeSendPool) connectedUIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.identities))
	for _, identity := range p.identities {
		out = append(out, identity.UID)
	}
	return out
}

func (p *fakeSendPool) totalMessages() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messages)
}

func (p *fakeSendPool) clientMsgNos() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.messages))
	for _, msg := range p.messages {
		out = append(out, msg.Message.ClientMsgNo)
	}
	return out
}
