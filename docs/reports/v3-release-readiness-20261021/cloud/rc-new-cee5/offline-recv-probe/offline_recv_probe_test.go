package chatlifecycle

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// probeReadClient gates the existing fake transport, never verifier methods.
// A next read-entry receipt proves the prior SessionPool drain iteration joined.
type probeReadClient struct {
	*engineFakeClient
	clock   SessionClock
	permits chan struct{}
	entered chan int
	calls   int
	active  atomic.Int32
}

func (c *probeReadClient) ReadFrameTiming(ctx context.Context) (frame.Frame, SessionFrameTiming, error) {
	c.active.Add(1)
	defer c.active.Add(-1)
	c.calls++
	select {
	case c.entered <- c.calls:
	case <-ctx.Done():
		return nil, SessionFrameTiming{}, ctx.Err()
	}
	select {
	case <-c.permits:
	case <-ctx.Done():
		return nil, SessionFrameTiming{}, ctx.Err()
	}
	packet, err := c.engineFakeClient.ReadFrame(ctx)
	if _, ok := packet.(*frame.SendackPacket); ok {
		return packet, SessionFrameTiming{ObservedAt: c.clock.Now()}, err
	}
	// Match the production adapter: RECV currently carries no ingress time.
	return packet, SessionFrameTiming{}, err
}

type probeReadFactory struct {
	base  *engineFakeFactory
	clock SessionClock
	mu    sync.Mutex
	byUID map[string]*probeReadClient
}

func (f *probeReadFactory) NewSession(ctx context.Context, uid, token string) (SessionClient, error) {
	client, err := f.base.NewSession(ctx, uid, token)
	if err != nil {
		return nil, err
	}
	wrapped := &probeReadClient{
		engineFakeClient: client.(*engineFakeClient), clock: f.clock,
		permits: make(chan struct{}, 1), entered: make(chan int, 8),
	}
	f.mu.Lock()
	f.byUID[uid] = wrapped
	f.mu.Unlock()
	return wrapped, nil
}

func (f *probeReadFactory) get(uid string) *probeReadClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byUID[uid]
}

func probeAwaitRead(t *testing.T, client *probeReadClient, want int) {
	t.Helper()
	if got := <-client.entered; got != want {
		t.Fatalf("read entry = %d, want %d", got, want)
	}
}

type probeRecvReceipt struct {
	Case                     string           `json:"case"`
	DeadlineNanos            int64            `json:"deadline_nanos"`
	ACKObservedNanos         *int64           `json:"ack_observed_nanos"`
	RECVEnqueuedNanos        *int64           `json:"recv_enqueued_nanos"`
	RECVProcessedNanos       *int64           `json:"recv_processed_nanos"`
	ExpirationAdvanceNanos   *int64           `json:"expiration_advance_nanos"`
	QueueDepthAtExpiry       *int             `json:"queue_depth_at_expiry"`
	Sampled                  uint64           `json:"sampled"`
	Delivered                uint64           `json:"delivered"`
	Expired                  uint64           `json:"expired"`
	Losses                   uint64           `json:"losses"`
	Received                 uint64           `json:"received"`
	ReceiveAcknowledged      uint64           `json:"receive_acknowledged"`
	SendAcknowledged         uint64           `json:"send_acknowledged"`
	Outstanding              int              `json:"outstanding"`
	Evidence                 EvidenceSnapshot `json:"evidence"`
	SessionsAfterStop        int              `json:"sessions_after_stop"`
	ActiveReadCallsAfterStop int32            `json:"active_read_calls_after_stop"`
}

