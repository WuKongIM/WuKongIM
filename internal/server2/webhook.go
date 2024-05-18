package server

import (
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type webhook struct {
	s *Server
	wklog.Log
}

func newWebhook(s *Server) *webhook {

	return &webhook{
		s:   s,
		Log: wklog.NewWKLog("Webhook"),
	}
}

func (w *webhook) Start() {
}

func (w *webhook) Stop() {
}

// Online 用户设备上线通知
func (w *webhook) Online(uid string, deviceFlag wkproto.DeviceFlag, connId int64, deviceOnlineCount int, totalOnlineCount int) {

}

func (w *webhook) notifyOfflineMsg(msg *ReactorChannelMessage, uids []string) {

}
