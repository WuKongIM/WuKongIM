package process

import (
	"github.com/WuKongIM/WuKongIM/internal/common"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/panjf2000/ants/v2"
	"go.uber.org/zap"
)

type Diffuse struct {
	wklog.Log
	processPool   *ants.Pool
	commonService *common.Service
}

func New() *Diffuse {
	d := &Diffuse{
		Log:           wklog.NewWKLog("processDiffuse"),
		commonService: common.NewService(),
	}
	var err error
	d.processPool, err = ants.NewPool(options.G.GoPool.ChannelProcess, ants.WithPanicHandler(func(i interface{}) {
		d.Panic("diffuse process pool is panic", zap.Any("err", err), zap.Stack("stack"))
	}))
	if err != nil {
		d.Panic("new diffuse process pool failed", zap.Error(err))
	}
	return d
}
