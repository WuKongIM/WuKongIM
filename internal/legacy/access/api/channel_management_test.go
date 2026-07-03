package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	channelusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestChannelCreateMapsLegacyRequestToUsecaseCommand(t *testing.T) {
	channels := &recordingChannelUsecase{}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2,"large":1,"ban":1,"disband":1,"send_ban":1,"allow_stranger":1,"reset":1,"subscribers":["u1","u2"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Equal(t, []channelusecase.UpsertCommand{{
		Info: channelusecase.Info{
			ChannelID:     "g1",
			ChannelType:   frame.ChannelTypeGroup,
			Large:         true,
			Ban:           true,
			Disband:       true,
			SendBan:       true,
			AllowStranger: true,
		},
		Reset:       true,
		Subscribers: []string{"u1", "u2"},
	}}, channels.upserts)
}

func TestChannelSubscriberAddDefaultsLegacyGroupType(t *testing.T) {
	channels := &recordingChannelUsecase{}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/subscriber_add", bytes.NewBufferString(`{"channel_id":"g1","subscribers":["u1"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"status":200}`, rec.Body.String())
	require.Equal(t, []channelusecase.SubscriberCommand{{
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Reset:       false,
		Subscribers: []string{"u1"},
	}}, channels.addSubscribers)
}

func TestChannelWhitelistGetReturnsLegacyMemberArray(t *testing.T) {
	channels := &recordingChannelUsecase{
		listAllowResult: channelusecase.MemberListResult{
			Members: []channelusecase.Member{{UID: "u1"}, {ID: 7, UID: "u2"}},
		},
	}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/channel/whitelist?channel_id=g1&channel_type=2", nil)

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"id":0,"uid":"u1"},{"id":7,"uid":"u2"}]`, rec.Body.String())
	require.Equal(t, []channelusecase.ChannelKey{{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}}, channels.listAllow)
}

func TestChannelLegacyMutationRoutesMapToUsecase(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		body   string
		assert func(t *testing.T, channels *recordingChannelUsecase)
	}{
		{
			name: "info",
			path: "/channel/info",
			body: `{"channel_id":"g1","channel_type":2,"ban":1}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.Info{{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Ban: true}}, channels.updateInfos)
			},
		},
		{
			name: "delete",
			path: "/channel/delete",
			body: `{"channel_id":"g1","channel_type":2}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.ChannelKey{{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}}, channels.deletes)
			},
		},
		{
			name: "subscriber remove",
			path: "/channel/subscriber_remove",
			body: `{"channel_id":"g1","channel_type":2,"subscribers":["u1"]}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.SubscriberCommand{{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Subscribers: []string{"u1"}}}, channels.removeSubscribers)
			},
		},
		{
			name: "subscriber remove all",
			path: "/channel/subscriber_remove_all",
			body: `{"channel_id":"g1","channel_type":2}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.ChannelKey{{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}}, channels.removeAllSubscribers)
			},
		},
		{
			name: "temporary subscriber set",
			path: "/tmpchannel/subscriber_set",
			body: `{"channel_id":"t1","uids":["u1"]}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.TempSubscriberCommand{{ChannelID: "t1", UIDs: []string{"u1"}}}, channels.setTempSubscribers)
			},
		},
		{
			name: "blacklist add",
			path: "/channel/blacklist_add",
			body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.MemberCommand{{ChannelKey: channelusecase.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, UIDs: []string{"u1"}}}, channels.addDeny)
			},
		},
		{
			name: "blacklist set",
			path: "/channel/blacklist_set",
			body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.MemberCommand{{ChannelKey: channelusecase.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, UIDs: []string{"u1"}}}, channels.setDeny)
			},
		},
		{
			name: "blacklist remove",
			path: "/channel/blacklist_remove",
			body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.MemberCommand{{ChannelKey: channelusecase.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, UIDs: []string{"u1"}}}, channels.removeDeny)
			},
		},
		{
			name: "blacklist remove all",
			path: "/channel/blacklist_remove_all",
			body: `{"channel_id":"g1","channel_type":2}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.ChannelKey{{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}}, channels.removeAllDeny)
			},
		},
		{
			name: "whitelist add",
			path: "/channel/whitelist_add",
			body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.MemberCommand{{ChannelKey: channelusecase.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, UIDs: []string{"u1"}}}, channels.addAllow)
			},
		},
		{
			name: "whitelist set",
			path: "/channel/whitelist_set",
			body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.MemberCommand{{ChannelKey: channelusecase.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, UIDs: []string{"u1"}}}, channels.setAllow)
			},
		},
		{
			name: "whitelist remove",
			path: "/channel/whitelist_remove",
			body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.MemberCommand{{ChannelKey: channelusecase.ChannelKey{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}, UIDs: []string{"u1"}}}, channels.removeAllow)
			},
		},
		{
			name: "whitelist remove all",
			path: "/channel/whitelist_remove_all",
			body: `{"channel_id":"g1","channel_type":2}`,
			assert: func(t *testing.T, channels *recordingChannelUsecase) {
				require.Equal(t, []channelusecase.ChannelKey{{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup}}, channels.removeAllAllow)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channels := &recordingChannelUsecase{}
			srv := New(Options{Channels: channels})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			srv.Engine().ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			require.JSONEq(t, `{"status":200}`, rec.Body.String())
			tt.assert(t, channels)
		})
	}
}

func TestChannelRoutesReturnLegacyValidationEnvelope(t *testing.T) {
	channels := &recordingChannelUsecase{}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/subscriber_add", bytes.NewBufferString(`{"channel_id":"","subscribers":["u1"]}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"频道ID不能为空！","status":400}`, rec.Body.String())
	require.Empty(t, channels.addSubscribers)
}

func TestChannelRoutePropagatesUsecaseErrorWithLegacyEnvelope(t *testing.T) {
	channels := &recordingChannelUsecase{err: errors.New("store busy")}
	srv := New(Options{Channels: channels})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/channel/info", bytes.NewBufferString(`{"channel_id":"g1","channel_type":2}`))
	req.Header.Set("Content-Type", "application/json")

	srv.Engine().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"msg":"store busy","status":400}`, rec.Body.String())
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

