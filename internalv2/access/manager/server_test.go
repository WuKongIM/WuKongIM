package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerLoginIssuesJWTForAuthorizedUser(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body loginResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if body.Username != "admin" || body.TokenType != "Bearer" || body.AccessToken == "" {
		t.Fatalf("login body = %+v, want username, bearer token", body)
	}
	if body.ExpiresIn != int64(time.Hour/time.Second) {
		t.Fatalf("expires_in = %d, want %d", body.ExpiresIn, int64(time.Hour/time.Second))
	}
	if time.Until(body.ExpiresAt) <= 0 {
		t.Fatalf("expires_at = %s, want future timestamp", body.ExpiresAt)
	}
	if len(body.Permissions) != 1 || body.Permissions[0].Resource != "cluster.node" || len(body.Permissions[0].Actions) != 1 || body.Permissions[0].Actions[0] != "r" {
		t.Fatalf("permissions = %#v, want copied grants", body.Permissions)
	}

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("Unmarshal raw() error = %v", err)
	}
	if _, ok := raw["token"]; ok {
		t.Fatalf("legacy token field present in response: %s", rec.Body.String())
	}
}

func TestManagerLoginRejectsInvalidCredentials(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
		}}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/login", bytes.NewBufferString(`{"username":"admin","password":"bad"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if !jsonEqual(rec.Body.String(), `{"error":"invalid_credentials","message":"invalid credentials"}`) {
		t.Fatalf("body = %q, want invalid credentials error", rec.Body.String())
	}
}

