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
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
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
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
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
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
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
		"from_uid":     "u1",
		"channel_id":   "u2",
		"channel_type": float64(frame.ChannelTypePerson),
		"payload":      base64.StdEncoding.EncodeToString([]byte("hi")),
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
	require.Equal(t, "u2@u1", msgs.calls[0].ChannelID)
	require.Equal(t, uint8(frame.ChannelTypePerson), msgs.calls[0].ChannelType)
	require.Equal(t, []byte("hi"), msgs.calls[0].Payload)
}

func TestSendMessageRejectsInvalidPersonChannelID(t *testing.T) {
	msgs := &recordingMessageUsecase{}
	srv := New(Options{Messages: msgs})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/message/send", bytes.NewBufferString(`{"sender_uid":"u1","channel_id":"u3@u4","channel_type":1,"payload":"aGk="}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"invalid channel id"}`, rec.Body.String())
	require.Empty(t, msgs.calls)
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
	require.Same(t, reqCtx, msgs.sendContexts[0])
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
	require.Same(t, reqCtx, msgs.sendContexts[0])
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

type recordingMessageUsecase struct {
	calls        []message.SendCommand
	sendContexts []context.Context
	sendFn       func(context.Context, message.SendCommand) (message.SendResult, error)
	result       message.SendResult
	err          error
}

func (r *recordingMessageUsecase) Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error) {
	r.sendContexts = append(r.sendContexts, ctx)
	r.calls = append(r.calls, cmd)
	if r.sendFn != nil {
		return r.sendFn(ctx, cmd)
	}
	return r.result, r.err
}

type recordingUserUsecase struct {
	calls []user.UpdateTokenCommand
	err   error
}

func (r *recordingUserUsecase) UpdateToken(_ context.Context, cmd user.UpdateTokenCommand) error {
	r.calls = append(r.calls, cmd)
	return r.err
}

type recordingConversationUsecase struct {
	queries []conversationusecase.SyncQuery
	result  conversationusecase.SyncResult
	err     error
}

func (r *recordingConversationUsecase) Sync(_ context.Context, query conversationusecase.SyncQuery) (conversationusecase.SyncResult, error) {
	r.queries = append(r.queries, query)
	return r.result, r.err
}
