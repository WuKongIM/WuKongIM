//go:build integration

package replication_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	nodeaccess "github.com/WuKongIM/WuKongIM/internal/access/node"
	channelappend "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

// BenchmarkThreeNodeChannelAppendAuthorityRPCWithGatewayDeliveryPressure1000QPS
// adds the production SEND-authority RPC before the real TCP quorum append and
// keeps the formal payload and delivery pressure around that path.
func BenchmarkThreeNodeChannelAppendAuthorityRPCWithGatewayDeliveryPressure1000QPS(b *testing.B) {
	benchmarkThreeNodeChannelAppendAuthorityRPCWithGatewayDeliveryPressure1000QPS(b, 16, false)
}

// BenchmarkThreeNodeSharedTransportAuthorityRPCWithGatewayDeliveryPressure1000QPS
// reproduces the product topology where SEND-authority and quorum-exchange RPCs
// share one outbound TransportClient and its per-peer connection pool.
func BenchmarkThreeNodeSharedTransportAuthorityRPCWithGatewayDeliveryPressure1000QPS(b *testing.B) {
	benchmarkThreeNodeChannelAppendAuthorityRPCWithGatewayDeliveryPressure1000QPS(b, 16, true)
}

// BenchmarkThreeNodeChannelAppendAuthorityRPCPoolMatrix1000QPS isolates the
// process transport connection count on the complete authority, quorum, and
// delivery-pressure path.
func BenchmarkThreeNodeChannelAppendAuthorityRPCPoolMatrix1000QPS(b *testing.B) {
	for _, poolSize := range []int{4, 16, 64, 256, 1000} {
		b.Run(fmt.Sprintf("pool_%d", poolSize), func(b *testing.B) {
			benchmarkThreeNodeChannelAppendAuthorityRPCWithGatewayDeliveryPressure1000QPS(b, poolSize, false)
		})
	}
}

