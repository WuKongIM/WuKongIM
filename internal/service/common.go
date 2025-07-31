package service

import (
	"time"

	"github.com/RussellLuo/timingwheel"
)

var CommonService ICommonService

type ICommonService interface {
	Schedule(interval time.Duration, f func()) *timingwheel.Timer
	AfterFunc(d time.Duration, f func())
}
