package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	channelusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestChannelCreateMapsCompatibleRequestToUsecaseCommand(t *testing.T) {
	channels := &recordingChannelUsecase{}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2,"large":1,"ban":1,"disband":1,"send_ban":1,"allow_stranger":1,"reset":1,"subscribers":["u1","u2"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "{\"status\":200}"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if len(channels.upserts) != 1 {
		t.Fatalf("upserts = %#v, want one call", channels.upserts)
	}
	got := channels.upserts[0]
	if got.Info.ChannelID != "g1" || got.Info.ChannelType != frame.ChannelTypeGroup ||
		!got.Info.Large || !got.Info.Ban || !got.Info.Disband || !got.Info.SendBan || !got.Info.AllowStranger ||
		!got.Reset || !equalStrings(got.Subscribers, []string{"u1", "u2"}) {
		t.Fatalf("upsert command = %#v, want mapped compatible command", got)
	}
}

func TestChannelSubscriberAddDefaultsCompatibleGroupType(t *testing.T) {
	channels := &recordingChannelUsecase{}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/subscriber_add", bytes.NewBufferString(`{"channel_id":"g1","subscribers":["u1"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if len(channels.addSubscribers) != 1 || channels.addSubscribers[0].ChannelType != frame.ChannelTypeGroup {
		t.Fatalf("addSubscribers = %#v, want default group type", channels.addSubscribers)
	}
}

func TestChannelWhitelistGetReturnsCompatibleMemberArray(t *testing.T) {
	channels := &recordingChannelUsecase{
		listAllowResult: channelusecase.MemberListResult{
			Members: []channelusecase.Member{{UID: "u1"}, {ID: 7, UID: "u2"}},
		},
	}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/channel/whitelist?channel_id=g1&channel_type=2", nil)

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "[{\"id\":0,\"uid\":\"u1\"},{\"id\":7,\"uid\":\"u2\"}]"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if len(channels.listAllow) != 1 || channels.listAllow[0].ChannelID != "g1" || channels.listAllow[0].ChannelType != frame.ChannelTypeGroup {
		t.Fatalf("listAllow = %#v, want g1 group", channels.listAllow)
	}
}

func TestChannelCompatibleMutationRoutesMapToUsecase(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		body   string
		assert func(t *testing.T, channels *recordingChannelUsecase)
	}{
		{name: "info", path: "/channel/info", body: `{"channel_id":"g1","channel_type":2,"ban":1}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.updateInfos) != 1 || channels.updateInfos[0].ChannelID != "g1" || !channels.updateInfos[0].Ban {
				t.Fatalf("updateInfos = %#v, want ban update", channels.updateInfos)
			}
		}},
		{name: "delete", path: "/channel/delete", body: `{"channel_id":"g1","channel_type":2}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.deletes) != 1 || channels.deletes[0].ChannelID != "g1" {
				t.Fatalf("deletes = %#v, want g1", channels.deletes)
			}
		}},
		{name: "subscriber remove", path: "/channel/subscriber_remove", body: `{"channel_id":"g1","channel_type":2,"subscribers":["u1"]}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.removeSubscribers) != 1 || channels.removeSubscribers[0].Subscribers[0] != "u1" {
				t.Fatalf("removeSubscribers = %#v, want u1", channels.removeSubscribers)
			}
		}},
		{name: "subscriber remove all", path: "/channel/subscriber_remove_all", body: `{"channel_id":"g1","channel_type":2}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.removeAllSubscribers) != 1 || channels.removeAllSubscribers[0].ChannelID != "g1" {
				t.Fatalf("removeAllSubscribers = %#v, want g1", channels.removeAllSubscribers)
			}
		}},
		{name: "temporary subscriber set", path: "/tmpchannel/subscriber_set", body: `{"channel_id":"t1","uids":["u1"]}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.setTempSubscribers) != 1 || channels.setTempSubscribers[0].ChannelID != "t1" {
				t.Fatalf("setTempSubscribers = %#v, want t1", channels.setTempSubscribers)
			}
		}},
		{name: "blacklist add", path: "/channel/blacklist_add", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.addDeny) != 1 || channels.addDeny[0].UIDs[0] != "u1" {
				t.Fatalf("addDeny = %#v, want u1", channels.addDeny)
			}
		}},
		{name: "blacklist set", path: "/channel/blacklist_set", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.setDeny) != 1 || channels.setDeny[0].UIDs[0] != "u1" {
				t.Fatalf("setDeny = %#v, want u1", channels.setDeny)
			}
		}},
		{name: "blacklist remove", path: "/channel/blacklist_remove", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.removeDeny) != 1 || channels.removeDeny[0].UIDs[0] != "u1" {
				t.Fatalf("removeDeny = %#v, want u1", channels.removeDeny)
			}
		}},
		{name: "blacklist remove all", path: "/channel/blacklist_remove_all", body: `{"channel_id":"g1","channel_type":2}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.removeAllDeny) != 1 || channels.removeAllDeny[0].ChannelID != "g1" {
				t.Fatalf("removeAllDeny = %#v, want g1", channels.removeAllDeny)
			}
		}},
		{name: "whitelist add", path: "/channel/whitelist_add", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.addAllow) != 1 || channels.addAllow[0].UIDs[0] != "u1" {
				t.Fatalf("addAllow = %#v, want u1", channels.addAllow)
			}
		}},
		{name: "whitelist set", path: "/channel/whitelist_set", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.setAllow) != 1 || channels.setAllow[0].UIDs[0] != "u1" {
				t.Fatalf("setAllow = %#v, want u1", channels.setAllow)
			}
		}},
		{name: "whitelist remove", path: "/channel/whitelist_remove", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.removeAllow) != 1 || channels.removeAllow[0].UIDs[0] != "u1" {
				t.Fatalf("removeAllow = %#v, want u1", channels.removeAllow)
			}
		}},
		{name: "whitelist remove all", path: "/channel/whitelist_remove_all", body: `{"channel_id":"g1","channel_type":2}`, assert: func(t *testing.T, channels *recordingChannelUsecase) {
			if len(channels.removeAllAllow) != 1 || channels.removeAllAllow[0].ChannelID != "g1" {
				t.Fatalf("removeAllAllow = %#v, want g1", channels.removeAllAllow)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channels := &recordingChannelUsecase{}
			srv := New(Options{Channels: channels})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
			}
			tt.assert(t, channels)
		})
	}
}

