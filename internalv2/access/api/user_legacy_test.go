package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	userusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestUpdateTokenMapsCompatibleRequestToUsecaseCommand(t *testing.T) {
	users := &recordingUserUsecase{}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/token", bytes.NewBufferString(`{"uid":"u1","token":"t1","device_flag":0,"device_level":1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "{\"status\":200}"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if len(users.tokenCommands) != 1 {
		t.Fatalf("tokenCommands = %#v, want one call", users.tokenCommands)
	}
	if got, want := users.tokenCommands[0], (userusecase.UpdateTokenCommand{
		UID:         "u1",
		Token:       "t1",
		DeviceFlag:  frame.APP,
		DeviceLevel: frame.DeviceLevelMaster,
	}); got != want {
		t.Fatalf("token command = %#v, want %#v", got, want)
	}
}

func TestUpdateTokenReturnsCompatibleErrorEnvelopes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		users UserUsecase
		body  string
		want  string
	}{
		{name: "invalid json", body: `{"uid":`, want: `{"msg":"invalid request","status":400}`},
		{name: "missing usecase", body: `{"uid":"u1","token":"t1","device_flag":0,"device_level":1}`, want: `{"msg":"user usecase not configured","status":400}`},
		{name: "usecase error", users: &recordingUserUsecase{err: errors.New("uid不能为空！")}, body: `{"uid":"","token":"t1","device_flag":0,"device_level":1}`, want: `{"msg":"uid不能为空！","status":400}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Users: tt.users})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/user/token", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.want) {
				t.Fatalf("body = %q, want JSON %s", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestDeviceQuitMapsCompatibleRequestToUsecaseCommand(t *testing.T) {
	users := &recordingUserUsecase{}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/device_quit", bytes.NewBufferString(`{"uid":"u1","device_flag":-1}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "{\"status\":200}"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if got, want := users.deviceQuitCommands, []userusecase.DeviceQuitCommand{{UID: "u1", DeviceFlag: -1}}; !equalDeviceQuitCommands(got, want) {
		t.Fatalf("device quit commands = %#v, want %#v", got, want)
	}
}

func TestOnlineStatusReturnsCompatibleResponses(t *testing.T) {
	users := &recordingUserUsecase{
		onlineStatuses: []userusecase.OnlineStatus{
			{UID: "u1", DeviceFlag: uint8(frame.APP), Online: 1},
			{UID: "u2", DeviceFlag: uint8(frame.WEB), Online: 1},
		},
	}
	srv := New(Options{Users: users})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/user/onlinestatus", bytes.NewBufferString(`["u1","u2"]`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `[{"uid":"u1","device_flag":0,"online":1},{"uid":"u2","device_flag":1,"online":1}]`) {
		t.Fatalf("body = %q, want online status array", rec.Body.String())
	}
	if len(users.onlineStatusQueries) != 1 || !equalStrings(users.onlineStatusQueries[0], []string{"u1", "u2"}) {
		t.Fatalf("onlineStatusQueries = %#v, want u1/u2", users.onlineStatusQueries)
	}

	emptyRec := httptest.NewRecorder()
	emptyReq := httptest.NewRequest(http.MethodPost, "/user/onlinestatus", bytes.NewBufferString(`[]`))
	emptyReq.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(emptyRec, emptyReq)

	if emptyRec.Code != http.StatusOK {
		t.Fatalf("empty status = %d body = %s, want 200", emptyRec.Code, emptyRec.Body.String())
	}
	if got, want := emptyRec.Body.String(), "{\"status\":200}"; got != want {
		t.Fatalf("empty body = %q, want %q", got, want)
	}
	if len(users.onlineStatusQueries) != 1 {
		t.Fatalf("onlineStatusQueries = %#v, want no query for empty request", users.onlineStatusQueries)
	}
}

