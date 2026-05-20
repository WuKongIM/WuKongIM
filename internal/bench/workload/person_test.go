package workload

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestPersonWorkloadSendOneBuildsRecipientSendPacket(t *testing.T) {
	sender := newRecordingPersonClient()
	recipient := newRecordingPersonClient()
	sender.sendacks = append(sender.sendacks, &frame.SendackPacket{
		ClientSeq:   7,
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-run-ch7-msg7",
		ReasonCode:  frame.ReasonSuccess,
	})

	workload, err := NewPersonWorkload(PersonConfig{
		RunID:            "run-a",
		ProfileName:      "profile-a",
		TrafficName:      "traffic-a",
		SenderUID:        "u1",
		RecipientUID:     "u2",
		ClientMsgPrefix:  "bench-msg",
		PayloadSizeBytes: 0,
	}, map[string]PersonClient{
		"u1": sender,
		"u2": recipient,
	})
	require.NoError(t, err)

	require.NoError(t, workload.SendOne(context.Background(), 7))

	require.Len(t, sender.sentFrames, 1)
	sent := sender.sentFrames[0]
	require.Equal(t, "u2@u1", sent.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, sent.ChannelType)
	require.Contains(t, sent.ClientMsgNo, "bench-msg")
	require.Contains(t, sent.ClientMsgNo, "run-a")
	require.Contains(t, sent.ClientMsgNo, "profile-a")
	require.Contains(t, sent.ClientMsgNo, "traffic-a")
	require.Contains(t, sent.ClientMsgNo, "ch7")
	require.Contains(t, sent.ClientMsgNo, "msg7")
}

func TestPersonWorkloadSendOneMatchesSendackAndVerifiesRecvWhenEnabled(t *testing.T) {
	sender := newRecordingPersonClient()
	recipient := newRecordingPersonClient()
	ack := &frame.SendackPacket{
		MessageID:   100,
		MessageSeq:  42,
		ClientSeq:   42,
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-run-ch3-msg42",
		ReasonCode:  frame.ReasonSuccess,
	}
	sender.sendacks = append(sender.sendacks, ack)
	recipient.recvFrames = append(recipient.recvFrames, &frame.RecvPacket{
		MessageID:   100,
		MessageSeq:  42,
		FromUID:     "u1",
		ChannelID:   "u1",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: ack.ClientMsgNo,
		Payload:     []byte("run=run-a profile=profile-a traffic=traffic-a phase=run channel=3 message=42"),
	})

	workload, err := NewPersonWorkload(PersonConfig{
		RunID:            "run-a",
		ProfileName:      "profile-a",
		TrafficName:      "traffic-a",
		Pairs:            []PersonPair{{ChannelIndex: 3, SenderUID: "u1", RecipientUID: "u2"}},
		ClientMsgPrefix:  "bench-msg",
		VerifyRecvMode:   "full",
		RecvAck:          true,
		PayloadSizeBytes: 0,
		Metrics:          metrics.NewRegistry(),
	}, map[string]PersonClient{
		"u1": sender,
		"u2": recipient,
	})
	require.NoError(t, err)

	require.NoError(t, workload.SendOne(context.Background(), 42))

	require.Len(t, sender.recvAckCalls, 0)
	require.Len(t, recipient.recvAckCalls, 1)
	require.Equal(t, int64(100), recipient.recvAckCalls[0].messageID)
	require.Equal(t, uint64(42), recipient.recvAckCalls[0].messageSeq)
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("person_send_success_total", nil))
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("person_recv_success_total", nil))
	require.NotEmpty(t, workload.Metrics().LatencyValues("person_send_latency_seconds", nil))
	require.NotEmpty(t, workload.Metrics().LatencyValues("person_recv_latency_seconds", nil))
}

