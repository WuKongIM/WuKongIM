package api

import (
	"net/http"
	"testing"
)

func TestChannelRoutesFailClosedWhenUsecaseIsNotWired(t *testing.T) {
	srv := New(Options{})
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "upsert", method: http.MethodPost, path: "/channel", body: `{"channel_id":"g1","channel_type":2}`},
		{name: "info", method: http.MethodPost, path: "/channel/info", body: `{"channel_id":"g1","channel_type":2}`},
		{name: "delete", method: http.MethodPost, path: "/channel/delete", body: `{"channel_id":"g1","channel_type":2}`},
		{name: "subscriber add", method: http.MethodPost, path: "/channel/subscriber_add", body: `{"channel_id":"g1","channel_type":2,"subscribers":["u1"]}`},
		{name: "subscriber remove", method: http.MethodPost, path: "/channel/subscriber_remove", body: `{"channel_id":"g1","channel_type":2,"subscribers":["u1"]}`},
		{name: "subscriber remove all", method: http.MethodPost, path: "/channel/subscriber_remove_all", body: `{"channel_id":"g1","channel_type":2}`},
		{name: "temporary subscriber set", method: http.MethodPost, path: "/tmpchannel/subscriber_set", body: `{"channel_id":"g1","uids":["u1"]}`},
		{name: "denylist add", method: http.MethodPost, path: "/channel/blacklist_add", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`},
		{name: "denylist set", method: http.MethodPost, path: "/channel/blacklist_set", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`},
		{name: "denylist remove", method: http.MethodPost, path: "/channel/blacklist_remove", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`},
		{name: "denylist remove all", method: http.MethodPost, path: "/channel/blacklist_remove_all", body: `{"channel_id":"g1","channel_type":2}`},
		{name: "allowlist add", method: http.MethodPost, path: "/channel/whitelist_add", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`},
		{name: "allowlist set", method: http.MethodPost, path: "/channel/whitelist_set", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`},
		{name: "allowlist remove", method: http.MethodPost, path: "/channel/whitelist_remove", body: `{"channel_id":"g1","channel_type":2,"uids":["u1"]}`},
		{name: "allowlist remove all", method: http.MethodPost, path: "/channel/whitelist_remove_all", body: `{"channel_id":"g1","channel_type":2}`},
		{name: "allowlist list", method: http.MethodGet, path: "/channel/whitelist?channel_id=g1&channel_type=2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveAPIRequest(t, srv, tt.method, tt.path, tt.body)
			if rec.Code != http.StatusBadRequest || !jsonEqual(rec.Body.String(), `{"msg":"channel usecase not configured","status":400}`) {
				t.Fatalf("status = %d body = %s, want explicit unwired-usecase failure", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestChannelRoutesRejectInvalidLegacyRequestsBeforeMutation(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		want string
	}{
		{name: "malformed json", path: "/channel", body: `{"channel_id":`, want: "数据格式有误！"},
		{name: "upsert missing id", path: "/channel", body: `{"channel_type":2}`, want: "频道ID不能为空！"},
		{name: "upsert missing type", path: "/channel", body: `{"channel_id":"g1"}`, want: "频道类型错误！"},
		{name: "upsert special id", path: "/channel", body: `{"channel_id":"g#1","channel_type":2}`, want: "频道ID不能包含特殊字符！"},
		{name: "person upsert subscribers", path: "/channel", body: `{"channel_id":"u2","channel_type":1,"subscribers":["u1"]}`, want: "不支持个人频道添加订阅者！"},
		{name: "person delete", path: "/channel/delete", body: `{"channel_id":"u1@u2","channel_type":1}`, want: "个人频道不支持添加订阅者！"},
		{name: "subscriber special id", path: "/channel/subscriber_add", body: `{"channel_id":"g@1","channel_type":2,"subscribers":["u1"]}`, want: "频道ID不能包含特殊字符！"},
		{name: "subscriber empty list", path: "/channel/subscriber_add", body: `{"channel_id":"g1","channel_type":2,"subscribers":[]}`, want: "订阅者不能为空！"},
		{name: "subscriber blank uid", path: "/channel/subscriber_add", body: `{"channel_id":"g1","channel_type":2,"subscribers":[" "]}`, want: "订阅者不能为空！"},
		{name: "person subscriber add", path: "/channel/subscriber_add", body: `{"channel_id":"u2","channel_type":1,"subscribers":["u1"]}`, want: "个人频道不支持添加订阅者！"},
		{name: "temporary subscriber mode removed", path: "/channel/subscriber_add", body: `{"channel_id":"g1","channel_type":2,"temp_subscriber":1,"subscribers":["u1"]}`, want: "新版本临时订阅者已不支持！"},
		{name: "person subscriber remove", path: "/channel/subscriber_remove", body: `{"channel_id":"u2","channel_type":1,"subscribers":["u1"]}`, want: "个人频道不支持添加订阅者！"},
		{name: "remove all missing id", path: "/channel/subscriber_remove_all", body: `{"channel_type":2}`, want: "channel_id不能为空！"},
		{name: "remove all missing type", path: "/channel/subscriber_remove_all", body: `{"channel_id":"g1"}`, want: "频道类型不能为0！"},
		{name: "person remove all", path: "/channel/subscriber_remove_all", body: `{"channel_id":"u1@u2","channel_type":1}`, want: "个人频道不支持此操作！"},
		{name: "temporary missing id", path: "/tmpchannel/subscriber_set", body: `{"uids":["u1"]}`, want: "channel_id不能为空！"},
		{name: "temporary special id", path: "/tmpchannel/subscriber_set", body: `{"channel_id":"g#1","uids":["u1"]}`, want: "频道ID不能包含特殊字符！"},
		{name: "temporary missing uids", path: "/tmpchannel/subscriber_set", body: `{"channel_id":"g1"}`, want: "uids不能为空！"},
		{name: "denylist add missing id", path: "/channel/blacklist_add", body: `{"channel_type":2,"uids":["u1"]}`, want: "channel_id不能为空！"},
		{name: "denylist add missing type", path: "/channel/blacklist_add", body: `{"channel_id":"g1","uids":["u1"]}`, want: "频道类型不能为0！"},
		{name: "denylist add missing uids", path: "/channel/blacklist_add", body: `{"channel_id":"g1","channel_type":2}`, want: "uids不能为空！"},
		{name: "denylist remove missing uids", path: "/channel/blacklist_remove", body: `{"channel_id":"g1","channel_type":2}`, want: "uids不能为空！"},
		{name: "denylist set missing id", path: "/channel/blacklist_set", body: `{"channel_type":2,"uids":["u1"]}`, want: "频道ID不能为空！"},
		{name: "allowlist missing id", path: "/channel/whitelist_add", body: `{"channel_type":2,"uids":["u1"]}`, want: "channel_id不能为空！"},
		{name: "allowlist special id", path: "/channel/whitelist_add", body: `{"channel_id":"g#1","channel_type":2,"uids":["u1"]}`, want: "频道ID不能包含特殊字符！"},
		{name: "allowlist missing type", path: "/channel/whitelist_add", body: `{"channel_id":"g1","uids":["u1"]}`, want: "频道类型不能为0！"},
		{name: "allowlist blank uid", path: "/channel/whitelist_add", body: `{"channel_id":"g1","channel_type":2,"uids":[""]}`, want: "uids不能为空！"},
		{name: "denylist remove all missing id", path: "/channel/blacklist_remove_all", body: `{"channel_type":2}`, want: "channel_id不能为空！"},
		{name: "allowlist remove all missing type", path: "/channel/whitelist_remove_all", body: `{"channel_id":"g1"}`, want: "频道类型不能为0！"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channels := &recordingChannelUsecase{}
			srv := New(Options{Channels: channels})
			rec := serveAPIRequest(t, srv, http.MethodPost, tt.path, tt.body)
			if rec.Code != http.StatusBadRequest || !jsonEqual(rec.Body.String(), `{"msg":"`+tt.want+`","status":400}`) {
				t.Fatalf("status = %d body = %s, want 400 %q", rec.Code, rec.Body.String(), tt.want)
			}
			if got := channelMutationCallCount(channels); got != 0 {
				t.Fatalf("usecase mutation calls = %d, want validation to reject before mutation", got)
			}
		})
	}
}

func channelMutationCallCount(channels *recordingChannelUsecase) int {
	return len(channels.upserts) + len(channels.updateInfos) + len(channels.deletes) +
		len(channels.addSubscribers) + len(channels.removeSubscribers) + len(channels.removeAllSubscribers) +
		len(channels.setTempSubscribers) + len(channels.addDeny) + len(channels.setDeny) +
		len(channels.removeDeny) + len(channels.removeAllDeny) + len(channels.addAllow) +
		len(channels.setAllow) + len(channels.removeAllow) + len(channels.removeAllAllow)
}
