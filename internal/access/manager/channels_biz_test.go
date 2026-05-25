package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestManagerListBusinessChannelsReturnsPage(t *testing.T) {
	inputCursor := managementusecase.ChannelListCursor{SlotID: 1, ChannelID: "a", ChannelType: 2, TypeFilter: 2, KeywordHash: 0xabc}
	nextCursor := managementusecase.ChannelListCursor{SlotID: 1, ChannelID: "g1", ChannelType: 2, TypeFilter: 2, KeywordHash: 0xabc}
	var gotReq managementusecase.ListBusinessChannelsRequest
	srv := New(Options{Management: managementStub{
		businessChannelsReqSink: &gotReq,
		businessChannelsPage: managementusecase.ListBusinessChannelsResponse{
			Items:      []managementusecase.BusinessChannelListItem{{ChannelID: "g1", ChannelType: 2, SlotID: 1, HashSlot: 7, Ban: true, SubscriberMutationVersion: 4}},
			HasMore:    true,
			NextCursor: nextCursor,
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels?type=2&keyword=g&limit=1&cursor="+mustEncodeBusinessChannelCursorForTest(t, inputCursor), nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListBusinessChannelsRequest{Limit: 1, Cursor: inputCursor, TypeFilter: 2, Keyword: "g"}, gotReq)

	var body BusinessChannelsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, []BusinessChannelListItemDTO{{ChannelID: "g1", ChannelType: 2, SlotID: 1, HashSlot: 7, Ban: true, SubscriberMutationVersion: 4}}, body.Items)
	require.True(t, body.HasMore)
	decoded, err := decodeBusinessChannelCursor(body.NextCursor)
	require.NoError(t, err)
	require.Equal(t, nextCursor, decoded)
}

func TestManagerListBusinessChannelsRejectsInvalidQuery(t *testing.T) {
	srv := New(Options{Management: managementStub{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels?limit=0", nil)
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/manager/channels?type=999", nil)
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/manager/channels?cursor=bad!", nil)
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestManagerBusinessChannelDetailMapsNotFound(t *testing.T) {
	srv := New(Options{Management: managementStub{businessChannelDetailErr: metadb.ErrNotFound}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels/2/g1", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"channel not found"}`, rec.Body.String())
}

func TestManagerBusinessChannelUpsertPassesBody(t *testing.T) {
	var gotReq managementusecase.UpsertBusinessChannelRequest
	srv := New(Options{Management: managementStub{
		businessChannelUpsertReqSink: &gotReq,
		businessChannelDetail: managementusecase.BusinessChannelDetail{BusinessChannelListItem: managementusecase.BusinessChannelListItem{
			ChannelID: "g1", ChannelType: 2, SlotID: 1, HashSlot: 7, Ban: true, SendBan: true,
		}},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channels", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2,"ban":true,"send_ban":true}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.UpsertBusinessChannelRequest{ChannelID: "g1", ChannelType: 2, Ban: true, SendBan: true}, gotReq)
	require.JSONEq(t, `{"channel_id":"g1","channel_type":2,"slot_id":1,"hash_slot":7,"ban":true,"disband":false,"send_ban":true,"subscriber_mutation_version":0,"has_subscribers":false,"has_allowlist":false,"has_denylist":false}`, rec.Body.String())
}

func TestManagerBusinessChannelMembersReturnsPage(t *testing.T) {
	inputCursor := managementusecase.ChannelMemberCursor{ChannelIDHash: 7, ChannelType: 2, ListKind: 2, UID: "u1"}
	nextCursor := managementusecase.ChannelMemberCursor{ChannelIDHash: 7, ChannelType: 2, ListKind: 2, UID: "u2"}
	var gotReq managementusecase.ListBusinessChannelMembersRequest
	srv := New(Options{Management: managementStub{
		businessChannelMembersReqSink: &gotReq,
		businessChannelMembersPage: managementusecase.ListBusinessChannelMembersResponse{
			Items: []managementusecase.BusinessChannelMember{{UID: "u2"}}, HasMore: true, NextCursor: nextCursor,
		},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels/2/g1/allowlist?limit=2&cursor="+mustEncodeBusinessChannelMemberCursorForTest(t, inputCursor), nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListBusinessChannelMembersRequest{ChannelID: "g1", ChannelType: 2, ListKind: "allowlist", Limit: 2, Cursor: inputCursor}, gotReq)
	var body BusinessChannelMembersResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, []BusinessChannelMemberDTO{{UID: "u2"}}, body.Items)
	require.True(t, body.HasMore)
	decoded, err := decodeBusinessChannelMemberCursor(body.NextCursor)
	require.NoError(t, err)
	require.Equal(t, nextCursor, decoded)
}

func TestManagerBusinessChannelMemberMutationsPassUIDs(t *testing.T) {
	var gotAdd managementusecase.MutateBusinessChannelMembersRequest
	var gotRemove managementusecase.MutateBusinessChannelMembersRequest
	srv := New(Options{Management: managementStub{
		businessChannelMutateReqSink:       &gotAdd,
		businessChannelSecondMutateReqSink: &gotRemove,
		businessChannelMutateResponse:      managementusecase.MutateBusinessChannelMembersResponse{ChannelID: "g1", ChannelType: 2, ListKind: "subscribers", Changed: true},
	}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/channels/2/g1/subscribers/add", bytes.NewBufferString(`{"uids":["u1","u2"]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.MutateBusinessChannelMembersRequest{ChannelID: "g1", ChannelType: 2, ListKind: "subscribers", UIDs: []string{"u1", "u2"}, Add: true}, gotAdd)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/channels/2/g1/denylist/remove", bytes.NewBufferString(`{"uids":["u3"]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.MutateBusinessChannelMembersRequest{ChannelID: "g1", ChannelType: 2, ListKind: "denylist", UIDs: []string{"u3"}, Add: false}, gotRemove)
}

func TestManagerBusinessChannelsRequireReadAndWritePermissions(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username:    "reader",
			Password:    "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.channel", Actions: []string{"r"}}},
		}}),
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/channels", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusForbidden, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/channels", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2}`))
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func mustEncodeBusinessChannelCursorForTest(t testing.TB, cursor managementusecase.ChannelListCursor) string {
	t.Helper()
	encoded, err := encodeBusinessChannelCursor(cursor)
	require.NoError(t, err)
	return encoded
}

func mustEncodeBusinessChannelMemberCursorForTest(t testing.TB, cursor managementusecase.ChannelMemberCursor) string {
	t.Helper()
	encoded, err := encodeBusinessChannelMemberCursor(cursor)
	require.NoError(t, err)
	return encoded
}
