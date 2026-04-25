package manager

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/stretchr/testify/require"
)

func TestManagerMessagesRejectsMissingChannelID(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/messages?channel_type=2", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"channel_id is required"}`, rec.Body.String())
}

func TestManagerMessagesReturnsPagedList(t *testing.T) {
	var received managementusecase.ListMessagesRequest
	inputCursor := managementusecase.MessageListCursor{BeforeSeq: 11}
	nextCursor := managementusecase.MessageListCursor{BeforeSeq: 9}
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "admin",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.channel",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managementStub{
			messagesReqSink: &received,
			messagesPage: managementusecase.ListMessagesResponse{
				Items: []managementusecase.Message{{
					MessageID:   101,
					MessageSeq:  10,
					ClientMsgNo: "c-101",
					ChannelID:   "room-1",
					ChannelType: 2,
					FromUID:     "u1",
					Timestamp:   1713859200,
					Payload:     []byte("hello"),
				}},
				HasMore:    true,
				NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/messages?channel_id=room-1&channel_type=2&limit=1&message_id=101&client_msg_no=c-101&cursor="+mustEncodeMessageCursorForTest(t, inputCursor), nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "admin"))

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListMessagesRequest{
		ChannelID:   "room-1",
		ChannelType: 2,
		Limit:       1,
		Cursor:      inputCursor,
		MessageID:   101,
		ClientMsgNo: "c-101",
	}, received)
	require.JSONEq(t, fmt.Sprintf(`{
		"items": [{
			"message_id": 101,
			"message_seq": 10,
			"client_msg_no": "c-101",
			"channel_id": "room-1",
			"channel_type": 2,
			"from_uid": "u1",
			"timestamp": 1713859200,
			"payload": "aGVsbG8="
		}],
		"has_more": true,
		"next_cursor": %q
	}`, mustEncodeMessageCursorForTest(t, nextCursor)), rec.Body.String())
}

func mustEncodeMessageCursorForTest(t *testing.T, cursor managementusecase.MessageListCursor) string {
	t.Helper()
	payload, err := json.Marshal(messageCursorPayload{
		Version:   1,
		BeforeSeq: cursor.BeforeSeq,
	})
	require.NoError(t, err)
	return base64.RawURLEncoding.EncodeToString(payload)
}