func TestChannelRoutesReturnCompatibleValidationEnvelope(t *testing.T) {
	channels := &recordingChannelUsecase{}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/subscriber_add", bytes.NewBufferString(`{"channel_id":"","subscribers":["u1"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "{\"msg\":\"频道ID不能为空！\",\"status\":400}"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if len(channels.addSubscribers) != 0 {
		t.Fatalf("addSubscribers = %#v, want none", channels.addSubscribers)
	}
}

func TestChannelRoutePropagatesUsecaseErrorWithCompatibleEnvelope(t *testing.T) {
	channels := &recordingChannelUsecase{err: errors.New("store busy")}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/info", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), "{\"msg\":\"store busy\",\"status\":400}"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

type recordingChannelUsecase struct {
	upserts              []channelusecase.UpsertCommand
	updateInfos          []channelusecase.Info
	deletes              []channelusecase.ChannelKey
	addSubscribers       []channelusecase.SubscriberCommand
	removeSubscribers    []channelusecase.SubscriberCommand
	removeAllSubscribers []channelusecase.ChannelKey
	setTempSubscribers   []channelusecase.TempSubscriberCommand
	addDeny              []channelusecase.MemberCommand
	setDeny              []channelusecase.MemberCommand
	removeDeny           []channelusecase.MemberCommand
	removeAllDeny        []channelusecase.ChannelKey
	addAllow             []channelusecase.MemberCommand
	setAllow             []channelusecase.MemberCommand
	removeAllow          []channelusecase.MemberCommand
	removeAllAllow       []channelusecase.ChannelKey
	listAllow            []channelusecase.ChannelKey
	listAllowResult      channelusecase.MemberListResult
	err                  error
}

func (r *recordingChannelUsecase) Upsert(_ context.Context, cmd channelusecase.UpsertCommand) error {
	r.upserts = append(r.upserts, cmd)
	return r.err
}

func (r *recordingChannelUsecase) UpdateInfo(_ context.Context, info channelusecase.Info) error {
	r.updateInfos = append(r.updateInfos, info)
	return r.err
}

func (r *recordingChannelUsecase) Delete(_ context.Context, key channelusecase.ChannelKey) error {
	r.deletes = append(r.deletes, key)
	return r.err
}

func (r *recordingChannelUsecase) AddSubscribers(_ context.Context, cmd channelusecase.SubscriberCommand) error {
	r.addSubscribers = append(r.addSubscribers, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveSubscribers(_ context.Context, cmd channelusecase.SubscriberCommand) error {
	r.removeSubscribers = append(r.removeSubscribers, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveAllSubscribers(_ context.Context, key channelusecase.ChannelKey) error {
	r.removeAllSubscribers = append(r.removeAllSubscribers, key)
	return r.err
}

func (r *recordingChannelUsecase) SetTempSubscribers(_ context.Context, cmd channelusecase.TempSubscriberCommand) error {
	r.setTempSubscribers = append(r.setTempSubscribers, cmd)
	return r.err
}

func (r *recordingChannelUsecase) AddDenylist(_ context.Context, cmd channelusecase.MemberCommand) error {
	r.addDeny = append(r.addDeny, cmd)
	return r.err
}

func (r *recordingChannelUsecase) SetDenylist(_ context.Context, cmd channelusecase.MemberCommand) error {
	r.setDeny = append(r.setDeny, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveDenylist(_ context.Context, cmd channelusecase.MemberCommand) error {
	r.removeDeny = append(r.removeDeny, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveAllDenylist(_ context.Context, key channelusecase.ChannelKey) error {
	r.removeAllDeny = append(r.removeAllDeny, key)
	return r.err
}

func (r *recordingChannelUsecase) AddAllowlist(_ context.Context, cmd channelusecase.MemberCommand) error {
	r.addAllow = append(r.addAllow, cmd)
	return r.err
}

func (r *recordingChannelUsecase) SetAllowlist(_ context.Context, cmd channelusecase.MemberCommand) error {
	r.setAllow = append(r.setAllow, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveAllowlist(_ context.Context, cmd channelusecase.MemberCommand) error {
	r.removeAllow = append(r.removeAllow, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveAllAllowlist(_ context.Context, key channelusecase.ChannelKey) error {
	r.removeAllAllow = append(r.removeAllAllow, key)
	return r.err
}

func (r *recordingChannelUsecase) ListAllowlist(_ context.Context, key channelusecase.ChannelKey) (channelusecase.MemberListResult, error) {
	r.listAllow = append(r.listAllow, key)
	return r.listAllowResult, r.err
}

func equalStrings(a, b []string) bool {
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
