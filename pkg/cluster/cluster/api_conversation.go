package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) conversationSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	currentPage := wkutil.ParseInt(c.Query("current_page")) // 页码
	uid := strings.TrimSpace(c.Query("uid"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))

	if currentPage <= 0 {
		currentPage = 1
	}
	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var searchLocalConversations = func() (*conversationRespTotal, error) {
		conversations, err := s.db.SearchConversation(wkdb.ConversationSearchReq{
			Uid:         uid,
			Limit:       limit,
			CurrentPage: currentPage,
		})
		if err != nil {
			s.Error("search conversation failed", zap.Error(err))
			return nil, err
		}

		conversationResps := make([]*conversationResp, 0, len(conversations))

		for _, conversation := range conversations {
			lastMsgSeq, _, err := s.db.GetChannelLastMessageSeq(conversation.ChannelId, conversation.ChannelType)
			if err != nil {
				s.Error("GetChannelLastMessageSeq error", zap.Error(err))
				return nil, err
			}
			resp := newConversationResp(conversation)
			resp.LastMsgSeq = lastMsgSeq
			if lastMsgSeq >= conversation.ReadToMsgSeq {
				resp.UnreadCount = uint32(lastMsgSeq - conversation.ReadToMsgSeq)
			}
			conversationResps = append(conversationResps, resp)
		}
		count, err := s.db.GetTotalSessionCount()
		if err != nil {
			s.Error("GetTotalConversationCount error", zap.Error(err))
			return nil, err
		}
		return &conversationRespTotal{
			Data:  conversationResps,
			Total: count,
		}, nil
	}

	if nodeId == s.opts.ConfigOptions.NodeId {
		result, err := searchLocalConversations()
		if err != nil {
			s.Error("search local conversation  failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	nodes := s.cfgServer.Nodes()
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	conversationResps := make([]*conversationResp, 0)
	for _, node := range nodes {
		if node.Id == s.opts.ConfigOptions.NodeId {
			result, err := searchLocalConversations()
			if err != nil {
				s.Error("search local conversation  failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			conversationResps = append(conversationResps, result.Data...)
			continue
		}

		if !s.NodeIsOnline(node.Id) {
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
				result, err := s.requestConversationSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				conversationResps = append(conversationResps, result.Data...)
				return nil
			}
		}(node.Id, c.Request.URL.Query()))

	}

	err := requestGroup.Wait()
	if err != nil {
		s.Error("search conversation request failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 移除conversationResps中重复的数据
	conversationRespMap := make(map[string]*conversationResp)
	for _, conversationResp := range conversationResps {
		conversationRespMap[fmt.Sprintf("%s-%s-%d", conversationResp.Uid, conversationResp.ChannelId, conversationResp.ChannelType)] = conversationResp
	}

	conversationResps = make([]*conversationResp, 0, len(conversationRespMap))
	for _, conversationResp := range conversationRespMap {
		conversationResps = append(conversationResps, conversationResp)
	}

	c.JSON(http.StatusOK, conversationRespTotal{
		Data:  conversationResps,
		Total: 0,
	})

}

func (s *Server) requestConversationSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*conversationRespTotal, error) {
	node := s.cfgServer.Node(nodeId)
	if node == nil {
		s.Error("requestConversationSearch failed, node not found", zap.Uint64("nodeId", nodeId))
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

	var conversationRespTotal *conversationRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &conversationRespTotal)
	if err != nil {
		return nil, err
	}
	return conversationRespTotal, nil
}
