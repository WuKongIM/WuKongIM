package server

import (
	"net/http"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"go.uber.org/zap"
)

type MigrateAPI struct {
	s *Server
	wklog.Log
}

func NewMigrateAPI(s *Server) *MigrateAPI {
	return &MigrateAPI{
		s:   s,
		Log: s.Log,
	}
}

// Route route
func (m *MigrateAPI) Route(r *wkhttp.WKHttp) {

	r.POST("/migrate/allusers", m.allUsers)       // 返回所有用户
	r.POST("/migrate/allchannels", m.allChannels) // 返回所有频道
	r.POST("/migrate/channel", m.channel)         // 返回单个频道
	r.POST("/migrate/topics", m.topicInSlot)      // 获取指定slot下的所有topic
}

func (m *MigrateAPI) allUsers(c *wkhttp.Context) {
	users, err := m.s.store.GetAllUsers()
	if err != nil {
		m.Error("get all users err", zap.Error(err))
		c.ResponseError(err)
		return
	}

	resps := make([]*User, 0, len(users))

	for _, u := range users {
		resps = append(resps, newUser(u))
	}

	c.JSON(http.StatusOK, resps)
}

func (m *MigrateAPI) allChannels(c *wkhttp.Context) {
	channels, err := m.s.store.GetAllChannels()
	if err != nil {
		m.Error("get all channels err", zap.Error(err))
		c.ResponseError(err)
		return
	}

	resps := make([]*ChannelInfo, 0, len(channels))

	for _, ch := range channels {
		resps = append(resps, newChannelInfo(ch))
	}

	c.JSON(http.StatusOK, resps)
}

func (m *MigrateAPI) channel(c *wkhttp.Context) {
	var req struct {
		ChannelID   string `json:"channel_id"`
		ChannelType uint8  `json:"channel_type"`
	}

	if err := c.BindJSON(&req); err != nil {
		m.Error("bind json err", zap.Error(err))
		c.ResponseError(err)
		return
	}

	subscribers, allowlist, denylist, err := m.s.store.GetSubscribersAndAllowlistAndDenylist(req.ChannelID, req.ChannelType)
	if err != nil {
		m.Error("get channel err", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, map[string]interface{}{
		"subscribers": subscribers,
		"allowlist":   allowlist,
		"denylist":    denylist,
	})
}

func (m *MigrateAPI) topicInSlot(c *wkhttp.Context) {
	var req struct {
		Slot uint32 `json:"slot"`
	}

	if err := c.BindJSON(&req); err != nil {
		m.Error("bind json err", zap.Error(err))
		c.ResponseError(err)
		return
	}

	topics, err := m.s.store.GetAllTopicWithSlotId(req.Slot)
	if err != nil {
		m.Error("get topics in slot err", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, topics)
}

type User struct {
	Uid         string `json:"uid"`
	Token       string `json:"token"`
	DeviceFlag  uint8  `json:"device_flag"`
	DeviceLevel uint8  `json:"device_level"`
}

func newUser(u wkstore.User) *User {
	return &User{
		Uid:         u.Uid,
		Token:       u.Token,
		DeviceFlag:  u.DeviceFlag,
		DeviceLevel: u.DeviceLevel,
	}
}

type ChannelInfo struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	Ban         bool   `json:"ban"`     // 是否被封
	Large       bool   `json:"large"`   // 是否是超大群
	Disband     bool   `json:"disband"` // 是否解散
}

func newChannelInfo(ch *wkstore.ChannelInfo) *ChannelInfo {
	return &ChannelInfo{
		ChannelID:   ch.ChannelID,
		ChannelType: ch.ChannelType,
		Ban:         ch.Ban,
		Large:       ch.Large,
		Disband:     ch.Disband,
	}
}
