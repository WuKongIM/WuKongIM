package service

import (
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

var CommonService ICommonService

type ICommonService interface {
	Schedule(interval time.Duration, f func()) *timingwheel.Timer
	AfterFunc(d time.Duration, f func())
}

// 判断单聊是否允许发送消息
func AllowSendForPerson(from, to string) (wkproto.ReasonCode, error) {
	// 判断是否是黑名单内
	isDenylist, err := Store.ExistDenylist(to, wkproto.ChannelTypePerson, from)
	if err != nil {
		wklog.Error("ExistDenylist error", zap.String("from", from), zap.String("to", to), zap.Error(err))
		return wkproto.ReasonSystemError, err
	}
	if isDenylist {
		return wkproto.ReasonInBlacklist, nil
	}

	if !options.G.WhitelistOffOfPerson {
		// 判断是否在白名单内
		isAllowlist, err := Store.ExistAllowlist(to, wkproto.ChannelTypePerson, from)
		if err != nil {
			wklog.Error("ExistAllowlist error", zap.Error(err))
			return wkproto.ReasonSystemError, err
		}
		if !isAllowlist {
			return wkproto.ReasonNotInWhitelist, nil
		}
	}

	return wkproto.ReasonSuccess, nil
}