func TestManagerLoginRouteDisabledWhenAuthOff(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestManagerNodesReturnsReadOnlyInventory(t *testing.T) {
	generatedAt := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			nodes: managementusecase.NodeList{
				GeneratedAt:        generatedAt,
				ControllerLeaderID: 1,
				Items: []managementusecase.Node{{
					NodeID:          1,
					Name:            "node-1",
					Addr:            "127.0.0.1:7011",
					Status:          "alive",
					LastHeartbeatAt: generatedAt,
					IsLocal:         true,
					CapacityWeight:  1,
					Membership: managementusecase.NodeMembership{
						Role:        "data",
						JoinState:   "active",
						Schedulable: true,
					},
					Health: managementusecase.NodeHealth{
						Status:          "alive",
						LastHeartbeatAt: generatedAt,
					},
					Controller: managementusecase.NodeController{
						Role:       "leader",
						Voter:      true,
						LeaderID:   1,
						RaftHealth: "unknown",
					},
					Slots: managementusecase.NodeSlotSummary{
						ReplicaCount: 2,
						LeaderCount:  1,
					},
					Runtime: managementusecase.NodeRuntimeSummary{
						NodeID:  1,
						Unknown: true,
					},
					Actions: managementusecase.NodeActions{},
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"generated_at": "2026-06-16T10:00:00Z",
		"controller_leader_id": 1,
		"total": 1,
		"items": [{
			"node_id": 1,
			"name": "node-1",
			"addr": "127.0.0.1:7011",
			"status": "alive",
			"last_heartbeat_at": "2026-06-16T10:00:00Z",
			"is_local": true,
			"capacity_weight": 1,
			"membership": {
				"role": "data",
				"join_state": "active",
				"schedulable": true
			},
			"health": {
				"status": "alive",
				"last_heartbeat_at": "2026-06-16T10:00:00Z"
			},
			"controller": {
				"role": "leader",
				"voter": true,
				"leader_id": 1,
				"raft_health": "unknown",
				"first_index": 0,
				"applied_index": 0,
				"snapshot_index": 0
			},
			"slot_stats": {
				"count": 2,
				"leader_count": 1
			},
			"slots": {
				"replica_count": 2,
				"leader_count": 1,
				"follower_count": 0,
				"quorum_lost_count": 0,
				"unreported_count": 0
			},
			"runtime": {
				"node_id": 1,
				"active_online": 0,
				"closing_online": 0,
				"total_online": 0,
				"gateway_sessions": 0,
				"sessions_by_listener": {},
				"accepting_new_sessions": false,
				"draining": false,
				"unknown": true
			},
			"actions": {
				"can_drain": false,
				"can_resume": false,
				"can_scale_in": false,
				"can_onboard": false
			}
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerNodesRequiresNodeReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/nodes", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}

func TestManagerSlotsReturnsReadOnlyInventory(t *testing.T) {
	reportedAt := time.Date(2026, 6, 16, 11, 0, 0, 0, time.UTC)
	var gotOpts managementusecase.ListSlotsOptions
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			lastSlotsOptions: &gotOpts,
			slots: []managementusecase.Slot{{
				SlotID: 9,
				HashSlots: &managementusecase.SlotHashSlots{
					Count: 2,
					Items: []uint16{3, 4},
				},
				State: managementusecase.SlotState{
					Quorum:      "ready",
					Sync:        "matched",
					LeaderMatch: true,
				},
				Assignment: managementusecase.SlotAssignment{
					DesiredPeers:    []uint64{1, 2},
					PreferredLeader: 1,
					ConfigEpoch:     7,
				},
				Runtime: managementusecase.SlotRuntime{
					CurrentPeers:        []uint64{1, 2},
					CurrentVoters:       []uint64{1, 2},
					LeaderID:            1,
					HealthyVoters:       2,
					HasQuorum:           true,
					ObservedConfigEpoch: 7,
					LastReportAt:        reportedAt,
				},
			}},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots?node_id=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotOpts.NodeID != 2 {
		t.Fatalf("node filter = %d, want 2", gotOpts.NodeID)
	}
	if !jsonEqual(rec.Body.String(), `{
		"total": 1,
		"items": [{
			"slot_id": 9,
			"hash_slots": {
				"count": 2,
				"items": [3, 4]
			},
			"state": {
				"quorum": "ready",
				"sync": "matched",
				"leader_match": true,
				"leader_transfer_pending": false
			},
			"assignment": {
				"desired_peers": [1, 2],
				"preferred_leader_id": 1,
				"config_epoch": 7,
				"balance_version": 0
			},
			"runtime": {
				"current_peers": [1, 2],
				"current_voters": [1, 2],
				"leader_id": 1,
				"healthy_voters": 2,
				"has_quorum": true,
				"observed_config_epoch": 7,
				"last_report_at": "2026-06-16T11:00:00Z"
			}
		}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestManagerSlotsRequiresSlotReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.node",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/slots", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}

func TestManagerBusinessChannelsReturnsReadOnlyList(t *testing.T) {
	nextCursor := managementusecase.ChannelListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 2, TypeFilter: 2, KeywordHash: 0xabc}
	var gotReq managementusecase.ListBusinessChannelsRequest
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			lastBusinessChannelsRequest: &gotReq,
			businessChannels: managementusecase.ListBusinessChannelsResponse{
				Items: []managementusecase.BusinessChannelListItem{{
					ChannelID:                 "g1",
					ChannelType:               2,
					SlotID:                    9,
					HashSlot:                  7,
					Ban:                       true,
					SendBan:                   true,
					SubscriberMutationVersion: 5,
				}},
				HasMore:    true,
				NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels?node_id=2&type=2&keyword=g&limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotReq.NodeID != 2 || gotReq.TypeFilter != 2 || gotReq.Keyword != "g" || gotReq.Limit != 1 {
		t.Fatalf("request = %#v, want node 2 type 2 keyword g limit 1", gotReq)
	}
	var body struct {
		Items      []BusinessChannelListItemDTO `json:"items"`
		HasMore    bool                         `json:"has_more"`
		NextCursor string                       `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ChannelID != "g1" || body.Items[0].ChannelType != 2 || body.Items[0].SlotID != 9 || body.Items[0].HashSlot != 7 || !body.Items[0].Ban || !body.Items[0].SendBan || body.Items[0].Disband || body.Items[0].SubscriberMutationVersion != 5 {
		t.Fatalf("items = %#v, want business channel row", body.Items)
	}
	if !body.HasMore || body.NextCursor == "" {
		t.Fatalf("pagination = has_more:%t next:%q, want next cursor", body.HasMore, body.NextCursor)
	}
	decoded, err := decodeBusinessChannelCursor(body.NextCursor)
	if err != nil {
		t.Fatalf("decodeBusinessChannelCursor() error = %v", err)
	}
	if decoded != nextCursor {
		t.Fatalf("decoded cursor = %#v, want %#v", decoded, nextCursor)
	}
}

func TestManagerChannelRuntimeMetaReturnsClusterRuntimeList(t *testing.T) {
	nextCursor := managementusecase.ChannelRuntimeMetaListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 2}
	var gotReq managementusecase.ListChannelRuntimeMetaRequest
	maxSeq := uint64(88)
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{
			lastChannelRuntimeMetaRequest: &gotReq,
			channelRuntimeMeta: managementusecase.ListChannelRuntimeMetaResponse{
				Items: []managementusecase.ChannelRuntimeMeta{{
					ChannelID:     "g1",
					ChannelType:   2,
					SlotID:        9,
					ChannelEpoch:  11,
					LeaderEpoch:   5,
					Leader:        2,
					Replicas:      []uint64{1, 2, 3},
					ISR:           []uint64{1, 2},
					MinISR:        2,
					MaxMessageSeq: &maxSeq,
					Status:        "active",
				}},
				HasMore:    true,
				NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channel-runtime-meta?node_id=2&node_scope=leader&include_max_message_seq=true&channel_id=g&limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotReq.NodeID != 2 || gotReq.NodeScope != managementusecase.ChannelRuntimeMetaNodeScopeLeader || gotReq.ChannelIDQuery != "g" || gotReq.Limit != 1 || !gotReq.IncludeMaxMessageSeq {
		t.Fatalf("request = %#v, want node 2 leader scope channel g include max seq limit 1", gotReq)
	}
	var body ChannelRuntimeMetaListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ChannelID != "g1" || body.Items[0].Leader != 2 || body.Items[0].MaxMessageSeq == nil || *body.Items[0].MaxMessageSeq != 88 {
		t.Fatalf("items = %#v, want runtime meta row with max seq", body.Items)
	}
	if !body.HasMore || body.NextCursor == "" {
		t.Fatalf("pagination = has_more:%t next:%q, want next cursor", body.HasMore, body.NextCursor)
	}
	decoded, err := decodeChannelRuntimeMetaCursor(body.NextCursor)
	if err != nil {
		t.Fatalf("decodeChannelRuntimeMetaCursor() error = %v", err)
	}
	if decoded != nextCursor {
		t.Fatalf("decoded cursor = %#v, want %#v", decoded, nextCursor)
	}
}

func TestManagerBusinessChannelsRejectsInvalidNodeID(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels?node_id=bad", nil)

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"bad_request","message":"invalid node_id"}`) {
		t.Fatalf("body = %s, want invalid node_id", rec.Body.String())
	}
}

func TestManagerBusinessChannelsRequiresChannelReadPermission(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.slot",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	missing := httptest.NewRecorder()
	srv.Engine().ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/manager/channels", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missing.Code, http.StatusUnauthorized)
	}

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied status = %d, want %d", denied.Code, http.StatusForbidden)
	}
}

func TestManagerBusinessChannelOperationRoutesStayUnmigrated(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channels", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestManagerSlotOperationRoutesStayUnmigrated(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/slots", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestManagerNodeOperationRoutesStayUnmigrated(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "*",
				Actions:  []string{"*"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/nodes/1/draining", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestManagerStartRequiresListenAddr(t *testing.T) {
	srv := New(Options{})

	if err := srv.Start(); err != ErrListenAddrRequired {
		t.Fatalf("Start() error = %v, want %v", err, ErrListenAddrRequired)
	}
}

type loginResponseBody struct {
	Username    string           `json:"username"`
	TokenType   string           `json:"token_type"`
	AccessToken string           `json:"access_token"`
	ExpiresIn   int64            `json:"expires_in"`
	ExpiresAt   time.Time        `json:"expires_at"`
	Permissions []permissionBody `json:"permissions"`
}

type permissionBody struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

func testAuthConfig(users []UserConfig) AuthConfig {
	return AuthConfig{
		On:        true,
		JWTSecret: "test-secret",
		JWTIssuer: "wukongim-manager",
		JWTExpire: time.Hour,
		Users:     users,
	}
}

func mustIssueTestToken(t *testing.T, srv *Server, username string) string {
	t.Helper()
	token, err := srv.issueToken(username, time.Now())
	if err != nil {
		t.Fatalf("issueToken() error = %v", err)
	}
	return token
}

type managerNodesStub struct {
	nodes                         managementusecase.NodeList
	slots                         []managementusecase.Slot
	channelRuntimeMeta            managementusecase.ListChannelRuntimeMetaResponse
	businessChannels              managementusecase.ListBusinessChannelsResponse
	recentConversations           managementusecase.RecentConversationsResponse
	messagesPage                  managementusecase.ListMessagesResponse
	connections                   []managementusecase.Connection
	connectionDetail              managementusecase.ConnectionDetail
	usersPage                     managementusecase.ListUsersResponse
	userDetail                    managementusecase.UserDetail
	slotLogEntriesPage            managementusecase.SlotLogEntriesResponse
	controllerLogEntriesPage      managementusecase.ControllerLogEntriesResponse
	kickUserResponse              managementusecase.KickUserResponse
	resetUserTokenResponse        managementusecase.ResetUserTokenResponse
	retentionResult               managementusecase.AdvanceMessageRetentionResponse
	systemUsers                   managementusecase.ListSystemUsersResponse
	systemUsersMutation           managementusecase.MutateSystemUsersResponse
	lastSlotsOptions              *managementusecase.ListSlotsOptions
	lastChannelRuntimeMetaRequest *managementusecase.ListChannelRuntimeMetaRequest
	lastBusinessChannelsRequest   *managementusecase.ListBusinessChannelsRequest
	recentConversationsReqSink    *managementusecase.RecentConversationsRequest
	messagesReqSink               *managementusecase.ListMessagesRequest
	connectionsReqSink            *managementusecase.ListConnectionsRequest
	connectionDetailReqSink       *managementusecase.GetConnectionRequest
	slotLogEntriesReqSink         *managementusecase.ListSlotLogEntriesRequest
	controllerLogEntriesReqSink   *managementusecase.ListControllerLogEntriesRequest
	retentionReqSink              *managementusecase.AdvanceMessageRetentionRequest
	usersReqSink                  *managementusecase.ListUsersRequest
	kickUserReqSink               *managementusecase.KickUserRequest
	resetUserTokenReqSink         *managementusecase.ResetUserTokenRequest
	systemUsersAddReq             *managementusecase.MutateSystemUsersRequest
	systemUsersRemoveReq          *managementusecase.MutateSystemUsersRequest
	err                           error
	slotsErr                      error
	channelRuntimeMetaErr         error
	businessChannelsErr           error
	recentConversationsErr        error
	messagesErr                   error
	connectionsErr                error
	connectionDetailErr           error
	slotLogEntriesErr             error
	controllerLogEntriesErr       error
	retentionErr                  error
	usersErr                      error
	userDetailErr                 error
	kickUserErr                   error
	resetUserTokenErr             error
	systemUsersErr                error
	systemUsersMutationErr        error
}

func (s managerNodesStub) ListNodes(context.Context) (managementusecase.NodeList, error) {
	return s.nodes, s.err
}

func (s managerNodesStub) ListSlots(_ context.Context, opts managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error) {
	if s.lastSlotsOptions != nil {
		*s.lastSlotsOptions = opts
	}
	return s.slots, s.slotsErr
}

func (s managerNodesStub) ListChannelRuntimeMeta(_ context.Context, req managementusecase.ListChannelRuntimeMetaRequest) (managementusecase.ListChannelRuntimeMetaResponse, error) {
	if s.lastChannelRuntimeMetaRequest != nil {
		*s.lastChannelRuntimeMetaRequest = req
	}
	return s.channelRuntimeMeta, s.channelRuntimeMetaErr
}

func (s managerNodesStub) ListSlotLogEntries(_ context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error) {
	if s.slotLogEntriesReqSink != nil {
		*s.slotLogEntriesReqSink = req
	}
	return s.slotLogEntriesPage, s.slotLogEntriesErr
}

func (s managerNodesStub) ListControllerLogEntries(_ context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error) {
	if s.controllerLogEntriesReqSink != nil {
		*s.controllerLogEntriesReqSink = req
	}
	return s.controllerLogEntriesPage, s.controllerLogEntriesErr
}

func (s managerNodesStub) ListBusinessChannels(_ context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error) {
	if s.lastBusinessChannelsRequest != nil {
		*s.lastBusinessChannelsRequest = req
	}
	return s.businessChannels, s.businessChannelsErr
}

func (s managerNodesStub) ListRecentConversations(_ context.Context, req managementusecase.RecentConversationsRequest) (managementusecase.RecentConversationsResponse, error) {
	if s.recentConversationsReqSink != nil {
		*s.recentConversationsReqSink = req
	}
	return s.recentConversations, s.recentConversationsErr
}

func (s managerNodesStub) ListMessages(_ context.Context, req managementusecase.ListMessagesRequest) (managementusecase.ListMessagesResponse, error) {
	if s.messagesReqSink != nil {
		*s.messagesReqSink = req
	}
	return s.messagesPage, s.messagesErr
}

func (s managerNodesStub) ListConnections(_ context.Context, req managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error) {
	if s.connectionsReqSink != nil {
		*s.connectionsReqSink = req
	}
	return append([]managementusecase.Connection(nil), s.connections...), s.connectionsErr
}

func (s managerNodesStub) GetConnection(_ context.Context, req managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error) {
	if s.connectionDetailReqSink != nil {
		*s.connectionDetailReqSink = req
	}
	return s.connectionDetail, s.connectionDetailErr
}

func (s managerNodesStub) AdvanceMessageRetention(_ context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	if s.retentionReqSink != nil {
		*s.retentionReqSink = req
	}
	return s.retentionResult, s.retentionErr
}

func (s managerNodesStub) ListUsers(_ context.Context, req managementusecase.ListUsersRequest) (managementusecase.ListUsersResponse, error) {
	if s.usersReqSink != nil {
		*s.usersReqSink = req
	}
	return s.usersPage, s.usersErr
}

func (s managerNodesStub) GetUser(_ context.Context, _ string) (managementusecase.UserDetail, error) {
	return s.userDetail, s.userDetailErr
}

func (s managerNodesStub) KickUser(_ context.Context, req managementusecase.KickUserRequest) (managementusecase.KickUserResponse, error) {
	if s.kickUserReqSink != nil {
		*s.kickUserReqSink = req
	}
	return s.kickUserResponse, s.kickUserErr
}

func (s managerNodesStub) ResetUserToken(_ context.Context, req managementusecase.ResetUserTokenRequest) (managementusecase.ResetUserTokenResponse, error) {
	if s.resetUserTokenReqSink != nil {
		*s.resetUserTokenReqSink = req
	}
	return s.resetUserTokenResponse, s.resetUserTokenErr
}

func (s managerNodesStub) ListSystemUsers(context.Context) (managementusecase.ListSystemUsersResponse, error) {
	return s.systemUsers, s.systemUsersErr
}

func (s managerNodesStub) AddSystemUsers(_ context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error) {
	if s.systemUsersAddReq != nil {
		*s.systemUsersAddReq = req
	}
	return s.systemUsersMutation, s.systemUsersMutationErr
}

func (s managerNodesStub) RemoveSystemUsers(_ context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error) {
	if s.systemUsersRemoveReq != nil {
		*s.systemUsersRemoveReq = req
	}
	return s.systemUsersMutation, s.systemUsersMutationErr
}

func jsonEqual(got, want string) bool {
	var gotValue any
	var wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		return false
	}
	return deepEqualJSON(gotValue, wantValue)
}

func deepEqualJSON(a, b any) bool {
	encodedA, err := json.Marshal(a)
	if err != nil {
		return false
	}
	encodedB, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(encodedA, encodedB)
}
