package channelappend

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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

func TestRecipientProcessorObservesOfflineRecipientsAfterPresence(t *testing.T) {
	steps := &orderedStepsForDeliveryTest{}
	observer := &recordingOfflineRecipientObserverForDeliveryTest{steps: steps}
	pusher := &recordingOwnerPusherForDeliveryTest{steps: steps}
	batch := RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, Payload: []byte("hello")},
		Recipients: []Recipient{
			{UID: "u1"},
			{UID: "u2"},
			{UID: "u2"},
			{UID: "u3"},
		},
	}

	err := processRecipientBatch(context.Background(), batch, recipientPorts{
		presence: &recordingPresenceResolverForDeliveryTest{steps: steps, routes: []Route{
			{UID: "u1", OwnerNodeID: 3, SessionID: 20},
			{UID: "u3", OwnerNodeID: 4, SessionID: 30},
			{UID: "u3", OwnerNodeID: 4, SessionID: 31},
		}},
		pusher:                   pusher,
		offlineRecipientObserver: observer,
		deliveryRetryMaxAttempts: 2,
	})

	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got, want := observer.uids(), []string{"u2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("offline uids = %#v, want %#v", got, want)
	}
	if got, want := steps.snapshot(), []string{"presence", "offline", "push", "push"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("steps = %#v, want %#v", got, want)
	}
	if got := pusher.callCount(); got != 2 {
		t.Fatalf("push calls = %d, want two owner groups", got)
	}
}

func TestRecipientProcessorPrefersBatchOfflineObserver(t *testing.T) {
	observer := &recordingBatchOfflineRecipientObserverForDeliveryTest{}
	batch := RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{
			{UID: "u1"},
			{UID: "u2"},
			{UID: "u1"},
			{UID: "u3"},
			{UID: "u4"},
		},
	}

	err := processRecipientBatch(context.Background(), batch, recipientPorts{
		presence: &recordingPresenceResolverForDeliveryTest{routes: []Route{
			{UID: "u3", OwnerNodeID: 4, SessionID: 30},
		}},
		offlineRecipientObserver: observer,
	})

	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := observer.batchCallCount(); got != 1 {
		t.Fatalf("batch offline observer calls = %d, want 1", got)
	}
	if got := observer.fallbackCallCount(); got != 0 {
		t.Fatalf("single offline observer calls = %d, want none when batch observer is available", got)
	}
	if got, want := observer.batchUIDs(), []string{"u1", "u2", "u4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("batch offline uids = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorFallsBackToSingleOfflineObserver(t *testing.T) {
	observer := &recordingOfflineRecipientObserverForDeliveryTest{}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: []Recipient{
			{UID: "u1"},
			{UID: "u2"},
			{UID: "u1"},
		},
	}, recipientPorts{
		presence:                 &recordingPresenceResolverForDeliveryTest{routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}}},
		offlineRecipientObserver: observer,
	})

	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got, want := observer.uids(), []string{"u1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback offline uids = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorBatchOfflineObserverReceivesCopiedUIDSlice(t *testing.T) {
	uids := []string{"u1", "u2", "u3"}
	observer := &recordingBatchOfflineRecipientObserverForDeliveryTest{aliasAgainst: uids}

	observeOfflineRecipients(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
	}, uids, []Route{{UID: "u3", OwnerNodeID: 4, SessionID: 30}}, observer)

	if observer.sawUIDAlias() {
		t.Fatalf("batch offline observer received a UID slice alias")
	}
	if got, want := observer.batchUIDs(), []string{"u1", "u2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("batch offline uids = %#v, want %#v", got, want)
	}
}

