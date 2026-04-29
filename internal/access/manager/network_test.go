package manager

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/stretchr/testify/require"
)

func TestManagerNetworkSummaryRejectsInsufficientPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/network/summary", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.JSONEq(t, `{"error":"forbidden","message":"forbidden"}`, rec.Body.String())
}

func TestManagerNetworkSummaryReturnsLocalNodeSnapshot(t *testing.T) {
	generatedAt := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
	lastHeartbeatAt := time.Date(2026, 4, 29, 11, 59, 0, 0, time.UTC)
	lastSeenAt := time.Date(2026, 4, 29, 11, 59, 30, 0, time.UTC)
	eventAt := time.Date(2026, 4, 29, 11, 59, 45, 0, time.UTC)
	successRate := 0.75
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.network",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{networkSummary: managementusecase.NetworkSummary{
			GeneratedAt: generatedAt,
			Scope: managementusecase.NetworkScope{
				View:               "local_node",
				LocalNodeID:        1,
				ControllerLeaderID: 1,
			},
			SourceStatus: managementusecase.NetworkSourceStatus{
				LocalCollector:    "ok",
				ControllerContext: "ok",
				RuntimeViews:      "unavailable",
			},
			Headline: managementusecase.NetworkHeadline{
				RemotePeers:       1,
				AliveNodes:        2,
				SuspectNodes:      1,
				PoolActive:        5,
				PoolIdle:          7,
				RPCInflight:       3,
				DialErrors1m:      1,
				QueueFull1m:       2,
				Timeouts1m:        4,
				StaleObservations: 1,
			},
			Traffic: managementusecase.NetworkTraffic{
				Scope:                  "local_total_by_msg_type",
				TXBytes1m:              1024,
				RXBytes1m:              2048,
				TXBps:                  128.5,
				RXBps:                  256.25,
				PeerBreakdownAvailable: false,
				ByMessageType: []managementusecase.NetworkTrafficMessageType{{
					Direction:   "tx",
					MessageType: "rpc",
					Bytes1m:     512,
					Bps:         64.5,
				}},
			},
			Peers: []managementusecase.NetworkPeer{{
				NodeID:          2,
				Name:            "node-2",
				Addr:            "127.0.0.1:7002",
				Health:          "alive",
				LastHeartbeatAt: lastHeartbeatAt,
				Pools: managementusecase.NetworkPeerPools{
					Cluster:   managementusecase.NetworkPoolStats{Active: 2, Idle: 3},
					DataPlane: managementusecase.NetworkPoolStats{Active: 1, Idle: 4},
				},
				RPC: managementusecase.NetworkPeerRPC{
					Inflight:    3,
					Calls1m:     8,
					P95Ms:       12.5,
					SuccessRate: &successRate,
				},
				Errors: managementusecase.NetworkPeerErrors{
					DialError1m:   1,
					QueueFull1m:   2,
					Timeout1m:     4,
					RemoteError1m: 1,
				},
			}},
			Services: []managementusecase.NetworkRPCService{{
				ServiceID:         35,
				Service:           "channel_long_poll_fetch",
				Group:             "channel_data_plane",
				TargetNode:        2,
				Inflight:          1,
				Calls1m:           6,
				Success1m:         5,
				ExpectedTimeout1m: 1,
				Timeout1m:         2,
				QueueFull1m:       3,
				RemoteError1m:     4,
				OtherError1m:      5,
				P50Ms:             2.5,
				P95Ms:             9.5,
				P99Ms:             15.5,
				LastSeenAt:        lastSeenAt,
			}},
			ChannelReplication: managementusecase.NetworkChannelReplication{
				Pool: managementusecase.NetworkPoolStats{Active: 1, Idle: 4},
				Services: []managementusecase.NetworkRPCService{{
					ServiceID:  33,
					Service:    "channel_append",
					Group:      "channel_data_plane",
					TargetNode: 2,
					Calls1m:    10,
					Success1m:  9,
					P50Ms:      1.5,
					P95Ms:      7.5,
					P99Ms:      11.5,
					LastSeenAt: lastSeenAt,
				}},
				LongPollConfig: managementusecase.NetworkLongPollConfig{
					LaneCount:   4,
					MaxWait:     1500 * time.Millisecond,
					MaxBytes:    65536,
					MaxChannels: 128,
				},
				LongPollTimeouts1m:  11,
				DataPlaneRPCTimeout: 2500 * time.Millisecond,
			},
			Discovery: managementusecase.NetworkDiscovery{
				ListenAddr:                    "0.0.0.0:7000",
				AdvertiseAddr:                 "10.0.0.1:7000",
				Seeds:                         []string{"10.0.0.2:7000"},
				StaticNodes:                   []managementusecase.NetworkDiscoveryNode{{NodeID: 2, Addr: "10.0.0.2:7000"}},
				PoolSize:                      3,
				DataPlanePoolSize:             5,
				DialTimeout:                   3 * time.Second,
				ControllerObservationInterval: 4 * time.Second,
			},
			Events: []managementusecase.NetworkEvent{{
				At:         eventAt,
				Severity:   "warn",
				Kind:       "rpc_timeout",
				TargetNode: 2,
				Service:    "channel_append",
				Message:    "RPC timeout observed",
			}},
		}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/network/summary", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
		"generated_at": "2026-04-29T12:00:00Z",
		"scope": {
			"view": "local_node",
			"local_node_id": 1,
			"controller_leader_id": 1
		},
		"source_status": {
			"local_collector": "ok",
			"controller_context": "ok",
			"runtime_views": "unavailable",
			"errors": {}
		},
		"headline": {
			"remote_peers": 1,
			"alive_nodes": 2,
			"suspect_nodes": 1,
			"dead_nodes": 0,
			"draining_nodes": 0,
			"pool_active": 5,
			"pool_idle": 7,
			"rpc_inflight": 3,
			"dial_errors_1m": 1,
			"queue_full_1m": 2,
			"timeouts_1m": 4,
			"stale_observations": 1
		},
		"traffic": {
			"scope": "local_total_by_msg_type",
			"tx_bytes_1m": 1024,
			"rx_bytes_1m": 2048,
			"tx_bps": 128.5,
			"rx_bps": 256.25,
			"peer_breakdown_available": false,
			"by_message_type": [{"direction":"tx","message_type":"rpc","bytes_1m":512,"bps":64.5}]
		},
		"peers": [{
			"node_id": 2,
			"name": "node-2",
			"addr": "127.0.0.1:7002",
			"health": "alive",
			"last_heartbeat_at": "2026-04-29T11:59:00Z",
			"pools": {
				"cluster": {"active": 2, "idle": 3},
				"data_plane": {"active": 1, "idle": 4}
			},
			"rpc": {"inflight": 3, "calls_1m": 8, "p95_ms": 12.5, "success_rate": 0.75},
			"errors": {"dial_error_1m": 1, "queue_full_1m": 2, "timeout_1m": 4, "remote_error_1m": 1}
		}],
		"services": [{
			"service_id": 35,
			"service": "channel_long_poll_fetch",
			"group": "channel_data_plane",
			"target_node": 2,
			"inflight": 1,
			"calls_1m": 6,
			"success_1m": 5,
			"expected_timeout_1m": 1,
			"timeout_1m": 2,
			"queue_full_1m": 3,
			"remote_error_1m": 4,
			"other_error_1m": 5,
			"p50_ms": 2.5,
			"p95_ms": 9.5,
			"p99_ms": 15.5,
			"last_seen_at": "2026-04-29T11:59:30Z"
		}],
		"channel_replication": {
			"pool": {"active": 1, "idle": 4},
			"services": [{
				"service_id": 33,
				"service": "channel_append",
				"group": "channel_data_plane",
				"target_node": 2,
				"inflight": 0,
				"calls_1m": 10,
				"success_1m": 9,
				"expected_timeout_1m": 0,
				"timeout_1m": 0,
				"queue_full_1m": 0,
				"remote_error_1m": 0,
				"other_error_1m": 0,
				"p50_ms": 1.5,
				"p95_ms": 7.5,
				"p99_ms": 11.5,
				"last_seen_at": "2026-04-29T11:59:30Z"
			}],
			"long_poll": {"lane_count": 4, "max_wait_ms": 1500, "max_bytes": 65536, "max_channels": 128},
			"long_poll_timeouts_1m": 11,
			"data_plane_rpc_timeout_ms": 2500
		},
		"discovery": {
			"listen_addr": "0.0.0.0:7000",
			"advertise_addr": "10.0.0.1:7000",
			"seeds": ["10.0.0.2:7000"],
			"static_nodes": [{"node_id": 2, "addr": "10.0.0.2:7000"}],
			"pool_size": 3,
			"data_plane_pool_size": 5,
			"dial_timeout_ms": 3000,
			"controller_observation_interval_ms": 4000
		},
		"events": [{
			"at": "2026-04-29T11:59:45Z",
			"severity": "warn",
			"kind": "rpc_timeout",
			"target_node": 2,
			"service": "channel_append",
			"message": "RPC timeout observed"
		}]
	}`, rec.Body.String())
}

func TestManagerNetworkSummaryReturnsInternalError(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.network",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{networkSummaryErr: errors.New("network collector failed")},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/network/summary", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.JSONEq(t, `{"error":"internal_error","message":"network collector failed"}`, rec.Body.String())
}