func TestPersonWorkloadFullRecvVerificationAllowsSmallPayload(t *testing.T) {
	sender := newRecordingPersonClient()
	recipient := newRecordingPersonClient()
	ack := &frame.SendackPacket{
		MessageID:   100,
		MessageSeq:  42,
		ClientSeq:   42,
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-run-ch3-msg42",
		ReasonCode:  frame.ReasonSuccess,
	}
	sender.sendacks = append(sender.sendacks, ack)
	recipient.recvFrames = append(recipient.recvFrames, &frame.RecvPacket{
		MessageID:   100,
		MessageSeq:  42,
		FromUID:     "u1",
		ChannelID:   "u1",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: ack.ClientMsgNo,
		Payload:     []byte("run=run-"),
	})
	workload, err := NewPersonWorkload(PersonConfig{
		RunID:            "run-a",
		ProfileName:      "profile-a",
		TrafficName:      "traffic-a",
		Pairs:            []PersonPair{{ChannelIndex: 3, SenderUID: "u1", RecipientUID: "u2"}},
		ClientMsgPrefix:  "bench-msg",
		VerifyRecvMode:   "full",
		PayloadSizeBytes: 8,
		Metrics:          metrics.NewRegistry(),
	}, map[string]PersonClient{"u1": sender, "u2": recipient})
	require.NoError(t, err)

	require.NoError(t, workload.SendOne(context.Background(), 42))
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("person_recv_success_total", nil))
}

func TestPersonWorkloadRecvVerificationRejectsWrongFromUID(t *testing.T) {
	sender := newRecordingPersonClient()
	recipient := newRecordingPersonClient()
	ack := &frame.SendackPacket{
		MessageID:   100,
		MessageSeq:  42,
		ClientSeq:   42,
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-run-ch3-msg42",
		ReasonCode:  frame.ReasonSuccess,
	}
	sender.sendacks = append(sender.sendacks, ack)
	recipient.recvFrames = append(recipient.recvFrames, &frame.RecvPacket{
		MessageID:   100,
		MessageSeq:  42,
		FromUID:     "intruder",
		ChannelID:   "u1",
		ChannelType: frame.ChannelTypePerson,
		ClientMsgNo: ack.ClientMsgNo,
		Payload:     []byte("run=run-a profile=profile-a traffic=traffic-a phase=run channel=3 message=42"),
	})
	workload, err := NewPersonWorkload(PersonConfig{
		RunID:           "run-a",
		ProfileName:     "profile-a",
		TrafficName:     "traffic-a",
		Pairs:           []PersonPair{{ChannelIndex: 3, SenderUID: "u1", RecipientUID: "u2"}},
		ClientMsgPrefix: "bench-msg",
		VerifyRecvMode:  "full",
		Metrics:         metrics.NewRegistry(),
	}, map[string]PersonClient{"u1": sender, "u2": recipient})
	require.NoError(t, err)

	err = workload.SendOne(context.Background(), 42)

	require.Error(t, err)
	require.Equal(t, uint64(1), workload.Metrics().CounterValue("person_recv_error_total", nil))
	require.NotEmpty(t, workload.Metrics().ErrorSamples())
}

func TestPersonWorkloadSendOneRecordsFailures(t *testing.T) {
	sender := newRecordingPersonClient()
	sender.sendacks = append(sender.sendacks, &frame.SendackPacket{
		ClientSeq:   1,
		ClientMsgNo: "unexpected",
		ReasonCode:  frame.ReasonSuccess,
	})

	workload, err := NewPersonWorkload(PersonConfig{
		RunID:           "run-a",
		ProfileName:     "profile-a",
		TrafficName:     "traffic-a",
		SenderUID:       "u1",
		RecipientUID:    "u2",
		ClientMsgPrefix: "bench-msg",
		Metrics:         metrics.NewRegistry(),
	}, map[string]PersonClient{"u1": sender, "u2": newRecordingPersonClient()})
	require.NoError(t, err)

	err = workload.SendOne(context.Background(), 1)

	require.Error(t, err)
	require.True(t, workload.Metrics().CounterValue("person_send_error_total", nil) > 0)
	require.NotEmpty(t, workload.Metrics().ErrorSamples())
}

