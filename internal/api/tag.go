package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
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
	r.GET("/tags", t.list)          // 获取tag列表
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
		NodeId      uint64 `json:"node_id"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		t.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if req.NodeId != 0 && !options.G.IsLocalNode(req.NodeId) {
		nodeInfo := service.Cluster.NodeInfoById(req.NodeId)
		if nodeInfo == nil {
			c.ResponseError(errors.New("node not found"))
			return
		}
		c.ForwardWithBody(fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
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

func (t *tag) list(c *wkhttp.Context) {

	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	channelId := c.Query("channel_id")
	channelType := wkutil.ParseUint8(c.Query("channel_type"))
	tagKey := c.Query("tag_key")

	if nodeId != 0 && !options.G.IsLocalNode(nodeId) {
		nodeInfo := service.Cluster.NodeInfoById(nodeId)
		if nodeInfo == nil {
			c.ResponseError(errors.New("node not found"))
			return
		}
		c.ForwardWithBody(fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path), nil)
		return
	}

	if channelId != "" && channelType != 0 {
		tagResps := make([]*tagResp, 0)
		tagKey := service.TagManager.GetChannelTag(channelId, channelType)
		if tagKey != "" {
			tag := service.TagManager.Get(tagKey)
			if tag != nil {
				tagResps = append(tagResps, newTagResp(tag))
			}
		}
		c.JSON(http.StatusOK, tagResps)
		return
	}

	if tagKey != "" {
		tagResps := make([]*tagResp, 0)
		tag := service.TagManager.Get(tagKey)
		if tag != nil {
			tagResps = append(tagResps, newTagResp(tag))
		}
		c.JSON(http.StatusOK, tagResps)
		return
	}

	tags := service.TagManager.GetAllTags()

	sort.Slice(tags, func(i, j int) bool {
		return tags[i].CreatedAt.After(tags[j].CreatedAt)
	})

	maxSize := 1000
	if len(tags) > maxSize {
		tags = tags[:maxSize]
	}

	tagResps := make([]*tagResp, 0, len(tags))
	for _, tag := range tags {
		tagResp := newTagResp(tag)
		tagResps = append(tagResps, tagResp)
	}

	c.JSON(http.StatusOK, tagResps)
}

type tagResp struct {
	Key         string         `json:"key"`
	ChannelId   string         `json:"channel_id"`
	ChannelType uint8          `json:"channel_type"`
	Nodes       []*nodeTagResp `json:"nodes"`
	// 创建时间
	CreatedAt string `json:"created_at"`
	// 过期时间
	ExpireAt string `json:"expire_at"`
	// 最后获取时间
	LastGetAt   string `json:"last_get_at"`
	NodeVersion uint64 `json:"node_version"`
	// 获取次数
	GetCount uint64 `json:"get_count"`
}

func newTagResp(tag *types.Tag) *tagResp {
	nodes := make([]*nodeTagResp, 0, len(tag.Nodes))
	for _, node := range tag.Nodes {
		nodes = append(nodes, &nodeTagResp{
			LeaderId: node.LeaderId,
			Uids:     node.Uids,
			SlotIds:  node.SlotIds,
			UidCount: len(node.Uids),
		})
	}
	var createdAtFormat string
	if !tag.CreatedAt.IsZero() {
		createdAtFormat = wkutil.ToyyyyMMddHHmm(tag.CreatedAt)
	}

	var expireAt string
	if !tag.LastGetTime.IsZero() {
		expireAt = wkutil.ToyyyyMMddHHmm(tag.LastGetTime.Add(options.G.Tag.Expire))
	}
	var lastGetAt string
	if !tag.LastGetTime.IsZero() {
		lastGetAt = wkutil.ToyyyyMMddHHmm(tag.LastGetTime)
	}

	return &tagResp{
		Key:         tag.Key,
		ChannelId:   tag.ChannelId,
		ChannelType: tag.ChannelType,
		Nodes:       nodes,
		CreatedAt:   createdAtFormat,
		ExpireAt:    expireAt,
		LastGetAt:   lastGetAt,
		NodeVersion: tag.NodeVersion,
		GetCount:    tag.GetCount.Load(),
	}
}

type nodeTagResp struct {
	// 节点id
	LeaderId uint64 `json:"leader_id"`
	UidCount int    `json:"uid_count"`
	// 节点id对应的用户集合
	Uids []string `json:"uids"`
	// 用户涉及到的slot
	SlotIds []uint32 `json:"slot_ids"`
}
