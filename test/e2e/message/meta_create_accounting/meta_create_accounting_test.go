//go:build e2e

package meta_create_accounting

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

const metaCreateMetric = "wukongim_channelv2_meta_created_total"

type counters struct {
	Created  [12]uint64 `json:"created"`
	Existing [12]uint64 `json:"already_existing"`
	Errors   [12]uint64 `json:"errors"`
}

type caseReceipt struct {
	Name         string    `json:"case"`
	Slot         uint32    `json:"slot"`
	Ingress      [2]uint64 `json:"ingress"`
	SlotLeader   uint64    `json:"slot_leader"`
	Acknowledged int       `json:"acknowledged"`
	Before       counters  `json:"before"`
	AfterFirst   counters  `json:"after_first"`
	AfterLater   counters  `json:"after_later"`
	AfterRestart bool      `json:"row_after_restart"`
}

func TestFirstPersonSendsCreateOneDurableRuntimeRow(t *testing.T) {
	path := os.Getenv("WK_E2E_META_CREATE_REPORT")
	if path == "" {
		path = filepath.Join(t.TempDir(), "meta-create.json")
	}
	var cluster *suite.StartedCluster
	report := struct {
		HashSlots int            `json:"hash_slots"`
		Joined    bool           `json:"processes_joined"`
		Passed    bool           `json:"passed"`
		Cases     [2]caseReceipt `json:"cases"`
	}{HashSlots: 256}
	t.Cleanup(func() {
		report.Joined = cluster != nil
		if cluster != nil {
			for _, node := range cluster.Nodes {
				report.Joined = report.Joined && node.Process == nil
			}
		}
		report.Passed = report.Joined && !t.Failed()
		receipt, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		require.NoError(t, err)
		err = json.NewEncoder(receipt).Encode(report)
		closeErr := receipt.Close()
		require.NoError(t, err)
		require.NoError(t, closeErr)
		data, err := json.Marshal(report)
		require.NoError(t, err)
		t.Logf("channel_meta_create %s", data)
	})

	overrides := map[string]string{
		"WK_CLUSTER_INITIAL_SLOT_COUNT": "12",
		"WK_CLUSTER_HASH_SLOT_COUNT":    "256",
		"WK_CLUSTER_SLOT_REPLICA_N":     "3",
		"WK_CLUSTER_CHANNEL_REPLICA_N":  "3",
		"WK_METRICS_ENABLE":             "true",
	}
	cluster = suite.New(t).StartThreeNodeCluster(suite.WithManagerHTTP(),
		suite.WithNodeConfigOverrides(1, overrides), suite.WithNodeConfigOverrides(2, overrides),
		suite.WithNodeConfigOverrides(3, overrides))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	slots := cluster.ManagerClient(t, 1).MustSlots(t)
	require.Len(t, slots, 12)

	var channels [2]string
	for index, name := range []string{"isolated_first_send", "concurrent_first_sends"} {
		passed := t.Run(name, func(t *testing.T) {
			result := &report.Cases[index]
			result.Name = name
			left, right := "metadata-left-"+name, "metadata-right-"+name
			channels[index] = channelid.EncodePersonChannel(left, right)
			hash := routing.HashSlotForKey(channels[index], 256)
			for _, slot := range slots {
				if slot.HashSlots != nil && slices.Contains(slot.HashSlots.Items, hash) {
					result.Slot, result.SlotLeader = slot.SlotID, slot.Runtime.LeaderID
					break
				}
			}
			require.Positive(t, result.Slot)
			require.Contains(t, []uint64{1, 2, 3}, result.SlotLeader)
			ingress := make([]uint64, 0, 2)
			for _, node := range cluster.Nodes {
				if node.Spec.ID != result.SlotLeader {
					ingress = append(ingress, node.Spec.ID)
				}
			}
			require.Len(t, ingress, 2)
			copy(result.Ingress[:], ingress)
			leftClient := connect(t, ctx, cluster.MustNode(ingress[0]), left)
			rightClient := connect(t, ctx, cluster.MustNode(ingress[1]), right)

			result.Before = readCounters(t, ctx, cluster)
			requireSlotLeader(t, cluster, result.Slot, result.SlotLeader)
			first, stopFirst := context.WithTimeout(ctx, 5*time.Second)
			if index == 0 {
				requireAck(t, leftClient, first, right, 1, "first")
				result.Acknowledged++
			} else {
				start := make(chan struct{})
				type submission struct {
					key string
					ack wkclient.SendResult
					err error
				}
				acks := make(chan submission, 2)
				for _, send := range []struct {
					client    *wkclient.Client
					peer, key string
				}{{leftClient, right, "left-first"}, {rightClient, left, "right-first"}} {
					go func() {
						<-start
						ack, err := send.client.Send(first, message(send.peer, 1, send.key))
						acks <- submission{key: send.key, ack: ack, err: err}
					}()
				}
				close(start)
				a, b := <-acks, <-acks
				for _, submission := range []submission{a, b} {
					require.NoError(t, submission.err)
					assertAck(t, submission.ack, 1, submission.key)
					result.Acknowledged++
				}
				require.NotEqual(t, a.ack.MessageSeq, b.ack.MessageSeq)
			}
			stopFirst()
			requireSlotLeader(t, cluster, result.Slot, result.SlotLeader)
			row, err := suite.GetChannelRuntimeMeta(ctx, cluster.MustNode(ingress[0]), channels[index], frame.ChannelTypePerson)
			require.NoError(t, err)
			require.Equal(t, result.Slot, row.SlotID)
			require.Len(t, row.Replicas, 3)
			result.AfterFirst = readCounters(t, ctx, cluster)
			want := result.Before.Created
			want[result.Slot-1]++
			require.Equal(t, want, result.AfterFirst.Created)
			require.Equal(t, result.Before.Errors, result.AfterFirst.Errors)

			later, stopLater := context.WithTimeout(ctx, 2*time.Second)
			requireAck(t, leftClient, later, right, 2, "later")
			stopLater()
			result.Acknowledged++
			result.AfterLater = readCounters(t, ctx, cluster)
			require.Equal(t, want, result.AfterLater.Created)
			require.Equal(t, result.Before.Errors, result.AfterLater.Errors)
		})
		require.True(t, passed)
	}

	for i := range cluster.Nodes {
		require.NoError(t, cluster.MustNode(uint64(i+1)).Stop())
	}
	for i := range cluster.Nodes {
		require.NoError(t, cluster.StartStoppedNode(uint64(i+1)))
	}
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	for index, channel := range channels {
		row, err := suite.GetChannelRuntimeMeta(ctx, cluster.MustNode(1), channel, frame.ChannelTypePerson)
		require.NoError(t, err)
		require.Equal(t, report.Cases[index].Slot, row.SlotID)
		require.Len(t, row.Replicas, 3)
		report.Cases[index].AfterRestart = true
	}
}