func (r *recordingChannelUsecase) Upsert(ctx context.Context, cmd channelusecase.UpsertCommand) error {
	r.upserts = append(r.upserts, cmd)
	return r.err
}

func (r *recordingChannelUsecase) UpdateInfo(ctx context.Context, info channelusecase.Info) error {
	r.updateInfos = append(r.updateInfos, info)
	return r.err
}

func (r *recordingChannelUsecase) Delete(ctx context.Context, key channelusecase.ChannelKey) error {
	r.deletes = append(r.deletes, key)
	return r.err
}

func (r *recordingChannelUsecase) AddSubscribers(ctx context.Context, cmd channelusecase.SubscriberCommand) error {
	r.addSubscribers = append(r.addSubscribers, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveSubscribers(ctx context.Context, cmd channelusecase.SubscriberCommand) error {
	r.removeSubscribers = append(r.removeSubscribers, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveAllSubscribers(ctx context.Context, key channelusecase.ChannelKey) error {
	r.removeAllSubscribers = append(r.removeAllSubscribers, key)
	return r.err
}

func (r *recordingChannelUsecase) SetTempSubscribers(ctx context.Context, cmd channelusecase.TempSubscriberCommand) error {
	r.setTempSubscribers = append(r.setTempSubscribers, cmd)
	return r.err
}

func (r *recordingChannelUsecase) AddDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error {
	r.addDeny = append(r.addDeny, cmd)
	return r.err
}

func (r *recordingChannelUsecase) SetDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error {
	r.setDeny = append(r.setDeny, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error {
	r.removeDeny = append(r.removeDeny, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveAllDenylist(ctx context.Context, key channelusecase.ChannelKey) error {
	r.removeAllDeny = append(r.removeAllDeny, key)
	return r.err
}

func (r *recordingChannelUsecase) AddAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error {
	r.addAllow = append(r.addAllow, cmd)
	return r.err
}

func (r *recordingChannelUsecase) SetAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error {
	r.setAllow = append(r.setAllow, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error {
	r.removeAllow = append(r.removeAllow, cmd)
	return r.err
}

func (r *recordingChannelUsecase) RemoveAllAllowlist(ctx context.Context, key channelusecase.ChannelKey) error {
	r.removeAllAllow = append(r.removeAllAllow, key)
	return r.err
}

func (r *recordingChannelUsecase) ListAllowlist(ctx context.Context, key channelusecase.ChannelKey) (channelusecase.MemberListResult, error) {
	r.listAllow = append(r.listAllow, key)
	return r.listAllowResult, r.err
}
