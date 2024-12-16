package process

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (c *Channel) routes() {
	service.Cluster.Route("/wk/allowSend", c.handleAllowSend)
}

func (c *Channel) handleAllowSend(ctx *wkserver.Context) {
	req := &allowSendReq{}
	err := req.decode(ctx.Body())
	if err != nil {
		c.Error("handleAllowSend Unmarshal err", zap.Error(err))
		ctx.WriteErr(err)
		return
	}

	reasonCode, err := c.allowSend(req.From, req.To)
	if err != nil {
		c.Error("handleAllowSend: allowSend failed", zap.Error(err))
		ctx.WriteErr(err)
		return
	}

	if reasonCode == wkproto.ReasonSuccess {
		ctx.WriteOk()
		return
	}
	ctx.WriteErrorAndStatus(errors.New("not allow send"), proto.Status(reasonCode))
}