func TestSystemUIDRoutesKeepCompatibleContracts(t *testing.T) {
	users := &recordingUserUsecase{systemUIDs: []string{"sys1", "sys2"}}
	srv := New(Options{Users: users})

	for _, tt := range []struct {
		name   string
		path   string
		body   string
		assert func(t *testing.T)
	}{
		{name: "add", path: "/user/systemuids_add", body: `{"uids":["sys1","sys2"]}`, assert: func(t *testing.T) {
			if len(users.systemUIDAdds) != 1 || !equalStrings(users.systemUIDAdds[0], []string{"sys1", "sys2"}) {
				t.Fatalf("systemUIDAdds = %#v, want sys1/sys2", users.systemUIDAdds)
			}
		}},
		{name: "remove", path: "/user/systemuids_remove", body: `{"uids":["sys1"]}`, assert: func(t *testing.T) {
			if len(users.systemUIDRemoves) != 1 || !equalStrings(users.systemUIDRemoves[0], []string{"sys1"}) {
				t.Fatalf("systemUIDRemoves = %#v, want sys1", users.systemUIDRemoves)
			}
		}},
		{name: "cache add", path: "/user/systemuids_add_to_cache", body: `{"uids":["sys1"]}`, assert: func(t *testing.T) {
			if len(users.systemUIDCacheAdds) != 1 || !equalStrings(users.systemUIDCacheAdds[0], []string{"sys1"}) {
				t.Fatalf("systemUIDCacheAdds = %#v, want sys1", users.systemUIDCacheAdds)
			}
		}},
		{name: "cache remove", path: "/user/systemuids_remove_from_cache", body: `{"uids":["sys1"]}`, assert: func(t *testing.T) {
			if len(users.systemUIDCacheRemoves) != 1 || !equalStrings(users.systemUIDCacheRemoves[0], []string{"sys1"}) {
				t.Fatalf("systemUIDCacheRemoves = %#v, want sys1", users.systemUIDCacheRemoves)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
			}
			if got, want := rec.Body.String(), "{\"status\":200}"; got != want {
				t.Fatalf("body = %q, want %q", got, want)
			}
			tt.assert(t)
		})
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/user/systemuids", nil)

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if !jsonEqual(rec.Body.String(), `["sys1","sys2"]`) {
		t.Fatalf("body = %q, want system UID array", rec.Body.String())
	}
	if users.systemUIDListCalls != 1 {
		t.Fatalf("systemUIDListCalls = %d, want 1", users.systemUIDListCalls)
	}
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
	return reflect.DeepEqual(gotValue, wantValue)
}

type recordingUserUsecase struct {
	tokenCommands         []userusecase.UpdateTokenCommand
	deviceQuitCommands    []userusecase.DeviceQuitCommand
	onlineStatusQueries   [][]string
	onlineStatuses        []userusecase.OnlineStatus
	systemUIDAdds         [][]string
	systemUIDRemoves      [][]string
	systemUIDCacheAdds    [][]string
	systemUIDCacheRemoves [][]string
	systemUIDs            []string
	systemUIDListCalls    int
	err                   error
}

func (r *recordingUserUsecase) UpdateToken(_ context.Context, cmd userusecase.UpdateTokenCommand) error {
	r.tokenCommands = append(r.tokenCommands, cmd)
	return r.err
}

func (r *recordingUserUsecase) DeviceQuit(_ context.Context, cmd userusecase.DeviceQuitCommand) error {
	r.deviceQuitCommands = append(r.deviceQuitCommands, cmd)
	return r.err
}

func (r *recordingUserUsecase) OnlineStatus(_ context.Context, uids []string) ([]userusecase.OnlineStatus, error) {
	r.onlineStatusQueries = append(r.onlineStatusQueries, append([]string(nil), uids...))
	return append([]userusecase.OnlineStatus(nil), r.onlineStatuses...), r.err
}

func (r *recordingUserUsecase) AddSystemUIDs(_ context.Context, uids []string) error {
	r.systemUIDAdds = append(r.systemUIDAdds, append([]string(nil), uids...))
	return r.err
}

func (r *recordingUserUsecase) RemoveSystemUIDs(_ context.Context, uids []string) error {
	r.systemUIDRemoves = append(r.systemUIDRemoves, append([]string(nil), uids...))
	return r.err
}

func (r *recordingUserUsecase) ListSystemUIDs(context.Context) ([]string, error) {
	r.systemUIDListCalls++
	return append([]string(nil), r.systemUIDs...), r.err
}

func (r *recordingUserUsecase) AddSystemUIDsToCache(uids []string) error {
	r.systemUIDCacheAdds = append(r.systemUIDCacheAdds, append([]string(nil), uids...))
	return r.err
}

func (r *recordingUserUsecase) RemoveSystemUIDsFromCache(uids []string) error {
	r.systemUIDCacheRemoves = append(r.systemUIDCacheRemoves, append([]string(nil), uids...))
	return r.err
}

func equalDeviceQuitCommands(a, b []userusecase.DeviceQuitCommand) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