func TestPersonWorkloadConnectsAssignedPairClients(t *testing.T) {
	sender := newRecordingPersonClient()
	recipient := newRecordingPersonClient()
	workload, err := NewPersonWorkload(PersonConfig{
		DevicePrefix:    "bench-device",
		ClientMsgPrefix: "bench-msg",
		Pairs: []PersonPair{
			{ChannelIndex: 3, SenderUID: "u1", RecipientUID: "u2"},
			{ChannelIndex: 4, SenderUID: "u1", RecipientUID: "u3"},
		},
	}, map[string]PersonClient{"u1": sender, "u2": recipient, "u3": newRecordingPersonClient()})
	require.NoError(t, err)

	require.NoError(t, workload.Connect(context.Background()))

	require.Equal(t, []personConnectCall{{uid: "u1", deviceID: "bench-device-u1"}}, sender.connected)
	require.Equal(t, []personConnectCall{{uid: "u2", deviceID: "bench-device-u2"}}, recipient.connected)
}

func TestPersonWorkloadRejectsMissingClient(t *testing.T) {
	_, err := NewPersonWorkload(PersonConfig{
		RunID:           "run-a",
		ProfileName:     "profile-a",
		TrafficName:     "traffic-a",
		SenderUID:       "u1",
		RecipientUID:    "u2",
		ClientMsgPrefix: "bench-msg",
	}, map[string]PersonClient{})

	require.Error(t, err)
}

type recordingPersonClient struct {
	mu           sync.Mutex
	connected    []personConnectCall
	sentFrames   []*frame.SendPacket
	sendacks     []*frame.SendackPacket
	recvFrames   []frame.Frame
	readErrors   []error
	recvAckCalls []recvAckCall
	autoSendack  bool
}

type personConnectCall struct {
	uid, deviceID string
}

type recvAckCall struct {
	messageID  int64
	messageSeq uint64
}

func newRecordingPersonClient() *recordingPersonClient {
	return &recordingPersonClient{}
}

func (c *recordingPersonClient) Connect(ctx context.Context, uid, deviceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = append(c.connected, personConnectCall{uid: uid, deviceID: deviceID})
	return nil
}

func (c *recordingPersonClient) Send(ctx context.Context, pkt *frame.SendPacket) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cloned := *pkt
	c.sentFrames = append(c.sentFrames, &cloned)
	if c.autoSendack {
		c.sendacks = append(c.sendacks, &frame.SendackPacket{ClientSeq: pkt.ClientSeq, ClientMsgNo: pkt.ClientMsgNo, ReasonCode: frame.ReasonSuccess})
	}
	return nil
}

func (c *recordingPersonClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.readErrors) > 0 {
		err := c.readErrors[0]
		c.readErrors = c.readErrors[1:]
		return nil, err
	}
	if len(c.sendacks) > 0 {
		f := c.sendacks[0]
		c.sendacks = c.sendacks[1:]
		return f, nil
	}
	if len(c.recvFrames) > 0 {
		f := c.recvFrames[0]
		c.recvFrames = c.recvFrames[1:]
		return f, nil
	}
	return nil, io.EOF
}

func (c *recordingPersonClient) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recvAckCalls = append(c.recvAckCalls, recvAckCall{messageID: messageID, messageSeq: messageSeq})
	return nil
}

func (c *recordingPersonClient) Close() error { return nil }

var _ PersonClient = (*recordingPersonClient)(nil)

