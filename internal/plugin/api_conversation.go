package plugin

import (
	"path"

	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/wkrpc"
	"github.com/sendgrid/rest"
	"go.uber.org/zap"
)

func (a *api) conversationChannels(c *wkrpc.Context) {
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
	forwardUrl := path.Join(leaderInfo.ApiServerAddr, "conversations", "channels")
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
	c.Write([]byte(resp.Body))
}

// ForwardWithBody 转发请求
func (a *api) post(url string, body []byte) (*rest.Response, error) {

	req := rest.Request{
		Method:  rest.Method("post"),
		BaseURL: url,
		Body:    body,
	}

	resp, err := rest.Send(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}
