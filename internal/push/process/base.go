package process

import (
	"github.com/WuKongIM/WuKongIM/internal/common"
	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type Push struct {
	wklog.Log
	processPool *ants.Pool
	client      *ingress.Client
	commService *common.Service
}

func New() *Push {
	d := &Push{
		Log:         wklog.NewWKLog("processPush"),
		client:      ingress.NewClient(),
		commService: common.NewService(),
	}
	var err error
	d.processPool, err = ants.NewPool(options.G.GoPool.ChannelProcess, ants.WithNonblocking(true), ants.WithPanicHandler(func(i interface{}) {
		d.Panic("push process pool is panic", zap.Any("err", err), zap.Stack("stack"))
	}))
	if err != nil {
		d.Panic("new push process pool failed", zap.Error(err))
	}
	return d
}
