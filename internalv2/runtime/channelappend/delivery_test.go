package channelappend

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRecipientProcessorPushesDeliveryWithPresenceAndPusher(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{}
	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{{UID: "u2"}},
	}, recipientPorts{
		presence:                 &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		pusher:                   pusher,
		deliveryRetryMaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := pusher.callCount(); got != 1 {
		t.Fatalf("push calls = %d, want delivery push", got)
	}
}

func TestRecipientProcessorSkipsOnlySameSenderOwnerSessionEcho(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{}
	resolver := &recordingPresenceResolverForDeliveryTest{routes: []Route{
		{UID: "u1", OwnerNodeID: 7, SessionID: 100, DeviceID: "same-session"},
		{UID: "u1", OwnerNodeID: 7, SessionID: 101, DeviceID: "same-owner-other-session"},
		{UID: "u1", OwnerNodeID: 8, SessionID: 100, DeviceID: "other-owner-same-session"},
		{UID: "u2", OwnerNodeID: 7, SessionID: 200, DeviceID: "other-user"},
	}}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{
			MessageID:         10,
			MessageSeq:        4,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "u1",
			SenderNodeID:      7,
			SenderSessionID:   100,
			ServerTimestampMS: 123,
		},
		Recipients: []Recipient{{UID: "u1"}, {UID: "u2"}},
	}, recipientPorts{
		presence:                 resolver,
		pusher:                   pusher,
		deliveryRetryMaxAttempts: 2,
	})
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}

	got := deviceIDSetForDeliveryTest(pusher.deviceIDs())
	want := map[string]bool{"same-owner-other-session": true, "other-owner-same-session": true, "other-user": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pushed device ids = %#v, want %#v", got, want)
	}
}

func TestRetryableOwnerPushRoutesAreRetriedWithBoundedBackoff(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{
		results: []PushResult{
			{Retryable: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
			{Accepted: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		},
	}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{
			{UID: "u2"},
		},
	}, recipientPorts{
		presence:                    &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		pusher:                      pusher,
		deliveryRetryMaxAttempts:    3,
		deliveryRetryInitialBackoff: time.Millisecond,
		deliveryRetryMaxBackoff:     time.Millisecond,
	})
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := pusher.callCount(); got != 2 {
		t.Fatalf("push calls = %d, want retry then success", got)
	}
}

func TestRetryableOwnerPushRoutesReturnErrorAfterMaxAttempts(t *testing.T) {
	pusher := &recordingOwnerPusherForDeliveryTest{
		results: []PushResult{
			{Retryable: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
			{Retryable: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		},
	}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{{UID: "u2"}},
	}, recipientPorts{
		presence:                    &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		pusher:                      pusher,
		deliveryRetryMaxAttempts:    2,
		deliveryRetryInitialBackoff: time.Millisecond,
		deliveryRetryMaxBackoff:     time.Millisecond,
	})
	if !errors.Is(err, ErrDeliveryRetryExhausted) {
		t.Fatalf("processRecipientBatch() error = %v, want ErrDeliveryRetryExhausted", err)
	}
	if got := pusher.callCount(); got != 2 {
		t.Fatalf("push calls = %d, want max attempts", got)
	}
}

type orderedStepsForDeliveryTest struct {
	mu    sync.Mutex
	steps []string
}

func (s *orderedStepsForDeliveryTest) add(step string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, step)
}

func (s *orderedStepsForDeliveryTest) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steps...)
}

type recordingPresenceResolverForDeliveryTest struct {
	steps  *orderedStepsForDeliveryTest
	routes []Route
}

func (r *recordingPresenceResolverForDeliveryTest) EndpointsByUIDs(_ context.Context, _ []string) ([]Route, error) {
	r.steps.add("presence")
	return append([]Route(nil), r.routes...), nil
}

type recordingOwnerPusherForDeliveryTest struct {
	steps    *orderedStepsForDeliveryTest
	mu       sync.Mutex
	commands []PushCommand
	results  []PushResult
}

func (p *recordingOwnerPusherForDeliveryTest) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	p.steps.add("push")
	p.mu.Lock()
	defer p.mu.Unlock()
	p.commands = append(p.commands, cmd.Clone())
	if len(p.results) >= len(p.commands) {
		return p.results[len(p.commands)-1].Clone(), nil
	}
	return PushResult{Accepted: append([]Route(nil), cmd.Routes...)}, nil
}

type payloadAliasOwnerPusherForDeliveryTest struct {
	mu      sync.Mutex
	payload []byte
	calls   int
	aliased bool
}

func (p *payloadAliasOwnerPusherForDeliveryTest) Push(_ context.Context, cmd PushCommand) (PushResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if len(p.payload) > 0 && len(cmd.Envelope.Payload) > 0 && &cmd.Envelope.Payload[0] == &p.payload[0] {
		p.aliased = true
	}
	return PushResult{Accepted: append([]Route(nil), cmd.Routes...)}, nil
}

func (p *payloadAliasOwnerPusherForDeliveryTest) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *payloadAliasOwnerPusherForDeliveryTest) sawAlias() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.aliased
}

func (p *recordingOwnerPusherForDeliveryTest) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.commands)
}

func (p *recordingOwnerPusherForDeliveryTest) deviceIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, cmd := range p.commands {
		for _, route := range cmd.Routes {
			out = append(out, route.DeviceID)
		}
	}
	return out
}

func deviceIDSetForDeliveryTest(deviceIDs []string) map[string]bool {
	out := make(map[string]bool)
	for _, deviceID := range deviceIDs {
		out[deviceID] = true
	}
	return out
}
