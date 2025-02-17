package plugin

import (
	"encoding/json"
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/wkrpc"
	"github.com/sendgrid/rest"
	"go.uber.org/zap"
)

func (a *rpc) conversationChannels(c *wkrpc.Context) {
	req := &pluginproto.ConversationChannelReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		a.Error("ConversationChannelReq unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.Uid, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		a.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", req.Uid), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.WriteErr(err)
		return
	}

	// 请求对应节点的用户最近会话频道接口
	forwardUrl := fmt.Sprintf("%s/conversation/channels", leaderInfo.ApiServerAddr)

	reqBodyMap := map[string]interface{}{
		"uid": req.Uid,
	}
	reqBody := wkutil.ToJSON(reqBodyMap)
	resp, err := a.post(forwardUrl, []byte(reqBody))
	if err != nil {
		a.Error("转发请求失败！", zap.Error(err))
		c.WriteErr(err)
		return
	}

	var respBodyMaps []map[string]interface{}
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &respBodyMaps)
	if err != nil {
		a.Error("解析返回数据失败！", zap.Error(err))
		c.WriteErr(err)
		return
	}

	channels := make([]*pluginproto.Channel, 0, len(respBodyMaps))
	for _, respBodyMap := range respBodyMaps {
		channelId, ok := respBodyMap["channel_id"]
		if !ok {
			continue
		}
		channelType, ok := respBodyMap["channel_type"]
		if !ok {
			continue
		}
		chType, _ := channelType.(json.Number).Int64()

		channel := &pluginproto.Channel{
			ChannelId:   channelId.(string),
			ChannelType: uint32(chType),
		}
		channels = append(channels, channel)
	}

	respBody := &pluginproto.ConversationChannelResp{
		Channels: channels,
	}

	respData, err := respBody.Marshal()
	if err != nil {
		a.Error("get conversationChannels failed, Marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	c.Write(respData)
}

// ForwardWithBody 转发请求
func (a *rpc) post(url string, body []byte) (*rest.Response, error) {

	req := rest.Request{
		Method:  rest.Post,
		BaseURL: url,
		Body:    body,
	}

	resp, err := rest.Send(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
