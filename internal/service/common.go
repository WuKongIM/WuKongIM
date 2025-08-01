package service

import (
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
)

var CommonService ICommonService

type ICommonService interface {
	Schedule(interval time.Duration, f func()) *timingwheel.Timer
	AfterFunc(d time.Duration, f func())

	// 根据消息id获取流数据
	GetStreamsForLocal(messageIds []int64) ([]*wkdb.StreamV2, error)
}
