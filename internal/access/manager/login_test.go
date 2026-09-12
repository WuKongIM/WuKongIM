package manager

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagerLoginInfo(t *testing.T) {
	for _, tc := range []struct {
		name    string
		users   []UserConfig
		authOff bool
		want    string
	}{
		{name: "configured guest", users: []UserConfig{{Username: "admin", Password: "private"}, {Username: "guest", Password: "custom-guest-password"}}, want: `{"guest":{"username":"guest","password":"custom-guest-password"}}`},
		{name: "no guest", users: []UserConfig{{Username: "admin", Password: "private"}}, want: `{}`},
		{name: "exact username only", users: []UserConfig{{Username: "Guest", Password: "private"}, {Username: "guest-admin", Password: "private"}}, want: `{}`},
		{name: "empty password", users: []UserConfig{{Username: "guest", Password: ""}}, want: `{"guest":{"username":"guest","password":""}}`},
		{name: "authentication disabled", users: []UserConfig{{Username: "guest", Password: "private"}}, authOff: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := testAuthConfig(tc.users)
			auth.On = !tc.authOff
			srv := New(Options{Auth: auth})
			rec := httptest.NewRecorder()
			srv.Engine().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/manager/login", nil))
			if tc.authOff {
				if rec.Code != http.StatusNotFound || bytes.Contains(rec.Body.Bytes(), []byte("private")) {
					t.Fatalf("disabled login info = %d %s", rec.Code, rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusOK || !jsonEqual(rec.Body.String(), tc.want) {
				t.Fatalf("login info = %d %s, want 200 %s", rec.Code, rec.Body.String(), tc.want)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("login info must not be cached")
			}
			if len(tc.users) > 0 && tc.users[len(tc.users)-1].Username == "guest" {
				login := httptest.NewRecorder()
				guest := tc.users[len(tc.users)-1]
				srv.Engine().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/manager/login", bytes.NewBufferString(`{"username":"guest","password":"`+guest.Password+`"}`)))
				if login.Code != http.StatusOK {
					t.Fatalf("advertised guest credentials cannot log in: %d", login.Code)
				}
			}
		})
	}
}
