package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	testdatausecase "github.com/WuKongIM/WuKongIM/internal/usecase/testdata"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestHealthzReturnsOK(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestMetricsEndpointUsesInjectedHandler(t *testing.T) {
	srv := New(Options{
		MetricsHandler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestHealthzDetailsReturnsInjectedSnapshot(t *testing.T) {
	srv := New(Options{
		HealthDetailEnabled: true,
		HealthDetails: func() any {
			return map[string]any{
				"status":    "healthy",
				"node_id":   1,
				"node_name": "node-1",
			}
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz/details", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":"healthy","node_id":1,"node_name":"node-1"}`, rec.Body.String())
}

func TestReadyzReturnsServiceUnavailableWhenNotReady(t *testing.T) {
	srv := New(Options{
		Readyz: func(context.Context) (bool, any) {
			return false, map[string]any{"status": "not_ready"}
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.JSONEq(t, `{"status":"not_ready"}`, rec.Body.String())
}

func TestDebugConfigRouteRequiresDebugEnable(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		srv := New(Options{
			DebugConfig: func() any {
				return map[string]any{"node_id": 1}
			},
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/debug/config", nil)

		srv.Engine().ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("enabled", func(t *testing.T) {
		srv := New(Options{
			DebugEnabled: true,
			DebugConfig: func() any {
				return map[string]any{"node_id": 1}
			},
		})

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/debug/config", nil)

		srv.Engine().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"node_id":1}`, rec.Body.String())
	})
}

func TestDebugClusterRouteRequiresDebugEnable(t *testing.T) {
	optsType := reflect.TypeOf(Options{})
	debugClusterField, ok := optsType.FieldByName("DebugCluster")
	require.True(t, ok, "Options should expose DebugCluster")
	require.Equal(t, reflect.TypeOf((func() any)(nil)), debugClusterField.Type)

	newServer := func(debugEnabled bool) *Server {
		opts := reflect.New(optsType).Elem()
		opts.FieldByName("DebugEnabled").SetBool(debugEnabled)
		opts.FieldByName("DebugCluster").Set(reflect.ValueOf(func() any {
			return map[string]any{"hash_slot_table_version": 7}
		}))
		return New(opts.Interface().(Options))
	}

	t.Run("disabled", func(t *testing.T) {
		srv := newServer(false)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/debug/cluster", nil)

		srv.Engine().ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("enabled", func(t *testing.T) {
		srv := newServer(true)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/debug/cluster", nil)

		srv.Engine().ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"hash_slot_table_version":7}`, rec.Body.String())
	})
}

func TestE2ETestDataRoutesRequireTestMode(t *testing.T) {
	testData := &recordingTestDataUsecase{}
	srv := New(Options{TestData: testData})

	for _, path := range []string{
		"/testdata/e2e/cluster/slot-snapshot-users",
		"/testdata/e2e/cluster/controller-snapshot-jobs",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"prefix":"snap","target_node_id":1,"count":1,"payload_bytes":8}`))
		req.Header.Set("Content-Type", "application/json")

		srv.Engine().ServeHTTP(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	}
	require.Empty(t, testData.slotSnapshotUserCommands)
	require.Empty(t, testData.controllerSnapshotJobCommands)
}

func TestE2ETestDataSlotSnapshotUsersMapsJSONToUsecaseCommand(t *testing.T) {
	testData := &recordingTestDataUsecase{
		slotSnapshotUsersResult: testdatausecase.GenerateSlotSnapshotUsersResult{
			Dataset:      "cluster/slot-snapshot-users",
			Prefix:       "snap",
			Count:        2,
			PayloadBytes: 32,
			FirstUID:     "snap-000000",
			LastUID:      "snap-000001",
		},
	}
	srv := New(Options{TestMode: true, TestData: testData})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/testdata/e2e/cluster/slot-snapshot-users", bytes.NewBufferString(`{"prefix":"snap","count":2,"payload_bytes":32,"seed":"abc","device_flag":1,"device_level":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"dataset":"cluster/slot-snapshot-users","prefix":"snap","count":2,"payload_bytes":32,"first_uid":"snap-000000","last_uid":"snap-000001"}`, rec.Body.String())
	require.Equal(t, []testdatausecase.GenerateSlotSnapshotUsersCommand{
		{
			Prefix:       "snap",
			Count:        2,
			PayloadBytes: 32,
			Seed:         "abc",
			DeviceFlag:   1,
			DeviceLevel:  1,
		},
	}, testData.slotSnapshotUserCommands)
}

func TestE2ETestDataControllerSnapshotJobsMapsJSONToUsecaseCommand(t *testing.T) {
	testData := &recordingTestDataUsecase{
		controllerSnapshotJobsResult: testdatausecase.GenerateControllerSnapshotJobsResult{
			Dataset:      "cluster/controller-snapshot-jobs",
			Prefix:       "snap-job",
			TargetNodeID: 2,
			Count:        3,
			PayloadBytes: 64,
			FirstJobID:   "job-000001",
			LastJobID:    "job-000003",
		},
	}
	srv := New(Options{TestMode: true, TestData: testData})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/testdata/e2e/cluster/controller-snapshot-jobs", bytes.NewBufferString(`{"prefix":"snap-job","target_node_id":2,"count":3,"payload_bytes":64,"seed":"abc"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"dataset":"cluster/controller-snapshot-jobs","prefix":"snap-job","target_node_id":2,"count":3,"payload_bytes":64,"first_job_id":"job-000001","last_job_id":"job-000003"}`, rec.Body.String())
	require.Equal(t, []testdatausecase.GenerateControllerSnapshotJobsCommand{{
		Prefix:       "snap-job",
		TargetNodeID: 2,
		Count:        3,
		PayloadBytes: 64,
		Seed:         "abc",
	}}, testData.controllerSnapshotJobCommands)
}

func TestRouteReturnsLegacyExternalAddresses(t *testing.T) {
	srv := New(Options{
		LegacyRouteExternal: LegacyRouteAddresses{
			TCPAddr: "198.51.100.10:5100",
			WSAddr:  "ws://198.51.100.10:5200",
			WSSAddr: "wss://198.51.100.10:5210",
		},
		LegacyRouteIntranet: LegacyRouteAddresses{
			TCPAddr: "10.0.0.10:5100",
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/route?uid=u1", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"tcp_addr":"198.51.100.10:5100","ws_addr":"ws://198.51.100.10:5200","wss_addr":"wss://198.51.100.10:5210"}`, rec.Body.String())
}

func TestRouteReturnsLegacyIntranetAddresses(t *testing.T) {
	srv := New(Options{
		LegacyRouteExternal: LegacyRouteAddresses{
			TCPAddr: "198.51.100.10:5100",
			WSAddr:  "ws://198.51.100.10:5200",
			WSSAddr: "wss://198.51.100.10:5210",
		},
		LegacyRouteIntranet: LegacyRouteAddresses{
			TCPAddr: "10.0.0.10:5100",
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/route?uid=u1&intranet=1", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"tcp_addr":"10.0.0.10:5100","ws_addr":"","wss_addr":""}`, rec.Body.String())
}

func TestRouteReturnsSpecifiedNodeExternalAddresses(t *testing.T) {
	srv := New(Options{
		LegacyRouteExternal: LegacyRouteAddresses{
			TCPAddr: "198.51.100.10:5100",
			WSAddr:  "ws://198.51.100.10:5200",
			WSSAddr: "wss://198.51.100.10:5210",
		},
		LegacyRouteNodes: map[uint64]LegacyRouteNodeAddresses{
			2: {
				External: LegacyRouteAddresses{
					TCPAddr: "198.51.100.20:5100",
					WSAddr:  "ws://198.51.100.20:5200",
					WSSAddr: "wss://198.51.100.20:5210",
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/route?uid=u1&node_id=2", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"tcp_addr":"198.51.100.20:5100","ws_addr":"ws://198.51.100.20:5200","wss_addr":"wss://198.51.100.20:5210"}`, rec.Body.String())
}

func TestRouteBatchReturnsSingleLegacyGroup(t *testing.T) {
	srv := New(Options{
		LegacyRouteExternal: LegacyRouteAddresses{
			TCPAddr: "198.51.100.10:5100",
			WSAddr:  "ws://198.51.100.10:5200",
			WSSAddr: "wss://198.51.100.10:5210",
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/route/batch", bytes.NewBufferString(`["u1","u2"]`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"uids":["u1","u2"],"tcp_addr":"198.51.100.10:5100","ws_addr":"ws://198.51.100.10:5200","wss_addr":"wss://198.51.100.10:5210"}]`, rec.Body.String())
}

func TestRouteBatchReturnsSpecifiedNodeIntranetAddresses(t *testing.T) {
	srv := New(Options{
		LegacyRouteNodes: map[uint64]LegacyRouteNodeAddresses{
			2: {
				External: LegacyRouteAddresses{
					TCPAddr: "198.51.100.20:5100",
					WSAddr:  "ws://198.51.100.20:5200",
					WSSAddr: "wss://198.51.100.20:5210",
				},
				Intranet: LegacyRouteAddresses{
					TCPAddr: "10.0.0.20:5100",
				},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/route/batch?node_id=2&intranet=1", bytes.NewBufferString(`["u1","u2"]`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"uids":["u1","u2"],"tcp_addr":"10.0.0.20:5100","ws_addr":"","wss_addr":""}]`, rec.Body.String())
}

func TestRouteBatchReturnsLegacyInvalidRequestError(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/route/batch", bytes.NewBufferString(`{"uids":["u1"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"数据格式有误！","status":400}`, rec.Body.String())
}

func TestCORSHeadersAddedToNormalResponses(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5175")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "http://localhost:5175", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, rec.Header().Values("Vary"), "Origin")
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodGet)
}

func TestCORSPreflightHandlesUserTokenRoute(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/user/token", nil)
	req.Header.Set("Origin", "http://localhost:5175")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type,authorization")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "http://localhost:5175", rec.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, rec.Header().Values("Vary"), "Origin")
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), http.MethodPost)
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Content-Type")
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization")
}

func TestSendMessageMapsJSONToUsecaseCommand(t *testing.T) {
	msgs := &recordingMessageUsecase{
		result: message.SendResult{
			MessageID:  99,
			MessageSeq: uint64(^uint32(0)) + 7,
			Reason:     frame.ReasonSuccess,
		},
	}
	srv := New(Options{Messages: msgs})

	body := map[string]any{
		"from_uid":      "u1",
		"channel_id":    "u2",
		"channel_type":  float64(frame.ChannelTypePerson),
		"client_msg_no": "api-client-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("hi")),
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"message_id":99,"message_seq":4294967302,"reason":1}`, rec.Body.String())
	require.Len(t, msgs.calls, 1)
	require.Equal(t, "u1", msgs.calls[0].FromUID)
	require.Equal(t, "u2", msgs.calls[0].ChannelID)
	require.Equal(t, uint8(frame.ChannelTypePerson), msgs.calls[0].ChannelType)
	require.Equal(t, "api-client-1", msgs.calls[0].ClientMsgNo)
	require.Equal(t, []byte("hi"), msgs.calls[0].Payload)
}

func TestSendMessageMapsSyncOnceAliasesToUsecaseCommand(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "header sync once",
			body: `{"sender_uid":"u1","channel_id":"g1","channel_type":2,"payload":"aGk=","header":{"sync_once":1}}`,
		},
		{
			name: "top level sync once",
			body: `{"sender_uid":"u1","channel_id":"g1","channel_type":2,"payload":"aGk=","sync_once":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := &recordingMessageUsecase{}
			srv := New(Options{Messages: msgs})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			require.Len(t, msgs.calls, 1)
			require.True(t, msgs.calls[0].Framer.SyncOnce)
		})
	}
}

func TestSendMessageMapsSubscribersToUsecaseCommand(t *testing.T) {
	msgs := &recordingMessageUsecase{
		result: message.SendResult{Reason: frame.ReasonSuccess},
	}
	srv := New(Options{Messages: msgs})

	body := map[string]any{
		"from_uid":      "system",
		"channel_type":  float64(frame.ChannelTypeGroup),
		"client_msg_no": "api-subscribers-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("cmd")),
		"subscribers":   []string{"u1", "u2"},
		"header": map[string]any{
			"sync_once": float64(1),
		},
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, msgs.calls, 1)
	require.Equal(t, "system", msgs.calls[0].FromUID)
	require.Empty(t, msgs.calls[0].ChannelID)
	require.Zero(t, msgs.calls[0].ChannelType)
	require.Equal(t, []string{"u1", "u2"}, msgs.calls[0].RequestSubscribers)
	require.True(t, msgs.calls[0].Framer.SyncOnce)
	require.Equal(t, "api-subscribers-1", msgs.calls[0].ClientMsgNo)
	require.Equal(t, []byte("cmd"), msgs.calls[0].Payload)
}

func TestSendMessagePassesPrecomposedPersonChannelToUsecase(t *testing.T) {
	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u1@u2","channel_type":1,"payload":"aGk="}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, msgs.calls, 1)
	require.Equal(t, "u1@u2", msgs.calls[0].ChannelID)
}

func TestSendMessagePreservesBusinessDenialReason(t *testing.T) {
	msgs := &recordingMessageUsecase{
		result: message.SendResult{Reason: frame.ReasonSubscriberNotExist},
	}
	srv := New(Options{Messages: msgs})

	body := map[string]any{
		"from_uid":      "u1",
		"channel_id":    "g1",
		"channel_type":  float64(frame.ChannelTypeGroup),
		"client_msg_no": "api-denied-1",
		"payload":       base64.StdEncoding.EncodeToString([]byte("hi")),
	}
	payload, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got struct {
		MessageID  int64  `json:"message_id"`
		MessageSeq uint64 `json:"message_seq"`
		Reason     uint8  `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, int64(0), got.MessageID)
	require.Equal(t, uint64(0), got.MessageSeq)
	require.Equal(t, uint8(frame.ReasonSubscriberNotExist), got.Reason)
	require.Len(t, msgs.calls, 1)
	require.Equal(t, "u1", msgs.calls[0].FromUID)
	require.Equal(t, "g1", msgs.calls[0].ChannelID)
	require.Equal(t, uint8(frame.ChannelTypeGroup), msgs.calls[0].ChannelType)
}

func TestSendMessageMapsUsecaseInvalidPersonChannelError(t *testing.T) {
	msgs := &recordingMessageUsecase{err: runtimechannelid.ErrInvalidPersonChannel}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u3@u4","channel_type":1,"payload":"aGk="}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid channel id"}`, rec.Body.String())
	require.Len(t, msgs.calls, 1)
	require.Equal(t, "u3@u4", msgs.calls[0].ChannelID)
}

func TestSendMessagePassesValidTraceHeader(t *testing.T) {
	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u2","channel_type":1,"payload":"aGk="}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-WK-Trace-ID", "ABCDEF0123456789ABCDEF0123456789")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, msgs.calls, 1)
	require.Equal(t, "abcdef0123456789abcdef0123456789", msgs.calls[0].TraceID)
}

func TestSendMessageGeneratesTraceIDForInvalidTraceHeader(t *testing.T) {
	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u2","channel_type":1,"payload":"aGk="}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-WK-Trace-ID", "not-a-valid-trace")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, msgs.calls, 1)
	require.NotEmpty(t, msgs.calls[0].TraceID)
	require.Len(t, msgs.calls[0].TraceID, 32)
	require.NotEqual(t, "not-a-valid-trace", msgs.calls[0].TraceID)
}

func TestSendMessagePropagatesHTTPRequestContext(t *testing.T) {
	type ctxKey string

	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	reqCtx := context.WithValue(context.Background(), ctxKey("request"), "api-send")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u2","channel_type":1,"payload":"aGk="}`)).WithContext(reqCtx)
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, msgs.sendContexts, 1)
	require.Equal(t, "api-send", msgs.sendContexts[0].Value(ctxKey("request")))
}

func TestSendMessageReturnsCanceledRequestContextError(t *testing.T) {
	msgs := &recordingMessageUsecase{
		sendFn: func(ctx context.Context, _ message.SendCommand) (message.SendResult, error) {
			return message.SendResult{}, ctx.Err()
		},
	}
	srv := New(Options{Messages: msgs})

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u2","channel_type":1,"payload":"aGk="}`)).WithContext(reqCtx)
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestTimeout, rec.Code)
	require.JSONEq(t, `{"error":"request canceled"}`, rec.Body.String())
	require.Len(t, msgs.sendContexts, 1)
	require.ErrorIs(t, msgs.sendContexts[0].Err(), context.Canceled)
}

func TestSendMessageRejectsInvalidBase64Payload(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u2","channel_type":1,"payload":"not-base64"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid payload"}`, rec.Body.String())
}

func TestSendMessageRejectsInvalidJSON(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid request"}`, rec.Body.String())
}

func TestSendMessageRejectsSubscribersWithChannelID(t *testing.T) {
	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"system","channel_id":"g1","payload":"aGk=","subscribers":["u1"],"header":{"sync_once":1}}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid request"}`, rec.Body.String())
	require.Empty(t, msgs.calls)
}

func TestSendMessageSubscribersWithoutSyncOnceMapsUsecaseValidation(t *testing.T) {
	msgs := message.New(message.Options{})
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"system","payload":"aGk=","subscribers":["u1"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"request subscribers require sync_once"}`, rec.Body.String())
}

func TestSendMessageRejectsEmptySubscribersWithoutOrdinaryChannel(t *testing.T) {
	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","payload":"aGk=","subscribers":[]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid request"}`, rec.Body.String())
	require.Empty(t, msgs.calls)
}

func TestSendMessageRejectsOrdinaryMissingChannel(t *testing.T) {
	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","payload":"aGk="}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid request"}`, rec.Body.String())
	require.Empty(t, msgs.calls)
}

func TestSendMessageReturnsInternalServerErrorWhenUsecaseFails(t *testing.T) {
	msgs := &recordingMessageUsecase{err: errors.New("boom")}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u2","channel_type":1,"payload":"aGk="}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.JSONEq(t, `{"error":"boom"}`, rec.Body.String())
}

func TestSendMessageMapsSemanticErrorsToHTTPStatus(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{
			name:   "channel not found",
			err:    channel.ErrChannelNotFound,
			status: http.StatusNotFound,
			body:   `{"error":"channel not found"}`,
		},
		{
			name:   "channel deleting",
			err:    channel.ErrChannelDeleting,
			status: http.StatusConflict,
			body:   `{"error":"channel deleting"}`,
		},
		{
			name:   "protocol upgrade required",
			err:    channel.ErrProtocolUpgradeRequired,
			status: http.StatusUpgradeRequired,
			body:   `{"error":"protocol upgrade required"}`,
		},
		{
			name:   "idempotency conflict",
			err:    channel.ErrIdempotencyConflict,
			status: http.StatusConflict,
			body:   `{"error":"idempotency conflict"}`,
		},
		{
			name:   "message seq exhausted",
			err:    channel.ErrMessageSeqExhausted,
			status: http.StatusConflict,
			body:   `{"error":"message seq exhausted"}`,
		},
		{
			name:   "stale meta",
			err:    channel.ErrStaleMeta,
			status: http.StatusServiceUnavailable,
			body:   `{"error":"retry required"}`,
		},
		{
			name:   "not leader",
			err:    channel.ErrNotLeader,
			status: http.StatusServiceUnavailable,
			body:   `{"error":"retry required"}`,
		},
		{
			name:   "invalid agent channel",
			err:    runtimechannelid.ErrInvalidAgentChannel,
			status: http.StatusBadRequest,
			body:   `{"error":"invalid channel id"}`,
		},
		{
			name:   "request subscribers require sync_once",
			err:    message.ErrRequestSubscribersRequireSyncOnce,
			status: http.StatusBadRequest,
			body:   `{"error":"request subscribers require sync_once"}`,
		},
		{
			name:   "request subscribers conflict channel",
			err:    message.ErrRequestSubscribersConflictChannel,
			status: http.StatusBadRequest,
			body:   `{"error":"request subscribers cannot include channel_id"}`,
		},
		{
			name:   "request subscribers required",
			err:    message.ErrRequestSubscribersRequired,
			status: http.StatusBadRequest,
			body:   `{"error":"request subscribers required"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{
				Messages: &recordingMessageUsecase{err: tt.err},
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u2","channel_type":1,"payload":"aGk="}`))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, tt.status, rec.Code)
			require.JSONEq(t, tt.body, rec.Body.String())
		})
	}
}

func TestChannelMessageSyncMapsLegacyRequestToUsecase(t *testing.T) {
	msgs := &recordingMessageUsecase{
		syncResult: message.SyncChannelMessagesResult{
			More: true,
			Messages: []channel.Message{
				{
					Framer:      frame.Framer{NoPersist: true, RedDot: true, SyncOnce: true},
					Setting:     3,
					MessageID:   88,
					MessageSeq:  2,
					ClientMsgNo: "c1",
					FromUID:     "u2",
					ChannelID:   runtimechannelid.EncodePersonChannel("u1", "u2"),
					ChannelType: frame.ChannelTypePerson,
					Expire:      60,
					Timestamp:   123,
					Payload:     []byte("hello"),
				},
			},
		},
	}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", bytes.NewBufferString(`{"login_uid":"u1","channel_id":"u2","channel_type":1,"start_message_seq":2,"end_message_seq":5,"limit":30,"pull_mode":1,"event_summary_mode":"full"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"start_message_seq":2,"end_message_seq":5,"more":1,"messages":[{"header":{"no_persist":1,"red_dot":1,"sync_once":1},"setting":3,"message_id":88,"message_idstr":"88","client_msg_no":"c1","message_seq":2,"from_uid":"u2","channel_id":"u2","channel_type":1,"expire":60,"timestamp":123,"payload":"aGVsbG8="}]}`, rec.Body.String())
	require.Len(t, msgs.syncQueries, 1)
	require.Equal(t, message.SyncChannelMessagesQuery{
		LoginUID:         "u1",
		ChannelID:        "u2",
		ChannelType:      frame.ChannelTypePerson,
		StartMessageSeq:  2,
		EndMessageSeq:    5,
		Limit:            30,
		PullMode:         message.PullModeUp,
		EventSummaryMode: "full",
	}, msgs.syncQueries[0])
}

func TestChannelMessageSyncReturnsLegacyEmptyResponse(t *testing.T) {
	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", bytes.NewBufferString(`{"login_uid":"u1","channel_id":"g1","channel_type":2}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"start_message_seq":0,"end_message_seq":0,"more":0,"messages":[]}`, rec.Body.String())
}

func TestChannelMessageSyncReturnsLegacyValidationError(t *testing.T) {
	srv := New(Options{Messages: message.New(message.Options{})})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/messagesync", bytes.NewBufferString(`{"login_uid":"","channel_id":"g1","channel_type":2}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"login_uid不能为空！","status":400}`, rec.Body.String())
}

func TestMessageSyncMapsLegacyRequestToCMDSyncUsecase(t *testing.T) {
	cmdSync := &recordingCMDSyncUsecase{
		syncResult: cmdsync.SyncResult{Messages: []channel.Message{
			{
				Framer:      frame.Framer{RedDot: true, SyncOnce: true},
				MessageID:   99,
				MessageSeq:  7,
				ClientMsgNo: "cmd-1",
				FromUID:     "system",
				ChannelID:   "g1",
				ChannelType: frame.ChannelTypeGroup,
				Timestamp:   123,
				Payload:     []byte("cmd"),
			},
			{
				MessageID:   100,
				MessageSeq:  8,
				FromUID:     "u2",
				ChannelID:   runtimechannelid.EncodePersonChannel("u1", "u2"),
				ChannelType: frame.ChannelTypePerson,
				Payload:     []byte("peer"),
			},
		}},
	}
	srv := New(Options{CMDSync: cmdSync})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/sync", bytes.NewBufferString(`{"uid":"u1","message_seq":3,"limit":20}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"header":{"no_persist":0,"red_dot":1,"sync_once":1},"setting":0,"message_id":99,"message_idstr":"99","client_msg_no":"cmd-1","message_seq":7,"from_uid":"system","channel_id":"g1","channel_type":2,"expire":0,"timestamp":123,"payload":"Y21k"},{"header":{"no_persist":0,"red_dot":0,"sync_once":0},"setting":0,"message_id":100,"message_idstr":"100","client_msg_no":"","message_seq":8,"from_uid":"u2","channel_id":"u2","channel_type":1,"expire":0,"timestamp":0,"payload":"cGVlcg=="}]`, rec.Body.String())
	require.Equal(t, []cmdsync.SyncQuery{{UID: "u1", MessageSeq: 3, Limit: 20}}, cmdSync.syncQueries)
}

func TestMessageSyncReturnsSourceChannelFromCommandLog(t *testing.T) {
	commandChannelID := runtimechannelid.ToCommandChannel("g1")
	states := &apiCMDStateStore{
		active: []metadb.CMDConversationState{{
			UID: "u1", ChannelID: commandChannelID, ChannelType: int64(frame.ChannelTypeGroup), ReadSeq: 4, ActiveAt: 100,
		}},
	}
	messages := &apiCMDMessageStore{
		byKey: map[cmdsync.CommandChannelKey][]channel.Message{
			{ChannelID: commandChannelID, ChannelType: frame.ChannelTypeGroup}: {{
				MessageID: 99, MessageSeq: 5, ChannelID: commandChannelID, ChannelType: frame.ChannelTypeGroup, Payload: []byte("cmd"),
			}},
		},
	}
	srv := New(Options{CMDSync: cmdsync.New(cmdsync.Options{States: states, Messages: messages})})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/sync", bytes.NewBufferString(`{"uid":"u1","limit":10}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"header":{"no_persist":0,"red_dot":0,"sync_once":0},"setting":0,"message_id":99,"message_idstr":"99","client_msg_no":"","message_seq":5,"from_uid":"","channel_id":"g1","channel_type":2,"expire":0,"timestamp":0,"payload":"Y21k"}]`, rec.Body.String())
	require.NotContains(t, rec.Body.String(), runtimechannelid.CommandChannelSuffix)
}

func TestMessageSyncReturnsLegacyValidationErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "missing uid", body: `{"uid":"","message_seq":1}`, want: `{"msg":"uid不能为空！","status":400}`},
		{name: "negative limit", body: `{"uid":"u1","limit":-1}`, want: `{"msg":"limit不能为负数！","status":400}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmdSync := &recordingCMDSyncUsecase{}
			srv := New(Options{CMDSync: cmdSync})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/sync", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.JSONEq(t, tt.want, rec.Body.String())
			require.Empty(t, cmdSync.syncQueries)
		})
	}
}

func TestMessageSyncAckMapsLegacyRequestToCMDSyncUsecase(t *testing.T) {
	cmdSync := &recordingCMDSyncUsecase{}
	srv := New(Options{CMDSync: cmdSync})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/syncack", bytes.NewBufferString(`{"uid":"u1","last_message_seq":9}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Equal(t, []cmdsync.SyncAckCommand{{UID: "u1", LastMessageSeq: 9}}, cmdSync.acks)
}

func TestMessageSyncAckRejectsMissingLastMessageSeq(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want string
	}{
		{name: "missing uid", body: `{"uid":"","last_message_seq":9}`, want: `{"msg":"uid不能为空！","status":400}`},
		{name: "missing last message seq", body: `{"uid":"u1","last_message_seq":0}`, want: `{"msg":"last_message_seq不能为空！","status":400}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmdSync := &recordingCMDSyncUsecase{}
			srv := New(Options{CMDSync: cmdSync})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/message/syncack", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.JSONEq(t, tt.want, rec.Body.String())
			require.Empty(t, cmdSync.acks)
		})
	}
}

func TestUpdateTokenMapsJSONToUsecaseCommand(t *testing.T) {
	users := &recordingUserUsecase{}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/token", bytes.NewBufferString(`{"uid":"u1","token":"t1","device_flag":0,"device_level":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Len(t, users.calls, 1)
	require.Equal(t, user.UpdateTokenCommand{
		UID:         "u1",
		Token:       "t1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
	}, users.calls[0])
}

func TestUpdateTokenRejectsInvalidJSONWithLegacyEnvelope(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/token", bytes.NewBufferString(`{"uid":`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"invalid request","status":400}`, rec.Body.String())
}

func TestUpdateTokenReturnsLegacyValidationErrorEnvelope(t *testing.T) {
	users := &recordingUserUsecase{err: errors.New("uid不能为空！")}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/token", bytes.NewBufferString(`{"uid":"","token":"t1","device_flag":0,"device_level":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"uid不能为空！","status":400}`, rec.Body.String())
}

func TestUpdateTokenReturnsLegacyBusinessErrorEnvelope(t *testing.T) {
	users := &recordingUserUsecase{err: errors.New("db busy")}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/token", bytes.NewBufferString(`{"uid":"u1","token":"t1","device_flag":0,"device_level":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"db busy","status":400}`, rec.Body.String())
}

func TestUpdateTokenReturnsLegacyMissingUserUsecaseEnvelope(t *testing.T) {
	srv := New(Options{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/token", bytes.NewBufferString(`{"uid":"u1","token":"t1","device_flag":0,"device_level":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"user usecase not configured","status":400}`, rec.Body.String())
}

func TestDeviceQuitMapsLegacyRequestToUsecaseCommand(t *testing.T) {
	users := &recordingUserUsecase{}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/device_quit", bytes.NewBufferString(`{"uid":"u1","device_flag":-1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Equal(t, []user.DeviceQuitCommand{{UID: "u1", DeviceFlag: -1}}, users.deviceQuitCommands)
}

func TestOnlineStatusReturnsLegacyArrayResponse(t *testing.T) {
	users := &recordingUserUsecase{
		onlineStatuses: []user.OnlineStatus{
			{UID: "u1", DeviceFlag: uint8(frame.APP), Online: 1},
			{UID: "u2", DeviceFlag: uint8(frame.WEB), Online: 1},
		},
	}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/onlinestatus", bytes.NewBufferString(`["u1","u2"]`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"uid":"u1","device_flag":0,"online":1},{"uid":"u2","device_flag":1,"online":1}]`, rec.Body.String())
	require.Equal(t, [][]string{{"u1", "u2"}}, users.onlineStatusQueries)
}

func TestOnlineStatusEmptyRequestReturnsLegacyOKEnvelope(t *testing.T) {
	users := &recordingUserUsecase{}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/onlinestatus", bytes.NewBufferString(`[]`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Empty(t, users.onlineStatusQueries)
}

func TestSystemUIDRoutesKeepLegacyContracts(t *testing.T) {
	users := &recordingUserUsecase{
		systemUIDs: []string{"sys1", "sys2"},
	}
	srv := New(Options{Users: users})

	for _, tt := range []struct {
		name   string
		path   string
		body   string
		assert func(t *testing.T)
	}{
		{
			name: "add",
			path: "/user/systemuids_add",
			body: `{"uids":["sys1","sys2"]}`,
			assert: func(t *testing.T) {
				require.Equal(t, [][]string{{"sys1", "sys2"}}, users.systemUIDAdds)
			},
		},
		{
			name: "remove",
			path: "/user/systemuids_remove",
			body: `{"uids":["sys1"]}`,
			assert: func(t *testing.T) {
				require.Equal(t, [][]string{{"sys1"}}, users.systemUIDRemoves)
			},
		},
		{
			name: "cache add",
			path: "/user/systemuids_add_to_cache",
			body: `{"uids":["sys1"]}`,
			assert: func(t *testing.T) {
				require.Equal(t, [][]string{{"sys1"}}, users.systemUIDCacheAdds)
			},
		},
		{
			name: "cache remove",
			path: "/user/systemuids_remove_from_cache",
			body: `{"uids":["sys1"]}`,
			assert: func(t *testing.T) {
				require.Equal(t, [][]string{{"sys1"}}, users.systemUIDCacheRemoves)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			require.JSONEq(t, `{"status":200}`, rec.Body.String())
			tt.assert(t)
		})
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/user/systemuids", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `["sys1","sys2"]`, rec.Body.String())
	require.Equal(t, 1, users.systemUIDListCalls)
}

func TestConversationSyncMapsLegacyRequestToUsecaseQuery(t *testing.T) {
	conversations := &recordingConversationUsecase{
		result: conversationusecase.SyncResult{
			Conversations: []conversationusecase.SyncConversation{},
		},
	}
	srv := New(Options{
		Conversations:            conversations,
		ConversationDefaultLimit: 200,
		ConversationMaxLimit:     500,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1","version":123,"last_msg_seqs":"u2:1:9|g1:2:7","msg_count":3,"only_unread":1,"exclude_channel_types":[3],"limit":999}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[]`, rec.Body.String())
	require.Len(t, conversations.queries, 1)
	require.Equal(t, conversationusecase.SyncQuery{
		UID:     "u1",
		Version: 123,
		LastMsgSeqs: map[conversationusecase.ConversationKey]uint64{
			{ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: frame.ChannelTypePerson}: 9,
			{ChannelID: "g1", ChannelType: 2}: 7,
		},
		MsgCount:            3,
		OnlyUnread:          true,
		ExcludeChannelTypes: []uint8{3},
		Limit:               500,
	}, conversations.queries[0])
}

func TestConversationSyncRejectsInvalidLastMsgSeqs(t *testing.T) {
	srv := New(Options{
		Conversations:            &recordingConversationUsecase{},
		ConversationDefaultLimit: 200,
		ConversationMaxLimit:     500,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1","last_msg_seqs":"bad-format"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid last_msg_seqs"}`, rec.Body.String())
}

func TestConversationSyncReturnsLegacyArrayResponse(t *testing.T) {
	conversations := &recordingConversationUsecase{
		result: conversationusecase.SyncResult{
			Conversations: []conversationusecase.SyncConversation{
				{
					ChannelID:       "u2",
					ChannelType:     frame.ChannelTypePerson,
					Unread:          2,
					Timestamp:       123,
					LastMsgSeq:      7,
					LastClientMsgNo: "c1",
					ReadToMsgSeq:    5,
					Version:         999,
					Recents: []channel.Message{
						{
							Framer:      frame.Framer{NoPersist: true, RedDot: true, SyncOnce: true},
							Setting:     3,
							MessageID:   88,
							MessageSeq:  7,
							ClientMsgNo: "c1",
							FromUID:     "u1",
							ChannelID:   runtimechannelid.EncodePersonChannel("u1", "u2"),
							ChannelType: frame.ChannelTypePerson,
							Expire:      60,
							Timestamp:   123,
							Payload:     []byte("hello"),
						},
					},
				},
			},
		},
	}
	srv := New(Options{
		Conversations:            conversations,
		ConversationDefaultLimit: 200,
		ConversationMaxLimit:     500,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1","msg_count":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"channel_id":"u2","channel_type":1,"unread":2,"timestamp":123,"last_msg_seq":7,"last_client_msg_no":"c1","offset_msg_seq":0,"readed_to_msg_seq":5,"version":999,"recents":[{"header":{"no_persist":1,"red_dot":1,"sync_once":1},"setting":3,"message_id":88,"message_idstr":"88","client_msg_no":"c1","message_seq":7,"from_uid":"u1","channel_id":"u2","channel_type":1,"expire":60,"timestamp":123,"payload":"aGVsbG8="}]}]`, rec.Body.String())
}

func TestConversationSyncIgnoresLegacySyncGate(t *testing.T) {
	srv := New(Options{
		Conversations:            &recordingConversationUsecase{},
		ConversationDefaultLimit: 200,
		ConversationMaxLimit:     500,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversation/sync", bytes.NewBufferString(`{"uid":"u1"}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[]`, rec.Body.String())
}

func TestConversationClearUnreadMapsLegacyRequestToUsecaseCommand(t *testing.T) {
	conversations := &recordingConversationUsecase{}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/clearUnread", bytes.NewBufferString(`{"uid":"u1","channel_id":"u2","channel_type":1,"message_seq":12}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Equal(t, []conversationusecase.ClearUnreadCommand{
		{
			UID:         "u1",
			ChannelID:   runtimechannelid.EncodePersonChannel("u1", "u2"),
			ChannelType: frame.ChannelTypePerson,
			MessageSeq:  12,
		},
	}, conversations.clearUnreadCommands)
}

func TestConversationSetUnreadMapsLegacyRequestToUsecaseCommand(t *testing.T) {
	conversations := &recordingConversationUsecase{}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/setUnread", bytes.NewBufferString(`{"uid":"u1","channel_id":"g1","channel_type":2,"unread":3}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Equal(t, []conversationusecase.SetUnreadCommand{
		{
			UID:         "u1",
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			Unread:      3,
		},
	}, conversations.setUnreadCommands)
}

func TestConversationDeleteMapsLegacyRequestToUsecaseCommand(t *testing.T) {
	conversations := &recordingConversationUsecase{}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/delete", bytes.NewBufferString(`{"uid":"u1","channel_id":"u2","channel_type":1,"message_seq":12}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Equal(t, []conversationusecase.DeleteConversationCommand{
		{
			UID:         "u1",
			ChannelID:   runtimechannelid.EncodePersonChannel("u1", "u2"),
			ChannelType: frame.ChannelTypePerson,
			MessageSeq:  12,
		},
	}, conversations.deleteCommands)
}

func TestConversationSetUnreadRejectsInvalidLegacyRequest(t *testing.T) {
	conversations := &recordingConversationUsecase{}
	srv := New(Options{Conversations: conversations})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/conversations/setUnread", bytes.NewBufferString(`{"uid":"","channel_id":"g1","channel_type":2,"unread":3}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"UID cannot be empty","status":400}`, rec.Body.String())
	require.Empty(t, conversations.setUnreadCommands)
}

type recordingMessageUsecase struct {
	calls        []message.SendCommand
	syncQueries  []message.SyncChannelMessagesQuery
	sendContexts []context.Context
	sendFn       func(context.Context, message.SendCommand) (message.SendResult, error)
	syncFn       func(context.Context, message.SyncChannelMessagesQuery) (message.SyncChannelMessagesResult, error)
	result       message.SendResult
	syncResult   message.SyncChannelMessagesResult
	err          error
	syncErr      error
}

func (r *recordingMessageUsecase) Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error) {
	r.sendContexts = append(r.sendContexts, ctx)
	r.calls = append(r.calls, cmd)
	if r.sendFn != nil {
		return r.sendFn(ctx, cmd)
	}
	return r.result, r.err
}

func (r *recordingMessageUsecase) SyncChannelMessages(ctx context.Context, query message.SyncChannelMessagesQuery) (message.SyncChannelMessagesResult, error) {
	r.syncQueries = append(r.syncQueries, query)
	if r.syncFn != nil {
		return r.syncFn(ctx, query)
	}
	return r.syncResult, r.syncErr
}

type recordingCMDSyncUsecase struct {
	syncQueries []cmdsync.SyncQuery
	acks        []cmdsync.SyncAckCommand
	syncResult  cmdsync.SyncResult
	syncErr     error
	ackErr      error
}

func (r *recordingCMDSyncUsecase) Sync(_ context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error) {
	r.syncQueries = append(r.syncQueries, query)
	return r.syncResult, r.syncErr
}

func (r *recordingCMDSyncUsecase) SyncAck(_ context.Context, cmd cmdsync.SyncAckCommand) error {
	r.acks = append(r.acks, cmd)
	return r.ackErr
}

// apiCMDStateStore is a small CMD sync state fake used by HTTP adapter tests.
type apiCMDStateStore struct {
	active  []metadb.CMDConversationState
	patches []metadb.CMDConversationReadPatch
	upserts []metadb.CMDConversationState
}

func (s *apiCMDStateStore) ListCMDConversationActive(_ context.Context, uid string, limit int) ([]metadb.CMDConversationState, error) {
	out := make([]metadb.CMDConversationState, 0, len(s.active))
	for _, state := range s.active {
		if state.UID != uid {
			continue
		}
		out = append(out, state)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *apiCMDStateStore) AdvanceCMDConversationReadSeq(_ context.Context, patches []metadb.CMDConversationReadPatch) error {
	s.patches = append(s.patches, patches...)
	return nil
}

func (s *apiCMDStateStore) UpsertCMDConversationStates(_ context.Context, states []metadb.CMDConversationState) error {
	s.upserts = append(s.upserts, states...)
	return nil
}

// apiCMDMessageStore is a command-log fake that filters by message sequence.
type apiCMDMessageStore struct {
	byKey map[cmdsync.CommandChannelKey][]channel.Message
}

func (s *apiCMDMessageStore) LoadCommandMessages(_ context.Context, key cmdsync.CommandChannelKey, fromSeq uint64, limit int) ([]channel.Message, error) {
	out := make([]channel.Message, 0, len(s.byKey[key]))
	for _, msg := range s.byKey[key] {
		if msg.MessageSeq < fromSeq {
			continue
		}
		out = append(out, msg)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

type recordingUserUsecase struct {
	calls                 []user.UpdateTokenCommand
	deviceQuitCommands    []user.DeviceQuitCommand
	onlineStatusQueries   [][]string
	onlineStatuses        []user.OnlineStatus
	systemUIDAdds         [][]string
	systemUIDRemoves      [][]string
	systemUIDCacheAdds    [][]string
	systemUIDCacheRemoves [][]string
	systemUIDs            []string
	systemUIDListCalls    int
	err                   error
	deviceQuitErr         error
	onlineStatusErr       error
	systemUIDErr          error
	systemUIDCacheErr     error
	systemUIDListErr      error
}

func (r *recordingUserUsecase) UpdateToken(_ context.Context, cmd user.UpdateTokenCommand) error {
	r.calls = append(r.calls, cmd)
	return r.err
}

func (r *recordingUserUsecase) DeviceQuit(_ context.Context, cmd user.DeviceQuitCommand) error {
	r.deviceQuitCommands = append(r.deviceQuitCommands, cmd)
	return r.deviceQuitErr
}

func (r *recordingUserUsecase) OnlineStatus(_ context.Context, uids []string) ([]user.OnlineStatus, error) {
	r.onlineStatusQueries = append(r.onlineStatusQueries, append([]string(nil), uids...))
	return append([]user.OnlineStatus(nil), r.onlineStatuses...), r.onlineStatusErr
}

func (r *recordingUserUsecase) AddSystemUIDs(_ context.Context, uids []string) error {
	r.systemUIDAdds = append(r.systemUIDAdds, append([]string(nil), uids...))
	return r.systemUIDErr
}

func (r *recordingUserUsecase) RemoveSystemUIDs(_ context.Context, uids []string) error {
	r.systemUIDRemoves = append(r.systemUIDRemoves, append([]string(nil), uids...))
	return r.systemUIDErr
}

func (r *recordingUserUsecase) ListSystemUIDs(context.Context) ([]string, error) {
	r.systemUIDListCalls++
	return append([]string(nil), r.systemUIDs...), r.systemUIDListErr
}

func (r *recordingUserUsecase) AddSystemUIDsToCache(uids []string) error {
	r.systemUIDCacheAdds = append(r.systemUIDCacheAdds, append([]string(nil), uids...))
	return r.systemUIDCacheErr
}

func (r *recordingUserUsecase) RemoveSystemUIDsFromCache(uids []string) error {
	r.systemUIDCacheRemoves = append(r.systemUIDCacheRemoves, append([]string(nil), uids...))
	return r.systemUIDCacheErr
}

type recordingTestDataUsecase struct {
	slotSnapshotUserCommands []testdatausecase.GenerateSlotSnapshotUsersCommand
	slotSnapshotUsersResult  testdatausecase.GenerateSlotSnapshotUsersResult
	slotSnapshotUsersErr     error

	controllerSnapshotJobCommands []testdatausecase.GenerateControllerSnapshotJobsCommand
	controllerSnapshotJobsResult  testdatausecase.GenerateControllerSnapshotJobsResult
	controllerSnapshotJobsErr     error
}

func (r *recordingTestDataUsecase) GenerateSlotSnapshotUsers(_ context.Context, cmd testdatausecase.GenerateSlotSnapshotUsersCommand) (testdatausecase.GenerateSlotSnapshotUsersResult, error) {
	r.slotSnapshotUserCommands = append(r.slotSnapshotUserCommands, cmd)
	return r.slotSnapshotUsersResult, r.slotSnapshotUsersErr
}

func (r *recordingTestDataUsecase) GenerateControllerSnapshotJobs(_ context.Context, cmd testdatausecase.GenerateControllerSnapshotJobsCommand) (testdatausecase.GenerateControllerSnapshotJobsResult, error) {
	r.controllerSnapshotJobCommands = append(r.controllerSnapshotJobCommands, cmd)
	return r.controllerSnapshotJobsResult, r.controllerSnapshotJobsErr
}

type recordingConversationUsecase struct {
	queries             []conversationusecase.SyncQuery
	clearUnreadCommands []conversationusecase.ClearUnreadCommand
	setUnreadCommands   []conversationusecase.SetUnreadCommand
	deleteCommands      []conversationusecase.DeleteConversationCommand
	result              conversationusecase.SyncResult
	err                 error
	clearUnreadErr      error
	setUnreadErr        error
	deleteErr           error
}

func (r *recordingConversationUsecase) Sync(_ context.Context, query conversationusecase.SyncQuery) (conversationusecase.SyncResult, error) {
	r.queries = append(r.queries, query)
	return r.result, r.err
}

func (r *recordingConversationUsecase) ClearUnread(_ context.Context, cmd conversationusecase.ClearUnreadCommand) error {
	r.clearUnreadCommands = append(r.clearUnreadCommands, cmd)
	return r.clearUnreadErr
}

func (r *recordingConversationUsecase) SetUnread(_ context.Context, cmd conversationusecase.SetUnreadCommand) error {
	r.setUnreadCommands = append(r.setUnreadCommands, cmd)
	return r.setUnreadErr
}

func (r *recordingConversationUsecase) DeleteConversation(_ context.Context, cmd conversationusecase.DeleteConversationCommand) error {
	r.deleteCommands = append(r.deleteCommands, cmd)
	return r.deleteErr
}
