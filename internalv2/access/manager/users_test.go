package manager

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerListUsersReturnsPage(t *testing.T) {
	nextCursor := managementusecase.UserListCursor{SlotID: 1, UID: "u1"}
	var gotReq managementusecase.ListUsersRequest
	srv := New(Options{
		Management: managerNodesStub{
			usersReqSink: &gotReq,
			usersPage: managementusecase.ListUsersResponse{
				Items: []managementusecase.UserListItem{{
					UID: "u1", SlotID: 1, HashSlot: 7,
				}},
				HasMore:    true,
				NextCursor: nextCursor,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/users?keyword=u&limit=1", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotReq.Limit != 1 || gotReq.Keyword != "u" {
		t.Fatalf("request = %#v, want keyword u limit 1", gotReq)
	}
	var body UsersListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].UID != "u1" || body.Items[0].SlotID != 1 || body.Items[0].HashSlot != 7 {
		t.Fatalf("items = %#v, want user row", body.Items)
	}
	if !body.HasMore || body.NextCursor == "" {
		t.Fatalf("pagination = has_more:%t next:%q, want cursor", body.HasMore, body.NextCursor)
	}
	decoded, err := decodeUserListCursor(body.NextCursor)
	if err != nil {
		t.Fatalf("decodeUserListCursor() error = %v", err)
	}
	if decoded != nextCursor {
		t.Fatalf("decoded cursor = %#v, want %#v", decoded, nextCursor)
	}
}

func TestManagerGetUserMapsDetailAndNotFound(t *testing.T) {
	srv := New(Options{
		Management: managerNodesStub{
			userDetail: managementusecase.UserDetail{
				UID: "u1", SlotID: 1, HashSlot: 7, Online: true,
				Devices: []managementusecase.UserDevice{{
					DeviceFlag: "app", DeviceLevel: "master", TokenSet: true, Online: true, OnlineSessionCount: 1,
				}},
				Connections: []managementusecase.Connection{{
					NodeID: 2, SessionID: 10, UID: "u1", DeviceID: "d1", DeviceFlag: "app", DeviceLevel: "master", Listener: "tcp",
				}},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/users/u1", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{
		"uid":"u1",
		"slot_id":1,
		"hash_slot":7,
		"online":true,
		"devices":[{"device_flag":"app","device_level":"master","token_set":true,"online":true,"online_session_count":1}],
		"connections":[{"node_id":2,"session_id":10,"uid":"u1","device_id":"d1","device_flag":"app","device_level":"master","slot_id":0,"state":"","listener":"tcp","connected_at":"0001-01-01T00:00:00Z","remote_addr":"","local_addr":""}]
	}`) {
		t.Fatalf("body = %s", rec.Body.String())
	}

	notFound := New(Options{Management: managerNodesStub{userDetailErr: metadb.ErrNotFound}})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/manager/users/missing", nil)
	notFound.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not found status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `{"error":"not_found","message":"user not found"}`) {
		t.Fatalf("not found body = %s", rec.Body.String())
	}
}

func TestManagerUserWritesPassParsedBodies(t *testing.T) {
	var kickReq managementusecase.KickUserRequest
	var resetReq managementusecase.ResetUserTokenRequest
	srv := New(Options{
		Management: managerNodesStub{
			kickUserReqSink: &kickReq,
			kickUserResponse: managementusecase.KickUserResponse{
				UID: "u1", DeviceFlag: "app", Changed: true,
			},
			resetUserTokenReqSink: &resetReq,
			resetUserTokenResponse: managementusecase.ResetUserTokenResponse{
				UID: "u1", DeviceFlag: "web", DeviceLevel: "slave", Token: "next",
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/users/u1/kick", bytes.NewBufferString(`{"device_flag":"app"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("kick status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if kickReq != (managementusecase.KickUserRequest{UID: "u1", DeviceFlag: "app"}) {
		t.Fatalf("kick request = %#v, want parsed request", kickReq)
	}
	if !jsonEqual(rec.Body.String(), `{"uid":"u1","device_flag":"app","changed":true}`) {
		t.Fatalf("kick body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/users/u1/token/reset", bytes.NewBufferString(`{"device_flag":"web","device_level":"slave"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if resetReq != (managementusecase.ResetUserTokenRequest{UID: "u1", DeviceFlag: "web", DeviceLevel: "slave"}) {
		t.Fatalf("reset request = %#v, want parsed request", resetReq)
	}
	if !jsonEqual(rec.Body.String(), `{"uid":"u1","device_flag":"web","device_level":"slave","token":"next"}`) {
		t.Fatalf("reset body = %s", rec.Body.String())
	}
}

func TestManagerUsersRequireReadAndWritePermissions(t *testing.T) {
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "reader",
			Password: "secret",
			Permissions: []PermissionConfig{{
				Resource: "cluster.user",
				Actions:  []string{"r"},
			}},
		}}),
		Management: managerNodesStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/users", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("read status = %d, want allowed read", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/users/u1/kick", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("write status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestManagerSystemUsersRoutes(t *testing.T) {
	var addReq managementusecase.MutateSystemUsersRequest
	var removeReq managementusecase.MutateSystemUsersRequest
	srv := New(Options{
		Management: managerNodesStub{
			systemUsers:          managementusecase.ListSystemUsersResponse{Items: []managementusecase.SystemUser{{UID: "sys-a"}, {UID: "sys-b"}}, Total: 2},
			systemUsersAddReq:    &addReq,
			systemUsersRemoveReq: &removeReq,
			systemUsersMutation:  managementusecase.MutateSystemUsersResponse{UIDs: []string{"sys-a"}, Changed: true},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/system-users", nil)
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !jsonEqual(rec.Body.String(), `{"items":[{"uid":"sys-a"},{"uid":"sys-b"}],"total":2}`) {
		t.Fatalf("list status/body = %d %s, want system user rows", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/system-users/add", bytes.NewBufferString(`{"uids":["sys-a"]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !sameStrings(addReq.UIDs, []string{"sys-a"}) || !jsonEqual(rec.Body.String(), `{"uids":["sys-a"],"changed":true}`) {
		t.Fatalf("add status/body/request = %d %s %#v, want mutation", rec.Code, rec.Body.String(), addReq)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/system-users/remove", bytes.NewBufferString(`{"uids":["sys-a"]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !sameStrings(removeReq.UIDs, []string{"sys-a"}) || !jsonEqual(rec.Body.String(), `{"uids":["sys-a"],"changed":true}`) {
		t.Fatalf("remove status/body/request = %d %s %#v, want mutation", rec.Code, rec.Body.String(), removeReq)
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
