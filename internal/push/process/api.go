package process

import (
	"errors"

	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"go.uber.org/zap"
)

func (p *Push) routes() {
	service.Cluster.Route("/wk/getTag", p.handleGetTag)
}

func (p *Push) handleGetTag(c *wkserver.Context) {
	req := &tagReq{}
	err := req.decode(c.Body())
	if err != nil {
		p.Error("getTag decode err", zap.Error(err))
		c.WriteErr(err)
		return
	}

	if req.nodeId == 0 {
		c.WriteErr(errors.New("node is 0"))
		return
	}

	if req.tagKey == "" {
		c.WriteErr(errors.New("tagKey is nil"))
		return
	}

	tag := service.TagMananger.Get(req.tagKey)
	if tag == nil {
		p.Error("handleGetTag: tag not exist", zap.Error(err))
		c.WriteErr(errors.New("handleGetTag: tag not exist"))
		return
	}
	var resp = &tagResp{
		tagKey: tag.Key,
		uids:   tag.GetNodeUsers(req.nodeId),
	}
	data, err := resp.encode()
	if err != nil {
		p.Error("tagResp encode failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}
