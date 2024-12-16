package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type User struct {
	wklog.Log
	processPool *ants.Pool // 用户逻辑处理协程池
}

func New() *User {
	p := &User{
		Log: wklog.NewWKLog("processUser"),
	}

	var err error
	p.processPool, err = ants.NewPool(options.G.GoPool.UserProcess, ants.WithPanicHandler(func(i interface{}) {
		p.Panic("user process pool is panic", zap.Any("err", err), zap.Stack("stack"))
	}))
	if err != nil {
		p.Panic("new user process pool failed", zap.Error(err))
	}
	return p
}