func TestRecipientProcessorBatchOfflineObserverUsesOneCallForLargeFanout(t *testing.T) {
	const recipientCount = 100000
	recipients := make([]Recipient, 0, recipientCount)
	for i := 0; i < recipientCount; i++ {
		recipients = append(recipients, Recipient{UID: "u" + strconv.Itoa(i)})
	}
	observer := &recordingBatchOfflineRecipientObserverForDeliveryTest{}

	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event:      CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2},
		Recipients: recipients,
	}, recipientPorts{
		presence:                 &recordingPresenceResolverForDeliveryTest{},
		offlineRecipientObserver: observer,
	})

	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if got := observer.batchCallCount(); got != 1 {
		t.Fatalf("batch offline observer calls = %d, want 1", got)
	}
	if got := observer.fallbackCallCount(); got != 0 {
		t.Fatalf("single offline observer calls = %d, want none when batch observer is available", got)
	}
	if got := observer.batchUIDCount(); got != recipientCount {
		t.Fatalf("batch offline UID count = %d, want %d", got, recipientCount)
	}
}

func TestRecipientProcessorOfflineObserverSkipsNonDurableCandidates(t *testing.T) {
	cases := []struct {
		name  string
		event CommittedEnvelope
	}{
		{
			name:  "zero message seq",
			event: CommittedEnvelope{MessageID: 10, ChannelID: "g1", ChannelType: 2},
		},
		{
			name:  "sync once",
			event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, SyncOnce: true},
		},
		{
			name:  "request scoped",
			event: CommittedEnvelope{MessageID: 10, MessageSeq: 4, ChannelID: "g1", ChannelType: 2, MessageScopedUIDs: []string{"u2"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observer := &recordingOfflineRecipientObserverForDeliveryTest{}
			err := processRecipientBatch(context.Background(), RecipientBatch{
				Event:      tc.event,
				Recipients: []Recipient{{UID: "u2"}},
			}, recipientPorts{
				presence:                 &recordingPresenceResolverForDeliveryTest{},
				pusher:                   &recordingOwnerPusherForDeliveryTest{},
				offlineRecipientObserver: observer,
			})
			if err != nil {
				t.Fatalf("processRecipientBatch() error = %v", err)
			}
			if got := observer.uids(); len(got) != 0 {
				t.Fatalf("offline uids = %#v, want none", got)
			}
		})
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

type recordingOfflineRecipientObserverForDeliveryTest struct {
	steps  *orderedStepsForDeliveryTest
	mu     sync.Mutex
	events []OfflineRecipientEvent
}

func (o *recordingOfflineRecipientObserverForDeliveryTest) ObserveOfflineRecipient(_ context.Context, event OfflineRecipientEvent) {
	o.steps.add("offline")
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingOfflineRecipientObserverForDeliveryTest) uids() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.events))
	for _, event := range o.events {
		out = append(out, event.UID)
	}
	return out
}

type recordingBatchOfflineRecipientObserverForDeliveryTest struct {
	steps          *orderedStepsForDeliveryTest
	aliasAgainst   []string
	mu             sync.Mutex
	batchEvents    []OfflineRecipientsEvent
	fallbackEvents []OfflineRecipientEvent
	aliased        bool
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) ObserveOfflineRecipients(_ context.Context, event OfflineRecipientsEvent) {
	o.steps.add("offline")
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.aliasAgainst) > 0 && len(event.UIDs) > 0 && &o.aliasAgainst[0] == &event.UIDs[0] {
		o.aliased = true
	}
	copied := event
	copied.UIDs = append([]string(nil), event.UIDs...)
	o.batchEvents = append(o.batchEvents, copied)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) ObserveOfflineRecipient(_ context.Context, event OfflineRecipientEvent) {
	o.steps.add("offline")
	o.mu.Lock()
	defer o.mu.Unlock()
	o.fallbackEvents = append(o.fallbackEvents, event)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) batchCallCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.batchEvents)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) fallbackCallCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.fallbackEvents)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) batchUIDs() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.batchEvents) == 0 {
		return nil
	}
	return append([]string(nil), o.batchEvents[0].UIDs...)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) batchUIDCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.batchEvents) == 0 {
		return 0
	}
	return len(o.batchEvents[0].UIDs)
}

func (o *recordingBatchOfflineRecipientObserverForDeliveryTest) sawUIDAlias() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.aliased
}
