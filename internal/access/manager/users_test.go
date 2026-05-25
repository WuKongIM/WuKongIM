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

func TestManagerListUsersReturnsPage(t *testing.T) {
	var gotReq managementusecase.ListUsersRequest
	srv := New(Options{
		Management: managementStub{
			usersReqSink: &gotReq,
			usersPage: managementusecase.ListUsersResponse{
				Items: []managementusecase.UserListItem{{
					UID: "u1", SlotID: 1, HashSlot: 7,
				}},
				HasMore:    true,
				NextCursor: managementusecase.UserListCursor{SlotID: 1, UID: "u1"},
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/users?keyword=u&limit=1", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ListUsersRequest{Limit: 1, Keyword: "u"}, gotReq)

	var body UsersListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, []UserListItemDTO{{
		UID: "u1", SlotID: 1, HashSlot: 7,
	}}, body.Items)
	require.True(t, body.HasMore)
	cursor, err := decodeUserListCursor(body.NextCursor)
	require.NoError(t, err)
	require.Equal(t, managementusecase.UserListCursor{SlotID: 1, UID: "u1"}, cursor)
}

func TestManagerListUsersRejectsInvalidLimit(t *testing.T) {
	srv := New(Options{Management: managementStub{}})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/users?limit=0", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"error":"bad_request","message":"invalid limit"}`, rec.Body.String())
}

func TestManagerGetUserMapsNotFound(t *testing.T) {
	srv := New(Options{
		Management: managementStub{userDetailErr: metadb.ErrNotFound},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/users/u1", nil)
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":"not_found","message":"user not found"}`, rec.Body.String())
}

func TestManagerKickUserPassesParsedBody(t *testing.T) {
	var gotReq managementusecase.KickUserRequest
	srv := New(Options{
		Management: managementStub{
			kickUserReqSink: &gotReq,
			kickUserResponse: managementusecase.KickUserResponse{
				UID: "u1", DeviceFlag: "app", Changed: true,
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/users/u1/kick", bytes.NewBufferString(`{"device_flag":"app"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.KickUserRequest{UID: "u1", DeviceFlag: "app"}, gotReq)
	require.JSONEq(t, `{"uid":"u1","device_flag":"app","changed":true}`, rec.Body.String())
}

func TestManagerResetUserTokenPassesParsedBody(t *testing.T) {
	var gotReq managementusecase.ResetUserTokenRequest
	srv := New(Options{
		Management: managementStub{
			resetUserTokenReqSink: &gotReq,
			resetUserTokenResponse: managementusecase.ResetUserTokenResponse{
				UID: "u1", DeviceFlag: "web", DeviceLevel: "slave", Token: "next",
			},
		},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/manager/users/u1/token/reset", bytes.NewBufferString(`{"device_flag":"web","device_level":"slave"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, managementusecase.ResetUserTokenRequest{UID: "u1", DeviceFlag: "web", DeviceLevel: "slave"}, gotReq)
	require.JSONEq(t, `{"uid":"u1","device_flag":"web","device_level":"slave","token":"next"}`, rec.Body.String())
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
		Management: managementStub{},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/users", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusForbidden, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/manager/users/u1/kick", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "reader"))
	srv.Engine().ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