// TestOfflineRecvQueueExpiryProbe characterizes existing behavior, not a fix.
// Only fixed clocks and ordered transport-read permits determine each schedule.
func TestOfflineRecvQueueExpiryProbe(t *testing.T) {
	cases := []struct {
		name     string
		wantLoss uint64
		wantRecv uint64
		wantAck  uint64
		wantDone uint64
	}{
		{"timely_enqueue_timely_processing", 0, 1, 1, 1},
		{"timely_enqueue_delayed_processing", 1, 1, 1, 0},
		{"actual_late_ingress", 1, 1, 1, 0},
		{"no_recv", 1, 0, 1, 0},
		{"boundary_recv_before_expiry", 0, 1, 1, 1},
		{"boundary_expiry_before_recv", 1, 1, 1, 0},
		{"recv_before_positive_ack", 0, 1, 1, 1},
		{"no_observed_positive_ack", 0, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newEngineTestFixture(t, engineTestLimits{
				OnlineUsers: 2, SessionDuration: time.Hour,
				WorkCapacity: 64, InflightCapacity: 16, MaxWorkPerAdvance: 16,
				AttemptTimeout: 20 * time.Second,
			})
			// The shared historical fixture uses a minute. Inject the exact run's
			// reviewed 10-second setting before any goroutine or message exists.
			fixture.verifier.config.CorrelationDeadline = 10 * time.Second
			fixture.factory.autoAck = true
			factory := &probeReadFactory{base: fixture.factory, clock: fixture.clock, byUID: map[string]*probeReadClient{}}
			fixture.pool.factory = factory
			fixture.pool.heartbeatSleep = func(ctx context.Context, _ time.Duration) error {
				<-ctx.Done()
				return ctx.Err()
			}
			if err := fixture.engine.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fixture.engine.Stop() })
			start := fixture.clock.Now()
			sender, recipient := fixture.identity.UID(0), fixture.identity.UID(1)
			for index, uid := range []string{sender, recipient} {
				if _, err := fixture.pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: uint64(index), LoginOrdinal: uint64(index)}); err != nil {
					t.Fatal(err)
				}
			}
			senderClient, recipientClient := factory.get(sender), factory.get(recipient)
			probeAwaitRead(t, senderClient, 1)
			probeAwaitRead(t, recipientClient, 1)
			var intent TrafficIntent
			for ordinal := uint64(0); ordinal < 100; ordinal++ {
				candidate := fixture.intent(t, sender, recipient, ordinal, TrafficPerson)
				correlate, err := fixture.verifier.ShouldCorrelate(candidate.Logical)
				if err != nil {
					t.Fatal(err)
				}
				if correlate {
					intent = candidate
					break
				}
			}
			if intent.Logical.ClientMsgNo == "" {
				t.Fatal("no sampled message")
			}
			intent.correlationRecipient = recipient
			admissionResult := make(chan error, 1)
			if err := fixture.engine.enqueueBlocking(engineCommand{run: func() {
				admissionResult <- fixture.engine.addSendWorkAt(intent, 0, start, start)
			}}); err != nil {
				t.Fatal(err)
			}
			if admissionErr := <-admissionResult; admissionErr != nil {
				t.Fatal(admissionErr)
			}
			advance := func(offset time.Duration) {
				fixture.clock.Set(start.Add(offset))
				if _, err := fixture.engine.Advance(start.Add(offset)); err != nil {
					t.Fatal(err)
				}
			}
			advance(0)
			if fixture.factory.sentCount() != 1 || len(senderClient.frames) != 1 {
				t.Fatal("SEND/ACK did not enter the actual fake transport")
			}
			receipt := probeRecvReceipt{Case: tc.name, DeadlineNanos: int64(10 * time.Second)}
			observeACK := func(offset time.Duration) {
				fixture.clock.Set(start.Add(offset))
				senderClient.permits <- struct{}{}
				probeAwaitRead(t, senderClient, 2)
				x := int64(offset)
				receipt.ACKObservedNanos = &x
				advance(offset)
			}
			enqueueRECV := func(offset time.Duration) {
				fixture.clock.Set(start.Add(offset))
				recipientClient.frames <- mustRecvPacket(t, fixture.traffic, intent.Logical, 1, 1)
				if len(recipientClient.frames) != 1 {
					t.Fatal("RECV bypassed the permit barrier")
				}
				x := int64(offset)
				receipt.RECVEnqueuedNanos = &x
			}
			processRECV := func(offset time.Duration) {
				fixture.clock.Set(start.Add(offset))
				recipientClient.permits <- struct{}{}
				probeAwaitRead(t, recipientClient, 2)
				if len(recipientClient.frames) != 0 {
					t.Fatal("RECV queue was not drained")
				}
				x := int64(offset)
				receipt.RECVProcessedNanos = &x
			}
			expire := func() {
				depth := len(recipientClient.frames)
				receipt.QueueDepthAtExpiry = &depth
				x := int64(10 * time.Second)
				receipt.ExpirationAdvanceNanos = &x
				advance(10 * time.Second)
			}
			if tc.name != "recv_before_positive_ack" && tc.name != "no_observed_positive_ack" {
				observeACK(0)
			}
			switch tc.name {
			case "timely_enqueue_timely_processing":
				enqueueRECV(9 * time.Second)
				processRECV(9 * time.Second)
				expire()
			case "timely_enqueue_delayed_processing":
				enqueueRECV(9 * time.Second)
				expire()
				processRECV(11 * time.Second)
			case "actual_late_ingress":
				expire()
				enqueueRECV(11 * time.Second)
				processRECV(11 * time.Second)
			case "no_recv", "no_observed_positive_ack":
				expire()
			case "boundary_recv_before_expiry":
				enqueueRECV(10 * time.Second)
				processRECV(10 * time.Second)
				expire()
			case "boundary_expiry_before_recv":
				enqueueRECV(10 * time.Second)
				expire()
				processRECV(10 * time.Second)
			case "recv_before_positive_ack":
				enqueueRECV(9 * time.Second)
				processRECV(9 * time.Second)
				expire()
				observeACK(11 * time.Second)
			}
			snapshot := fixture.verifier.Snapshot()
			if snapshot.Sampled != 1 || snapshot.SampledExpired != tc.wantLoss || snapshot.Losses != tc.wantLoss ||
				snapshot.Received != tc.wantRecv || snapshot.ReceiveAcknowledged != tc.wantRecv ||
				snapshot.Acknowledged != tc.wantAck || snapshot.SampledDelivered != tc.wantDone ||
				snapshot.Attempts != 1 || snapshot.UnknownSendacks != 0 || snapshot.ReceiveFailures != 0 {
				t.Fatalf("unexpected mechanism outcome: %+v", snapshot)
			}
			receipt.Sampled, receipt.Delivered, receipt.Expired, receipt.Losses = snapshot.Sampled, snapshot.SampledDelivered, snapshot.SampledExpired, snapshot.Losses
			receipt.Received, receipt.ReceiveAcknowledged, receipt.SendAcknowledged = snapshot.Received, snapshot.ReceiveAcknowledged, snapshot.Acknowledged
			receipt.Outstanding = snapshot.CorrelationCurrent
			receipt.Evidence = fixture.evidence.Snapshot()
			if tc.wantLoss == 1 {
				if len(receipt.Evidence.Classes) != 1 || receipt.Evidence.Classes[0].Count != 1 || len(receipt.Evidence.Classes[0].First) != 1 {
					t.Fatal("missing bounded expired sample")
				}
				example := receipt.Evidence.Classes[0].First[0]
				if example.Code != FailureCodeCorrelationExpired || example.SampleIndex != intent.Logical.LogicalSend || example.Fingerprint != messageFingerprint(intent.Logical.ClientMsgNo) || example.Value != uint64(10*time.Second) {
					t.Fatal("wrong bounded expired sample")
				}
			}
			if err := fixture.engine.Stop(); err != nil {
				t.Fatal(err)
			}
			receipt.SessionsAfterStop = fixture.pool.Counts().Online
			receipt.ActiveReadCallsAfterStop = senderClient.active.Load() + recipientClient.active.Load()
			if receipt.SessionsAfterStop != 0 || receipt.ActiveReadCallsAfterStop != 0 {
				t.Fatal("cleanup did not join session ownership")
			}
			encoded, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("RECV_PROBE %s", encoded)
		})
	}
}