func requireSlotLeader(t *testing.T, cluster *suite.StartedCluster, slotID uint32, leader uint64) {
	t.Helper()
	for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
		if slot.SlotID == slotID {
			require.Equal(t, leader, slot.Runtime.LeaderID, "Slot leader across first SENDs")
			return
		}
	}
	t.Fatal("Slot absent from Manager inventory")
}

func connect(t *testing.T, ctx context.Context, node *suite.StartedNode, uid string) *wkclient.Client {
	t.Helper()
	token := uid + "-token"
	require.NoError(t, suite.RegisterUserToken(ctx, node.APIAddr(), uid, token))
	client, err := wkclient.New(wkclient.Config{Addr: node.GatewayAddr(), AckTimeout: 5 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ack, err := client.Connect(ctx, wkclient.ConnectOptions{UID: uid, Token: token, DeviceID: "metadata-check", DeviceFlag: frame.APP})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	return client
}

func message(peer string, sequence uint64, key string) wkclient.Message {
	return wkclient.Message{ChannelType: frame.ChannelTypePerson, ChannelID: peer, ClientSeq: sequence, ClientMsgNo: key, Payload: []byte("metadata")}
}

func requireAck(t *testing.T, client *wkclient.Client, ctx context.Context, peer string, sequence uint64, key string) {
	t.Helper()
	ack, err := client.Send(ctx, message(peer, sequence, key))
	require.NoError(t, err)
	assertAck(t, ack, sequence, key)
}

func assertAck(t *testing.T, ack wkclient.SendResult, sequence uint64, key string) {
	t.Helper()
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	require.Equal(t, sequence, ack.ClientSeq)
	require.Equal(t, key, ack.ClientMsgNo)
	require.Positive(t, ack.MessageSeq)
}

func readCounters(t *testing.T, ctx context.Context, cluster *suite.StartedCluster) counters {
	t.Helper()
	var total counters
	for _, node := range cluster.Nodes {
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		require.NoError(t, err)
		seen := make(map[string]bool, 36)
		for _, sample := range samples {
			if sample.Name != metaCreateMetric {
				continue
			}
			slot, err := strconv.Atoi(sample.Labels["slot_id"])
			require.NoError(t, err)
			require.GreaterOrEqual(t, slot, 1)
			require.LessOrEqual(t, slot, 12)
			require.GreaterOrEqual(t, sample.Value, float64(0))
			require.LessOrEqual(t, sample.Value, float64(uint64(1)<<53))
			value := uint64(sample.Value)
			require.Equal(t, sample.Value, float64(value))
			result := sample.Labels["result"]
			key := fmt.Sprintf("%d/%s", slot, result)
			require.False(t, seen[key], "duplicate metric series")
			seen[key] = true
			switch result {
			case "created":
				total.Created[slot-1] += value
			case "already_existing":
				total.Existing[slot-1] += value
			case "error":
				total.Errors[slot-1] += value
			default:
				t.Fatal("unknown metadata metric result")
			}
		}
		require.Len(t, seen, 36)
	}
	return total
}
