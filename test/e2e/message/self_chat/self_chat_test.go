//go:build e2e

package self_chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const selfUID = "e2e-self-chat-user"

// messageFact is the public persistent projection compared with SENDACK and RECV.
type messageFact struct {
	ID          string `json:"message_idstr"`
	Seq         uint64 `json:"message_seq"`
	ClientMsgNo string `json:"client_msg_no"`
	FromUID     string `json:"from_uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	Payload     []byte `json:"payload"`
	Header      struct {
		NoPersist int `json:"no_persist"`
		SyncOnce  int `json:"sync_once"`
		RedDot    int `json:"red_dot"`
	} `json:"header"`
}

type historyReceipt struct {
	Phase            string        `json:"phase"`
	NodeID           uint64        `json:"node_id"`
	Pages            int           `json:"pages"`
	TemporaryRetries int           `json:"temporary_retries"`
	Messages         []messageFact `json:"messages"`
	LastPage         *historyPage  `json:"last_page,omitempty"`
	Complete         bool          `json:"complete"`
}

// historyPage retains the successful raw page even when a content assertion fails.
type historyPage struct {
	StartSeq uint64        `json:"start_message_seq"`
	More     int           `json:"more"`
	Messages []messageFact `json:"messages"`
}

type receiveReceipt struct {
	Phase     string `json:"phase"`
	Device    string `json:"device"`
	MessageID string `json:"message_id"`
	Seq       uint64 `json:"message_seq"`
	Duplicate bool   `json:"duplicate"`
}

type caseReceipt struct {
	Nodes              int              `json:"nodes"`
	Phase              string           `json:"phase"`
	Passed             bool             `json:"passed"`
	BinarySHA256       string           `json:"binary_sha256"`
	InvalidAuthRefused bool             `json:"invalid_auth_refused"`
	Restarts           int              `json:"whole_cluster_restarts"`
	Acknowledged       []messageFact    `json:"acknowledged"`
	Receives           []receiveReceipt `json:"receives"`
	History            []historyReceipt `json:"history"`
	SuppressionWindows int              `json:"sender_suppression_windows"`
}

// device tracks exact messages allowed on this session, including safe redelivery.
type device struct {
	name    string
	client  *wkclient.Client
	allowed map[string]messageFact
	seen    map[string]bool
	closed  bool
}

func TestSelfChatPersistsAcrossClusterRestart(t *testing.T) {
	report := struct {
		Version             int            `json:"schema_version"`
		StartedAt           time.Time      `json:"started_at"`
		FinishedAt          time.Time      `json:"finished_at"`
		Passed              bool           `json:"passed"`
		HashSlots           int            `json:"hash_slots"`
		TokenAuthentication string         `json:"token_authentication"`
		SuppressionWindowMS int            `json:"suppression_window_ms"`
		Cases               []*caseReceipt `json:"cases"`
	}{Version: 1, StartedAt: time.Now().UTC(), HashSlots: 256, TokenAuthentication: "production_default", SuppressionWindowMS: 1000}
	defer func() {
		report.Passed, report.FinishedAt = !t.Failed(), time.Now().UTC()
		path := os.Getenv("WK_E2E_SELF_CHAT_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-self-chat.json")
		}
		body, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.MkdirAll(filepath.Dir(path), 0755)
		}
		if err == nil {
			err = os.WriteFile(path, append(body, '\n'), 0600)
		}
		if err != nil {
			t.Errorf("write self-chat receipt: %v", err)
		}
		t.Logf("self-chat receipt: %s", path)
	}()
	for _, nodes := range []int{1, 3} {
		out := &caseReceipt{Nodes: nodes, Phase: "startup"}
		report.Cases = append(report.Cases, out)
		out.Passed = t.Run(fmt.Sprintf("%d-node-cluster", nodes), func(t *testing.T) {
			runSelfChat(t, out)
		})
	}
}

func runSelfChat(t *testing.T, out *caseReceipt) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for nodeID := 1; nodeID <= out.Nodes; nodeID++ {
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(nodeID), map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
			"WK_CLUSTER_CHANNEL_REPLICA_N": strconv.Itoa(out.Nodes),
		}))
	}
	cluster := suite.New(t).StartStaticCluster(out.Nodes, opts...)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("self-chat phase=%s\n%s", out.Phase, cluster.DumpDiagnostics())
		}
	})
	f, err := os.Open(cluster.MustNode(1).Process.BinaryPath)
	require.NoError(t, err)
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	require.NoError(t, f.Close())
	require.NoError(t, err)
	out.BinarySHA256 = hex.EncodeToString(hash.Sum(nil))
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)

	// This negative control proves the default fixture did not bypass Token auth.
	invalid, err := wkclient.New(wkclient.Config{Addr: cluster.MustNode(1).GatewayAddr(), OperationTimeout: 5 * time.Second})
	require.NoError(t, err)
	_, err = invalid.Connect(ctx, wkclient.ConnectOptions{UID: selfUID, DeviceID: "invalid", DeviceFlag: frame.APP, Token: "unregistered"})
	require.NoError(t, invalid.Close())
	require.ErrorContains(t, err, frame.ReasonAuthFail.String())
	out.InvalidAuthRefused = true
	for _, flag := range []frame.DeviceFlag{frame.APP, frame.WEB} {
		_, err = suite.PostJSON(ctx, "http://"+cluster.MustNode(1).APIAddr()+"/user/token", map[string]any{
			"uid": selfUID, "token": tokenFor(flag), "device_flag": int(flag), "device_level": 0,
		}, nil)
		require.NoError(t, err)
	}

	for generation := 0; generation < 2; generation++ {
		out.Phase = fmt.Sprintf("generation-%d", generation)
		app := connectDevice(t, ctx, cluster.MustNode(1), frame.APP, "app")
		web := connectDevice(t, ctx, cluster.MustNode(uint64(out.Nodes)), frame.WEB, "web")
		for i, pair := range [][2]*device{{app, web}, {web, app}} {
			key := fmt.Sprintf("self-%d-wkproto-%d", generation, i)
			ack, err := pair[0].client.Send(ctx, wkclient.Message{ClientSeq: uint64(i + 1), ClientMsgNo: key,
				ChannelID: selfUID, ChannelType: frame.ChannelTypePerson, Payload: []byte(key)})
			require.NoError(t, err)
			require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
			require.Equal(t, key, ack.ClientMsgNo)
			require.Equal(t, uint64(i+1), ack.ClientSeq)
			fact := acknowledge(t, out, ack.MessageID, ack.MessageSeq, key)
			pair[1].allowed[fact.ID] = fact
			awaitReceive(t, ctx, out, pair[1], fact.ID)
		}
		key := fmt.Sprintf("self-%d-http", generation)
		ack, err := suite.PostMessageSendEventually(ctx, cluster.MustNode(uint64(out.Nodes)).APIAddr(), map[string]any{
			"from_uid": selfUID, "channel_id": selfUID, "channel_type": frame.ChannelTypePerson,
			"client_msg_no": key, "payload": []byte(key), "header": map[string]int{"no_persist": 0, "sync_once": 0, "red_dot": 0},
		})
		require.NoError(t, err)
		require.Equal(t, uint8(frame.ReasonSuccess), ack.Reason)
		fact := acknowledge(t, out, ack.MessageID, ack.MessageSeq, key)
		for _, d := range []*device{app, web} {
			d.allowed[fact.ID] = fact
			awaitReceive(t, ctx, out, d, fact.ID)
			// A quiet bounded window checks sender suppression while permitting exact
			// RECV redelivery, which is independent from durable-history uniqueness.
			quietCtx, quietCancel := context.WithTimeout(ctx, time.Second)
			for {
				packet, err := d.client.Recv(quietCtx)
				if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
					break
				}
				require.NoError(t, err)
				verifyReceive(t, ctx, out, d, packet)
			}
			quietCancel()
			out.SuppressionWindows++
		}
		// Membership is an asynchronous directory projection. Wait on that public
		// projection explicitly; never poll a successful but incomplete history.
		suite.RequireConversationEventuallyWithin(t, *cluster.MustNode(1), selfUID, selfUID, 15*time.Second, func(item suite.ConversationListItem) error {
			if item.ChannelType != int64(frame.ChannelTypePerson) || item.LastMessage == nil || item.LastMessage.MessageSeq != fact.Seq {
				return fmt.Errorf("self conversation has not projected acknowledged tail %d", fact.Seq)
			}
			return nil
		})
		checkAllHistory(t, ctx, cluster, out)
		require.NoError(t, app.close())
		require.NoError(t, web.close())
		if generation == 0 {
			out.Phase = "whole-cluster-restart-before-any-send"
			for i := range cluster.Nodes {
				require.NoError(t, cluster.Nodes[i].Stop())
			}
			for i := range cluster.Nodes {
				require.NoError(t, cluster.StartStoppedNode(cluster.Nodes[i].Spec.ID))
			}
			out.Restarts++
			require.NoError(t, cluster.WaitHTTPReady(ctx))
			checkAllHistory(t, ctx, cluster, out)
			require.NoError(t, cluster.WaitClusterReady(ctx))
		}
	}
	out.Phase = "complete"
}

func tokenFor(flag frame.DeviceFlag) string { return fmt.Sprintf("self-chat-fixture-token-%d", flag) }

func connectDevice(t *testing.T, ctx context.Context, node *suite.StartedNode, flag frame.DeviceFlag, name string) *device {
	t.Helper()
	c, err := wkclient.New(wkclient.Config{Addr: node.GatewayAddr(), OperationTimeout: 5 * time.Second, AckTimeout: 5 * time.Second})
	require.NoError(t, err)
	d := &device{name: name, client: c, allowed: make(map[string]messageFact), seen: make(map[string]bool)}
	t.Cleanup(func() { require.NoError(t, d.close()) })
	ack, err := c.Connect(ctx, wkclient.ConnectOptions{UID: selfUID, DeviceID: "self-chat-" + name, DeviceFlag: flag, Token: tokenFor(flag)})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	return d
}

// close joins each terminal client once, whether a phase or test cleanup owns it.
func (d *device) close() error {
	if d.closed {
		return nil
	}
	d.closed = true
	return d.client.Close()
}

func acknowledge(t *testing.T, out *caseReceipt, id int64, seq uint64, key string) messageFact {
	t.Helper()
	require.Positive(t, id)
	require.Positive(t, seq)
	if len(out.Acknowledged) > 0 {
		require.Greater(t, seq, out.Acknowledged[len(out.Acknowledged)-1].Seq)
	}
	for _, old := range out.Acknowledged {
		require.NotEqual(t, old.ID, strconv.FormatInt(id, 10))
	}
	fact := messageFact{ID: strconv.FormatInt(id, 10), Seq: seq, ClientMsgNo: key, FromUID: selfUID,
		ChannelID: selfUID, ChannelType: frame.ChannelTypePerson, Payload: []byte(key)}
	out.Acknowledged = append(out.Acknowledged, fact)
	return fact
}

func awaitReceive(t *testing.T, ctx context.Context, out *caseReceipt, d *device, id string) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for !d.seen[id] {
		packet, err := d.client.Recv(readCtx)
		require.NoError(t, err, "device=%s message=%s", d.name, id)
		verifyReceive(t, ctx, out, d, packet)
	}
}

func verifyReceive(t *testing.T, ctx context.Context, out *caseReceipt, d *device, packet *frame.RecvPacket) {
	t.Helper()
	id := strconv.FormatInt(packet.MessageID, 10)
	want, ok := d.allowed[id]
	require.True(t, ok, "unexpected RECV or exact-sender echo: device=%s id=%s", d.name, id)
	require.Equal(t, want.Seq, packet.MessageSeq)
	require.Equal(t, want.ClientMsgNo, packet.ClientMsgNo)
	require.Equal(t, want.FromUID, packet.FromUID)
	require.Equal(t, want.ChannelID, packet.ChannelID)
	require.Equal(t, want.ChannelType, packet.ChannelType)
	require.Equal(t, want.Payload, packet.Payload)
	require.False(t, packet.NoPersist)
	require.False(t, packet.SyncOnce)
	require.False(t, packet.RedDot)
	require.Less(t, len(out.Receives), 64, "bounded receive observation exceeded")
	out.Receives = append(out.Receives, receiveReceipt{Phase: out.Phase, Device: d.name, MessageID: id, Seq: packet.MessageSeq, Duplicate: d.seen[id]})
	d.seen[id] = true
	require.NoError(t, d.client.RecvAck(ctx, packet.MessageID, packet.MessageSeq))
}

func checkAllHistory(t *testing.T, ctx context.Context, cluster *suite.StartedCluster, out *caseReceipt) {
	t.Helper()
	for i := range cluster.Nodes {
		node := &cluster.Nodes[i]
		out.History = append(out.History, historyReceipt{Phase: out.Phase, NodeID: node.Spec.ID})
		observation := &out.History[len(out.History)-1]
		readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		// An explicit inclusive sequence starts at the beginning. Both bounds zero
		// select the latest page even with pull_mode=1, not the oldest page.
		cursor := uint64(1)
		for pageNo := 0; pageNo < 8; pageNo++ {
			page := historyPage{StartSeq: cursor}
			for {
				_, err := suite.PostJSON(readCtx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{
					"login_uid": selfUID, "channel_id": selfUID, "channel_type": frame.ChannelTypePerson,
					"start_message_seq": cursor, "end_message_seq": 0, "pull_mode": 1, "limit": 2,
				}, &page)
				if err == nil {
					break
				}
				require.True(t, historyUnavailable(err), "history node=%d phase=%s: %v", node.Spec.ID, out.Phase, err)
				observation.TemporaryRetries++
				select {
				case <-readCtx.Done():
					require.NoError(t, readCtx.Err(), "history recovery did not finish")
				case <-time.After(100 * time.Millisecond):
				}
			}
			observation.Pages++
			observation.LastPage = &page
			require.Contains(t, []int{0, 1}, page.More)
			require.NotEmpty(t, page.Messages, "successful empty history cannot hide an acknowledged message")
			require.LessOrEqual(t, len(page.Messages), 2)
			for _, row := range page.Messages {
				position := len(observation.Messages)
				require.Less(t, position, len(out.Acknowledged), "extra or duplicate durable message")
				require.Equal(t, out.Acknowledged[position], row, "history node=%d row=%d", node.Spec.ID, position)
				observation.Messages = append(observation.Messages, row)
			}
			if page.More == 0 {
				observation.Complete = true
				break
			}
			next := page.Messages[len(page.Messages)-1].Seq + 1
			require.Greater(t, next, cursor)
			cursor = next
		}
		cancel()
		require.True(t, observation.Complete, "history pagination did not terminate")
		require.Equal(t, out.Acknowledged, observation.Messages, "complete history must retain every acknowledged message")
	}
}

func historyUnavailable(err error) bool {
	var statusErr *suite.HTTPStatusError
	var body struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusServiceUnavailable &&
		json.Unmarshal([]byte(statusErr.Body), &body) == nil && body.Code == "unavailable" && body.Status == http.StatusServiceUnavailable
}
