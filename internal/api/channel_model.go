package api

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/pkg/errors"
)

// ChannelInfoReq ChannelInfoReq
type channelInfoReq struct {
	ChannelID   string `json:"channel_id"`   // 频道ID
	ChannelType uint8  `json:"channel_type"` // 频道类型
	Large       int    `json:"large"`        // 是否是超大群
	Ban         int    `json:"ban"`          // 是否封禁频道（封禁后此频道所有人都将不能发消息，除了系统账号）
	Disband     int    `json:"disband"`      // 是否解散频道
}

func (c channelInfoReq) ToChannelInfo() wkdb.ChannelInfo {
	createdAt := time.Now()
	updatedAt := time.Now()
	return wkdb.ChannelInfo{
		ChannelId:   c.ChannelID,
		ChannelType: c.ChannelType,
		Large:       c.Large == 1,
		Ban:         c.Ban == 1,
		Disband:     c.Disband == 1,
		CreatedAt:   &createdAt,
		UpdatedAt:   &updatedAt,
	}
}

// ChannelCreateReq 频道创建请求
type channelCreateReq struct {
	channelInfoReq
	Reset       int      `json:"reset"`       // 是否重置订阅者 （0.不重置 1.重置），选择重置，将删除原来的所有成员
	Subscribers []string `json:"subscribers"` // 订阅者
}

// Check 检查请求参数
func (r channelCreateReq) Check() error {
	if strings.TrimSpace(r.ChannelID) == "" {
		return errors.New("频道ID不能为空！")
	}
	if r.ChannelType == 0 {
		return errors.New("频道类型错误！")
	}
	if options.IsSpecialChar(r.ChannelID) {
		return errors.New("频道ID不能包含特殊字符！")
	}
	return nil
}

type subscriberAddReq struct {
	ChannelId      string   `json:"channel_id"`      // 频道ID
	ChannelType    uint8    `json:"channel_type"`    // 频道类型
	Reset          int      `json:"reset"`           // 是否重置订阅者 （0.不重置 1.重置），选择重置，将删除原来的所有成员
	TempSubscriber int      `json:"temp_subscriber"` //  是否是临时订阅者 (1. 是 0. 否)
	Subscribers    []string `json:"subscribers"`     // 订阅者
}

func (s subscriberAddReq) Check() error {
	if strings.TrimSpace(s.ChannelId) == "" {
		return errors.New("频道ID不能为空！")
	}
	if options.IsSpecialChar(s.ChannelId) {
		return errors.New("频道ID不能包含特殊字符！")
	}
	if stringArrayIsEmpty(s.Subscribers) {
		return errors.New("订阅者不能为空！")
	}
	return nil
}

type subscriberRemoveReq struct {
	ChannelId      string   `json:"channel_id"`
	ChannelType    uint8    `json:"channel_type"`
	TempSubscriber int      `json:"temp_subscriber"` //  是否是临时订阅者 (1. 是 0. 否)
	Subscribers    []string `json:"subscribers"`
}

func (s subscriberRemoveReq) Check() error {
	if strings.TrimSpace(s.ChannelId) == "" {
		return errors.New("频道ID不能为空！")
	}
	if options.IsSpecialChar(s.ChannelId) {
		return errors.New("频道ID不能包含特殊字符！")
	}
	if stringArrayIsEmpty(s.Subscribers) {
		return errors.New("订阅者不能为空！")
	}
	return nil
}

func stringArrayIsEmpty(array []string) bool {
	if len(array) == 0 {
		return true
	}
	emptyCount := 0
	for _, a := range array {
		if strings.TrimSpace(a) == "" {
			emptyCount++
		}
	}
	return emptyCount >= len(array)
}

type tmpSubscriberSetReq struct {
	ChannelId string   `json:"channel_id"` // 频道ID
	Uids      []string `json:"uids"`       // 订阅者
}

func (r tmpSubscriberSetReq) Check() error {
	if r.ChannelId == "" {
		return errors.New("channel_id不能为空！")
	}
	if options.IsSpecialChar(r.ChannelId) {
		return errors.New("频道ID不能包含特殊字符！")
	}
	if len(r.Uids) <= 0 {
		return errors.New("uids不能为空！")
	}
	return nil
}

type blacklistReq struct {
	ChannelId   string   `json:"channel_id"`   // 频道ID
	ChannelType uint8    `json:"channel_type"` // 频道类型
	UIDs        []string `json:"uids"`         // 订阅者
}

func (r blacklistReq) Check() error {
	if r.ChannelId == "" {
		return errors.New("channel_id不能为空！")
	}
	if r.ChannelType == 0 {
		return errors.New("频道类型不能为0！")
	}
	if len(r.UIDs) <= 0 {
		return errors.New("uids不能为空！")
	}
	return nil
}

type channelDeleteReq struct {
	ChannelId   string `json:"channel_id"`   // 频道ID
	ChannelType uint8  `json:"channel_type"` // 频道类型
}

type whitelistReq struct {
	ChannelId   string   `json:"channel_id"`   // 频道ID
	ChannelType uint8    `json:"channel_type"` // 频道类型
	UIDs        []string `json:"uids"`         // 订阅者
}

func (r whitelistReq) Check() error {
	if r.ChannelId == "" {
		return errors.New("channel_id不能为空！")
	}
	if options.IsSpecialChar(r.ChannelId) {
		return errors.New("频道ID不能包含特殊字符！")
	}
	if r.ChannelType == 0 {
		return errors.New("频道类型不能为0！")
	}
	if stringArrayIsEmpty(r.UIDs) {
		return errors.New("uids不能为空！")
	}
	return nil
}

var emptySyncMessageResp = syncMessageResp{
	StartMessageSeq: 0,
	EndMessageSeq:   0,
	More:            0,
	Messages:        make([]*types.MessageResp, 0),
}

type syncMessageResp struct {
	StartMessageSeq uint64               `json:"start_message_seq"` // 开始序列号
	EndMessageSeq   uint64               `json:"end_message_seq"`   // 结束序列号
	More            int                  `json:"more"`              // 是否还有更多 1.是 0.否
	Messages        []*types.MessageResp `json:"messages"`          // 消息数据
}
