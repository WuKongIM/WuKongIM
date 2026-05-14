package workload

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestPersonWorkloadSendOneBuildsRecipientSendPacket(t *testing.T) {
	sender := newRecordingPersonClient()
	recipient := newRecordingPersonClient()
	sender.sendacks = append(sender.sendacks, &frame.SendackPacket{
		ClientSeq:   7,
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-ch7-msg7",
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
	require.Equal(t, "u2", sent.ChannelID)
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
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-ch3-msg42",
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
		Payload:     []byte("run=run-a profile=profile-a traffic=traffic-a channel=3 message=42"),
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

func TestPersonWorkloadRecvVerificationRejectsWrongFromUID(t *testing.T) {
	sender := newRecordingPersonClient()
	recipient := newRecordingPersonClient()
	ack := &frame.SendackPacket{
		MessageID:   100,
		MessageSeq:  42,
		ClientSeq:   42,
		ClientMsgNo: "bench-msg-run-a-profile-a-traffic-a-ch3-msg42",
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
		Payload:     []byte("run=run-a profile=profile-a traffic=traffic-a channel=3 message=42"),
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
	recvAckCalls []recvAckCall
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
	return nil
}

func (c *recordingPersonClient) ReadFrame(ctx context.Context) (frame.Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
