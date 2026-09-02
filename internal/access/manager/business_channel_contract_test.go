package manager

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerBusinessChannelDetailAndUpdateKeepAuthoritativeIdentity(t *testing.T) {
	srv := New(Options{Management: managerNodesStub{}})
	detail := httptest.NewRecorder()
	srv.Engine().ServeHTTP(detail, httptest.NewRequest(http.MethodGet, "/manager/channels/2/room-a", nil))
	if detail.Code != http.StatusOK || !jsonEqual(detail.Body.String(), `{
		"channel_id":"room-a","channel_type":2,"slot_id":0,"hash_slot":0,
		"ban":false,"disband":false,"send_ban":false,"subscriber_mutation_version":0,
		"has_subscribers":false,"has_allowlist":false,"has_denylist":false
	}`) {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}

	updated := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/manager/channels/2/room-a", bytes.NewBufferString(`{
		"ban":true,"disband":false,"send_ban":true
	}`))
	request.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(updated, request)
	if updated.Code != http.StatusOK || !jsonEqual(updated.Body.String(), `{
		"channel_id":"room-a","channel_type":2,"slot_id":0,"hash_slot":0,
		"ban":true,"disband":false,"send_ban":true,"subscriber_mutation_version":0,
		"has_subscribers":false,"has_allowlist":false,"has_denylist":false
	}`) {
		t.Fatalf("update status=%d body=%s", updated.Code, updated.Body.String())
	}
}

func TestManagerBusinessChannelRoutesMapEachMemberSetOperationExactly(t *testing.T) {
	var listReq managementusecase.ListBusinessChannelMembersRequest
	var mutationReq managementusecase.MutateBusinessChannelMembersRequest
	srv := New(Options{Management: managerNodesStub{
		lastBusinessChannelMembersRequest:  &listReq,
		lastBusinessChannelMutationRequest: &mutationReq,
		businessChannelMembers: managementusecase.ListBusinessChannelMembersResponse{
			Items: []managementusecase.BusinessChannelMember{{UID: "member-a"}},
		},
	}})

	for _, test := range []struct {
		path string
		kind string
	}{
		{path: "/manager/channels/2/room-a/allowlist", kind: businessMemberListAllowlist},
		{path: "/manager/channels/2/room-a/denylist", kind: businessMemberListDenylist},
	} {
		listReq = managementusecase.ListBusinessChannelMembersRequest{}
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != http.StatusOK || listReq.ChannelID != "room-a" || listReq.ChannelType != 2 || listReq.ListKind != test.kind {
			t.Fatalf("path=%s status=%d request=%#v body=%s", test.path, recorder.Code, listReq, recorder.Body.String())
		}
	}

	for _, test := range []struct {
		path string
		kind string
		add  bool
	}{
		{path: "/manager/channels/2/room-a/subscribers/add", kind: businessMemberListSubscribers, add: true},
		{path: "/manager/channels/2/room-a/subscribers/remove", kind: businessMemberListSubscribers, add: false},
		{path: "/manager/channels/2/room-a/allowlist/add", kind: businessMemberListAllowlist, add: true},
		{path: "/manager/channels/2/room-a/allowlist/remove", kind: businessMemberListAllowlist, add: false},
		{path: "/manager/channels/2/room-a/denylist/add", kind: businessMemberListDenylist, add: true},
	} {
		mutationReq = managementusecase.MutateBusinessChannelMembersRequest{}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(`{"uids":["member-a"]}`))
		request.Header.Set("Content-Type", "application/json")
		srv.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || mutationReq.ChannelID != "room-a" || mutationReq.ChannelType != 2 ||
			mutationReq.ListKind != test.kind || mutationReq.Add != test.add || len(mutationReq.UIDs) != 1 || mutationReq.UIDs[0] != "member-a" {
			t.Fatalf("path=%s status=%d request=%#v body=%s", test.path, recorder.Code, mutationReq, recorder.Body.String())
		}
	}
}

func TestManagerBusinessChannelBoundariesRejectInvalidInputBeforeManagement(t *testing.T) {
	var listReq managementusecase.ListBusinessChannelMembersRequest
	var mutationReq managementusecase.MutateBusinessChannelMembersRequest
	srv := New(Options{Management: managerNodesStub{
		lastBusinessChannelMembersRequest:  &listReq,
		lastBusinessChannelMutationRequest: &mutationReq,
	}})
	tests := []struct {
		method string
		path   string
		body   []byte
	}{
		{method: http.MethodGet, path: "/manager/channels/0/room-a"},
		{method: http.MethodGet, path: "/manager/channels/256/room-a"},
		{method: http.MethodGet, path: "/manager/channels/2/room-a/allowlist?limit=501"},
		{method: http.MethodPost, path: "/manager/channels/2/room-a/subscribers/add", body: []byte(`{"uids":`)},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, test.path, bytes.NewReader(test.body))
		if test.body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		srv.Engine().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
	if listReq.ChannelID != "" || mutationReq.ChannelID != "" {
		t.Fatalf("management called for rejected input: list=%#v mutation=%#v", listReq, mutationReq)
	}
}

func TestManagerBusinessChannelErrorsUseStableHTTPStates(t *testing.T) {
	listTests := []struct {
		err  error
		want int
	}{
		{err: metadb.ErrInvalidArgument, want: http.StatusBadRequest},
		{err: managementusecase.ErrBusinessChannelReaderUnavailable, want: http.StatusServiceUnavailable},
		{err: managementusecase.ErrBusinessChannelControlUnavailable, want: http.StatusServiceUnavailable},
	}
	for _, test := range listTests {
		srv := New(Options{Management: managerNodesStub{businessChannelsErr: test.err}})
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/channels", nil))
		if recorder.Code != test.want {
			t.Fatalf("list error=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
	}

	detailTests := []struct {
		err  error
		want int
	}{
		{err: metadb.ErrInvalidArgument, want: http.StatusBadRequest},
		{err: metadb.ErrNotFound, want: http.StatusNotFound},
		{err: metadb.ErrAlreadyExists, want: http.StatusConflict},
		{err: managementusecase.ErrBusinessChannelControlUnavailable, want: http.StatusServiceUnavailable},
		{err: managementusecase.ErrBusinessChannelOperatorUnavailable, want: http.StatusServiceUnavailable},
		{err: managementusecase.ErrBusinessChannelAuthorityUnavailable, want: http.StatusServiceUnavailable},
	}
	for _, test := range detailTests {
		srv := New(Options{Management: managerNodesStub{businessChannelsErr: test.err}})
		recorder := httptest.NewRecorder()
		srv.Engine().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/manager/channels/2/room-a", nil))
		if recorder.Code != test.want {
			t.Fatalf("detail error=%v status=%d body=%s", test.err, recorder.Code, recorder.Body.String())
		}
	}
}
