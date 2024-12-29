package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
)

type tag struct {
	wklog.Log
	s *Server
}

// newTag tag相关api
func newTag(s *Server) *tag {
	return &tag{
		s:   s,
		Log: wklog.NewWKLog("tag"),
	}
}

func (t *tag) route(r *wkhttp.WKHttp) {

	r.GET("/tag", t.get)            // 获取tag
	r.POST("/tag/remove", t.remove) // 删除tag
}

func (t *tag) get(c *wkhttp.Context) {
	tagKey := c.Query("tag_key")
	channelId := c.Query("channel_id")
	channelType := wkutil.ParseUint8(c.Query("channel_type"))

	if strings.TrimSpace(tagKey) == "" {
		tagKey = service.TagManager.GetChannelTag(channelId, channelType)
	}

	var tag *types.Tag
	if strings.TrimSpace(tagKey) != "" {
		tag = service.TagManager.Get(strings.TrimSpace(tagKey))
	}

	if tag == nil {
		c.ResponseError(errors.New("tag not found"))
		return
	}

	c.JSON(http.StatusOK, tag)
}

func (t *tag) remove(c *wkhttp.Context) {
	var req struct {
		TagKey      string `json:"tag_key"`
		ChannelId   string `json:"channel_id"`
		ChannelType uint8  `json:"channel_type"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(err)
		return
	}

	tagKey := req.TagKey
	if strings.TrimSpace(tagKey) == "" {
		tagKey = service.TagManager.GetChannelTag(req.ChannelId, req.ChannelType)
	}

	if tagKey != "" {
		service.TagManager.RemoveTag(tagKey)
	}
	c.ResponseOK()
}
