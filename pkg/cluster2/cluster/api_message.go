package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) messageSearch(c *wkhttp.Context) {

	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	fromUid := strings.TrimSpace(c.Query("from_uid"))
	channelId := strings.TrimSpace(c.Query("channel_id"))
	channelType := wkutil.ParseUint8(c.Query("channel_type"))
	offsetMessageId := wkutil.ParseInt64(c.Query("offset_message_id"))    // 偏移的messageId
	offsetMessageSeq := wkutil.ParseUint64(c.Query("offset_message_seq")) // 偏移的messageSeq（通过频道筛选并分页的时候需要传此值）
	pre := wkutil.ParseInt(c.Query("pre"))                                // 是否向前搜索
	payloadStr := strings.TrimSpace(c.Query("payload"))                   // base64编码的消息内容
	messageId := wkutil.ParseInt64(c.Query("message_id"))
	clientMsgNo := strings.TrimSpace(c.Query("client_msg_no"))

	// 解密payload
	var payload []byte
	var err error
	// base64解码payloadStr
	if strings.TrimSpace(payloadStr) != "" {
		payload, err = wkutil.Base64Decode(payloadStr)
		if err != nil {
			s.Error("base64 decode error", zap.Error(err), zap.String("payloadStr", payloadStr))
			c.ResponseError(err)
			return
		}
	}

	if nodeId > 0 && nodeId != s.opts.ConfigOptions.NodeId {
		node := s.cfgServer.Node(nodeId)
		c.Forward(fmt.Sprintf("%s%s", node.ApiServerAddr, c.Request.URL.Path))
		return
	}

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	if nodeId > 0 && nodeId != s.opts.ConfigOptions.NodeId {
		s.Error("nodeId not is self", zap.Uint64("nodeId", nodeId))
		c.ResponseError(errors.New("nodeId not is self"))
		return
	}

	// 搜索本地消息
	var searchLocalMessage = func() ([]*messageResp, error) {
		messages, err := s.db.SearchMessages(wkdb.MessageSearchReq{
			MessageId:        messageId,
			FromUid:          fromUid,
			Limit:            limit,
			ChannelId:        channelId,
			ChannelType:      channelType,
			OffsetMessageId:  offsetMessageId,
			OffsetMessageSeq: offsetMessageSeq,
			Pre:              pre == 1,
			Payload:          payload,
			ClientMsgNo:      clientMsgNo,
		})
		if err != nil {
			s.Error("查询消息失败！", zap.Error(err))
			return nil, err
		}

		resps := make([]*messageResp, 0, len(messages))
		for _, message := range messages {
			resps = append(resps, newMessageResp(message))
		}
		return resps, nil
	}

	if nodeId == s.opts.ConfigOptions.NodeId {
		messages, err := searchLocalMessage()
		if err != nil {
			s.Error("search local message failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, messageRespTotal{
			Data: messages,
		})
		return
	}

	nodes := s.cfgServer.Nodes()

	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	var messages = make([]*messageResp, 0)
	var messageLock sync.Mutex
	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for _, node := range nodes {
		if node.Id == s.opts.ConfigOptions.NodeId {
			localMessages, err := searchLocalMessage()
			if err != nil {
				s.Error("search local message failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			for _, localMsg := range localMessages {
				exist := false
				for _, msg := range messages {
					if localMsg.MessageId == msg.MessageId {
						exist = true
						break
					}
				}
				if !exist {
					messages = append(messages, localMsg)
				}
			}
			continue
		}
		if !node.Online {
			continue
		}

		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestNodeMessageSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				messageLock.Lock()
				for _, resultMsg := range result.Data {
					exist := false
					for _, msg := range messages {
						if resultMsg.MessageId == msg.MessageId {
							exist = true
							break
						}
					}
					if !exist {
						messages = append(messages, resultMsg)
					}
				}
				messageLock.Unlock()
				return nil
			}
		}(node.Id, c.Request.URL.Query()))
	}

	err = requestGroup.Wait()
	if err != nil {
		s.Error("search message failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	sort.Slice(messages, func(i, j int) bool {

		return messages[i].MessageId > messages[j].MessageId
	})

	if len(messages) > limit {
		if pre == 1 {
			messages = messages[len(messages)-limit:]
		} else {
			messages = messages[:limit]
		}
	}
	count, err := s.db.GetTotalMessageCount()
	if err != nil {
		s.Error("GetTotalMessageCount error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.JSON(http.StatusOK, messageRespTotal{
		Data:  messages,
		Total: count,
	})
}

func (s *Server) requestNodeMessageSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*messageRespTotal, error) {
	node := s.cfgServer.Node(nodeId)
	if node == nil {
		s.Error("requestNodeMessageSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, headers)
	if err != nil {
		return nil, err
	}
	err = handlerIMError(resp)
	if err != nil {
		return nil, err
	}

	var msgResp *messageRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &msgResp)
	if err != nil {
		return nil, err
	}
	return msgResp, nil
}