func TestConcurrentPersonClientBuffersUnmatchedFrames(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.sendacks = append(raw.sendacks,
		&frame.SendackPacket{ClientSeq: 1, ClientMsgNo: "msg-a", ReasonCode: frame.ReasonSuccess},
		&frame.SendackPacket{ClientSeq: 2, ClientMsgNo: "msg-b", ReasonCode: frame.ReasonSuccess},
	)
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	got, err := readFrameMatching(ctx, wrapped, func(f frame.Frame) bool {
		ack, ok := f.(*frame.SendackPacket)
		return ok && ack.ClientMsgNo == "msg-b"
	})
	require.NoError(t, err)
	require.Equal(t, "msg-b", got.(*frame.SendackPacket).ClientMsgNo)

	got, err = readFrameMatching(ctx, wrapped, func(f frame.Frame) bool {
		ack, ok := f.(*frame.SendackPacket)
		return ok && ack.ClientMsgNo == "msg-a"
	})
	require.NoError(t, err)
	require.Equal(t, "msg-a", got.(*frame.SendackPacket).ClientMsgNo)
}

func TestConcurrentPersonClientServesBufferedFrameWhileAnotherReadBlocks(t *testing.T) {
	raw := newBlockingReadPersonClient([]frame.Frame{
		&frame.SendackPacket{ClientSeq: 2, ClientMsgNo: "msg-b", ReasonCode: frame.ReasonSuccess},
	})
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	firstDone := make(chan error, 1)
	go func() {
		_, err := readFrameMatching(ctxA, wrapped, func(f frame.Frame) bool {
			ack, ok := f.(*frame.SendackPacket)
			return ok && ack.ClientMsgNo == "msg-a"
		})
		firstDone <- err
	}()
	require.Eventually(t, func() bool {
		select {
		case <-raw.blockStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	secondDone := make(chan error, 1)
	go func() {
		got, err := readFrameMatching(context.Background(), wrapped, func(f frame.Frame) bool {
			ack, ok := f.(*frame.SendackPacket)
			return ok && ack.ClientMsgNo == "msg-b"
		})
		if err != nil {
			secondDone <- err
			return
		}
		require.Equal(t, "msg-b", got.(*frame.SendackPacket).ClientMsgNo)
		secondDone <- nil
	}()

	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-time.After(50 * time.Millisecond):
		cancelA()
		<-firstDone
		t.Fatal("buffered matching frame was blocked behind another goroutine's read")
	}
	cancelA()
	<-firstDone
}

func TestAutoRecvAckDrainsAndBuffersRecvFrames(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.recvFrames = append(raw.recvFrames, &frame.RecvPacket{
		MessageID:   42,
		MessageSeq:  7,
		ClientMsgNo: "msg-a",
		FromUID:     "sender",
		ChannelID:   "channel-a",
		ChannelType: frame.ChannelTypeGroup,
	})
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	stop := StartAutoRecvAck(map[string]PersonClient{"u1": wrapped})
	defer stop()

	require.Eventually(t, func() bool {
		raw.mu.Lock()
		defer raw.mu.Unlock()
		return len(raw.recvAckCalls) == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []recvAckCall{{messageID: 42, messageSeq: 7}}, raw.recvAckCalls)

	got, err := readFrameMatching(context.Background(), wrapped, func(f frame.Frame) bool {
		recv, ok := f.(*frame.RecvPacket)
		return ok && recv.ClientMsgNo == "msg-a"
	})
	require.NoError(t, err)
	require.Equal(t, int64(42), got.(*frame.RecvPacket).MessageID)
}

func TestAutoRecvAckSuppressesDuplicateExplicitRecvAck(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.recvFrames = append(raw.recvFrames, &frame.RecvPacket{
		MessageID:   44,
		MessageSeq:  9,
		ClientMsgNo: "msg-direct",
		FromUID:     "sender",
		ChannelID:   "channel-a",
		ChannelType: frame.ChannelTypePerson,
	})
	wrapped := &matchingPersonClient{
		client:      raw,
		bufferLimit: defaultMatchingBufferLimit,
		autoRecvAck: true,
	}

	got, err := readFrameMatching(context.Background(), wrapped, func(f frame.Frame) bool {
		recv, ok := f.(*frame.RecvPacket)
		return ok && recv.ClientMsgNo == "msg-direct"
	})
	require.NoError(t, err)
	require.Equal(t, int64(44), got.(*frame.RecvPacket).MessageID)
	require.Equal(t, []recvAckCall{{messageID: 44, messageSeq: 9}}, raw.recvAckCalls)

	require.NoError(t, wrapped.RecvAck(context.Background(), 44, 9))
	require.Equal(t, []recvAckCall{{messageID: 44, messageSeq: 9}}, raw.recvAckCalls)
}

func TestAutoRecvAckContinuesAfterIdleReadTimeout(t *testing.T) {
	raw := newRecordingPersonClient()
	raw.readErrors = append(raw.readErrors, context.DeadlineExceeded)
	raw.recvFrames = append(raw.recvFrames, &frame.RecvPacket{
		MessageID:   43,
		MessageSeq:  8,
		ClientMsgNo: "msg-after-timeout",
	})
	wrapped := WrapPersonClientsForConcurrentReads(map[string]PersonClient{"u1": raw})["u1"]
	stop := StartAutoRecvAck(map[string]PersonClient{"u1": wrapped})
	defer stop()

	require.Eventually(t, func() bool {
		raw.mu.Lock()
		defer raw.mu.Unlock()
		return len(raw.recvAckCalls) == 1
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, []recvAckCall{{messageID: 43, messageSeq: 8}}, raw.recvAckCalls)
}

type blockingReadPersonClient struct {
	frames       []frame.Frame
	blockStarted chan struct{}
	blockOnce    sync.Once
}

func newBlockingReadPersonClient(frames []frame.Frame) *blockingReadPersonClient {
	return &blockingReadPersonClient{frames: append([]frame.Frame(nil), frames...), blockStarted: make(chan struct{})}
}

func (c *blockingReadPersonClient) Connect(ctx context.Context, uid, deviceID string) error {
	return nil
}
func (c *blockingReadPersonClient) Send(ctx context.Context, pkt *frame.SendPacket) error { return nil }
func (c *blockingReadPersonClient) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	return nil
}
func (c *blockingReadPersonClient) Close() error { return nil }

func (c *blockingReadPersonClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if len(c.frames) > 0 {
		f := c.frames[0]
		c.frames = c.frames[1:]
		return f, nil
	}
	c.blockOnce.Do(func() { close(c.blockStarted) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPersonWorkloadRunHonorsRateDurationPerChannel(t *testing.T) {
	clients := map[string]*recordingPersonClient{
		"u1": newRecordingPersonClient(),
		"u2": newRecordingPersonClient(),
		"u3": newRecordingPersonClient(),
		"u4": newRecordingPersonClient(),
	}
	for _, client := range clients {
		client.autoSendack = true
	}
	var sleeps []time.Duration
	workload, err := NewPersonWorkload(PersonConfig{
		RunID:           "run-a",
		ProfileName:     "profile-a",
		TrafficName:     "traffic-a",
		ClientMsgPrefix: "bench-msg",
		RunDuration:     time.Second,
		Rate:            model.Rate{PerSecond: 2},
		Pairs: []PersonPair{
			{ChannelIndex: 0, SenderUID: "u1", RecipientUID: "u2"},
			{ChannelIndex: 1, SenderUID: "u3", RecipientUID: "u4"},
		},
		Metrics: metrics.NewRegistry(),
		sleep: func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		},
	}, map[string]PersonClient{"u1": clients["u1"], "u2": clients["u2"], "u3": clients["u3"], "u4": clients["u4"]})
	require.NoError(t, err)

	require.NoError(t, workload.Run(context.Background()))

	require.Len(t, clients["u1"].sentFrames, 2)
	require.Len(t, clients["u3"].sentFrames, 2)
	require.Len(t, sleeps, 4)
	for _, d := range sleeps {
		require.Equal(t, 250*time.Millisecond, d)
	}
	require.Equal(t, uint64(4), workload.Metrics().CounterValue("person_send_success_total", nil))
}