func benchmarkThreeNodeChannelAppendAuthorityRPCWithGatewayDeliveryPressure1000QPS(b *testing.B, poolSize int, sharedTransport bool) {
	cluster := newChannelAppendBenchmarkClusterWithOptions(b, threeNodeBenchmarkChannels, durableQuorumBenchmarkOptions{
		useNetwork: true, commitShards: 1, payload: chatLifecycleBenchmarkPayload, transportPoolSize: poolSize,
	})
	defer cluster.close(b)
	rpc := newChannelAppendAuthorityRPCBenchmark(b, cluster, poolSize, sharedTransport)
	defer rpc.close()
	pressure := newChannelAppendGatewayDeliveryPressure(b, channelAppendDeliveryPressureSessions, channelAppendDeliverySynchronousAck)
	defer func() {
		if err := pressure.Stop(); err != nil {
			b.Errorf("delivery pressure Stop(): %v", err)
		}
	}()

	latencies := make([]time.Duration, b.N)
	jobs := make(chan int, threeNodeBenchmarkWorkers)
	var workers sync.WaitGroup
	workers.Add(threeNodeBenchmarkWorkers)
	for range threeNodeBenchmarkWorkers {
		go func() {
			defer workers.Done()
			for index := range jobs {
				channelIndex := index % len(cluster.channels)
				channel := cluster.channels[channelIndex]
				ingress := ch.NodeID(index%3 + 1)
				item := channelappend.SendBatchItem{Context: context.Background(), Command: channelappend.SendCommand{
					MessageID: uint64(index + 1), ClientSeq: uint64(index + 1),
					ClientMsgNo: fmt.Sprintf("authority-rpc-%d", index+1), FromUID: "benchmark-sender",
					ChannelKey: string(channel.authority.Key), ChannelID: channel.authority.ChannelID.ID,
					ChannelType: channel.authority.ChannelID.Type, Payload: cluster.payload(index),
				}}
				target := channelappend.AuthorityTarget{
					ChannelID:  channelappend.ChannelID{ID: channel.authority.ChannelID.ID, Type: channel.authority.ChannelID.Type},
					ChannelKey: string(channel.authority.Key), LeaderNodeID: uint64(channel.authority.Leader),
					Epoch: channel.authority.ID.ChannelEpoch, LeaderEpoch: channel.authority.ID.LeaderTerm,
					RouteGeneration: channel.authority.ID.FenceVersion,
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				started := time.Now()
				result := rpc.submit(ctx, ingress, target, item)
				latencies[index] = time.Since(started)
				cancel()
				if result.Err != nil {
					cluster.recordError(fmt.Errorf("authority RPC append %d: %w", index, result.Err))
				}
			}
		}()
	}

	b.ReportAllocs()
	b.ResetTimer()
	pressure.Start()
	started := time.Now()
	for index := range b.N {
		waitUntil(started.Add(time.Duration(index) * time.Second / threeNodeBenchmarkRate))
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	b.StopTimer()
	if err := pressure.Stop(); err != nil {
		b.Errorf("delivery pressure Stop(): %v", err)
	}
	pressure = nil
	if err := cluster.firstError(); err != nil {
		b.Fatal(err)
	}
	b.Logf("channel stages: %s", cluster.channelObserver.summary())
	cluster.transportObserver.report(b)
	cluster.exchangeObserver.report(b)
	reportChannelAppendAuthorityRPCWindows(b, latencies)
	reportDurableQuorumBenchmark(b, "authority-rpc", latencies, cluster.commitObserver.snapshot())
}

func reportChannelAppendAuthorityRPCWindows(b *testing.B, latencies []time.Duration) {
	b.Helper()
	const windowItems = 60_000
	for start := 0; start < len(latencies); start += windowItems {
		end := min(start+windowItems, len(latencies))
		reportAuthorityRPCLatencySlice(b, fmt.Sprintf("minute-%d", start/windowItems+1), latencies[start:end])
	}
	classes := [][]time.Duration{nil, nil, nil, nil}
	for index, latency := range latencies {
		class := 0
		switch index % 100 {
		case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89, 90, 91, 92, 93, 94:
			class = 1
		case 95, 96, 97, 98:
			class = 2
		case 99:
			class = 3
		}
		classes[class] = append(classes[class], latency)
	}
	for index, name := range []string{"payload-256b", "payload-1k", "payload-4k", "payload-16k"} {
		reportAuthorityRPCLatencySlice(b, name, classes[index])
	}
}

func reportAuthorityRPCLatencySlice(b *testing.B, name string, latencies []time.Duration) {
	b.Helper()
	if len(latencies) == 0 {
		return
	}
	sorted := append([]time.Duration(nil), latencies...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p99 := sorted[(len(sorted)*99-1)/100]
	above := 0
	for _, latency := range latencies {
		if latency > 200*time.Millisecond {
			above++
		}
	}
	b.ReportMetric(float64(p99)/float64(time.Millisecond), name+"-p99-ms")
	b.ReportMetric(100*float64(above)/float64(len(latencies)), name+"-over-200ms-pct")
}

type channelAppendAuthorityRPCBenchmark struct {
	cluster *durableQuorumBenchmarkCluster
	servers map[ch.NodeID]*clusternet.TransportServer
	clients map[ch.NodeID]*nodeaccess.Client
	raw     map[ch.NodeID]*clusternet.TransportClient
}

func newChannelAppendAuthorityRPCBenchmark(b *testing.B, cluster *durableQuorumBenchmarkCluster, poolSize int, sharedTransport bool) *channelAppendAuthorityRPCBenchmark {
	b.Helper()
	rpc := &channelAppendAuthorityRPCBenchmark{
		cluster: cluster, servers: make(map[ch.NodeID]*clusternet.TransportServer, 3),
		clients: make(map[ch.NodeID]*nodeaccess.Client, 3), raw: make(map[ch.NodeID]*clusternet.TransportClient, 3),
	}
	if sharedTransport {
		for _, nodeID := range []ch.NodeID{1, 2, 3} {
			server := cluster.transportServers[nodeID]
			client := cluster.transportClients[nodeID]
			if server == nil || client == nil {
				b.Fatalf("shared authority RPC transport missing for node %d", nodeID)
			}
			adapter := nodeaccess.NewChannelAppendAdapter(nodeaccess.ChannelAppendOptions{
				ChannelAppend: channelAppendAuthorityRPCSubmitter{cluster: cluster, nodeID: nodeID},
			})
			server.Register(nodeaccess.ChannelAppendRPCServiceID, clusternet.HandlerFunc(adapter.HandleChannelAppendRPC))
			rpc.clients[nodeID] = nodeaccess.NewClient(channelAppendAuthorityRPCNode{client: client})
		}
		return rpc
	}
	discovery := clusternet.NewDiscovery()
	addresses := make([]clusternet.NodeAddress, 0, 3)
	for _, nodeID := range []ch.NodeID{1, 2, 3} {
		server := clusternet.NewTransportServer(clusternet.TransportServerConfig{NodeID: uint64(nodeID)})
		adapter := nodeaccess.NewChannelAppendAdapter(nodeaccess.ChannelAppendOptions{
			ChannelAppend: channelAppendAuthorityRPCSubmitter{cluster: cluster, nodeID: nodeID},
		})
		server.Register(nodeaccess.ChannelAppendRPCServiceID, clusternet.HandlerFunc(adapter.HandleChannelAppendRPC))
		if err := server.Start("127.0.0.1:0"); err != nil {
			rpc.close()
			b.Fatalf("authority RPC server %d Start(): %v", nodeID, err)
		}
		rpc.servers[nodeID] = server
		addresses = append(addresses, clusternet.NodeAddress{NodeID: uint64(nodeID), Addr: server.Addr()})
	}
	discovery.Update(addresses)
	for _, nodeID := range []ch.NodeID{1, 2, 3} {
		client := clusternet.NewTransportClient(clusternet.TransportClientConfig{Discovery: discovery, NodeID: uint64(nodeID), PoolSize: poolSize})
		rpc.raw[nodeID] = client
		rpc.clients[nodeID] = nodeaccess.NewClient(channelAppendAuthorityRPCNode{client: client})
	}
	return rpc
}

func (r *channelAppendAuthorityRPCBenchmark) submit(ctx context.Context, ingress ch.NodeID, target channelappend.AuthorityTarget, item channelappend.SendBatchItem) channelappend.SendBatchItemResult {
	if ingress == ch.NodeID(target.LeaderNodeID) {
		return channelAppendAuthorityRPCSubmitter{cluster: r.cluster, nodeID: ingress}.SubmitForAuthority(ctx, target, []channelappend.SendBatchItem{item})[0]
	}
	results := r.clients[ingress].ForwardSendBatch(ctx, target, []channelappend.SendBatchItem{item})
	if len(results) != 1 {
		return channelappend.SendBatchItemResult{Err: channelappend.ErrAppendResultMissing}
	}
	return results[0]
}

func (r *channelAppendAuthorityRPCBenchmark) close() {
	if r == nil {
		return
	}
	for _, client := range r.raw {
		client.Stop()
	}
	for _, server := range r.servers {
		server.Stop()
	}
}

type channelAppendAuthorityRPCNode struct{ client *clusternet.TransportClient }

func (n channelAppendAuthorityRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	return n.client.Call(ctx, nodeID, serviceID, payload)
}

type channelAppendAuthorityRPCSubmitter struct {
	cluster *durableQuorumBenchmarkCluster
	nodeID  ch.NodeID
}

func (s channelAppendAuthorityRPCSubmitter) SubmitForAuthority(ctx context.Context, target channelappend.AuthorityTarget, items []channelappend.SendBatchItem) []channelappend.SendBatchItemResult {
	results := make([]channelappend.SendBatchItemResult, len(items))
	if s.cluster == nil || s.nodeID != ch.NodeID(target.LeaderNodeID) {
		for index := range results {
			results[index].Err = channelappend.ErrNotChannelAuthority
		}
		return results
	}
	messages := make([]ch.Message, len(items))
	for index, item := range items {
		command := item.Command
		messages[index] = ch.Message{
			MessageID: command.MessageID, ChannelID: command.ChannelID, ChannelType: command.ChannelType,
			FromUID: command.FromUID, ClientMsgNo: command.ClientMsgNo, Payload: command.Payload,
			ServerTimestampMS: time.Now().UnixMilli(),
		}
	}
	appended, err := s.cluster.nodes[s.nodeID].AppendBatch(ctx, ch.AppendBatchRequest{
		ChannelID: ch.ChannelID{ID: target.ChannelID.ID, Type: target.ChannelID.Type}, Messages: messages,
		CommitMode: ch.CommitModeQuorum, ExpectedChannelEpoch: target.Epoch, ExpectedLeaderEpoch: target.LeaderEpoch,
		OmitResultPayload: true, ServerAllocatedMessageIDs: true,
	})
	if err != nil {
		for index := range results {
			results[index].Err = err
		}
		return results
	}
	if len(appended.Items) != len(results) {
		for index := range results {
			results[index].Err = channelappend.ErrAppendResultMissing
		}
		return results
	}
	for index, item := range appended.Items {
		results[index].Err = item.Err
		results[index].Result = channelappend.SendResult{
			MessageID: item.MessageID, MessageSeq: item.MessageSeq, Reason: channelappend.ReasonSuccess,
		}
	}
	return results
}
