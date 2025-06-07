package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/sendgrid/rest"
	"go.uber.org/zap"
)

type request struct {
	wklog.Log
}

func newRequset() *request {

	return &request{
		Log: wklog.NewWKLog("request"),
	}
}

func (s *request) getRecentMessagesForCluster(uid string, msgCount int, channels []*channelRecentMessageReq, orderByLast bool) ([]*channelRecentMessage, error) {
	if len(channels) == 0 {
		return nil, nil
	}
	var (
		channelRecentMessages     []*channelRecentMessage
		err                       error
		channelRecentMessagesLock sync.Mutex
	)

	// 按照频道所在节点进行分组
	peerChannelRecentMessageReqsMap := make(map[uint64][]*channelRecentMessageReq)
	for _, channelRecentMsgReq := range channels {
		fakeChannelId := channelRecentMsgReq.ChannelId
		leaderInfo, err := service.Cluster.LeaderOfChannel(fakeChannelId, channelRecentMsgReq.ChannelType) // 获取频道的领导节点
		if err != nil {
			// s.Warn("getRecentMessagesForCluster: 获取频道所在节点失败！", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelRecentMsgReq.ChannelType))
			continue
		}
		peerChannelRecentMessageReqsMap[leaderInfo.Id] = append(peerChannelRecentMessageReqsMap[leaderInfo.Id], channelRecentMsgReq)
	}

	// 请求远程的消息列表
	if len(peerChannelRecentMessageReqsMap) > 0 {
		var reqErr error
		wg := &sync.WaitGroup{}
		for nodeId, peerChannelRecentMessageReqs := range peerChannelRecentMessageReqsMap {
			if nodeId == options.G.Cluster.NodeId { // 本机节点忽略
				continue
			}
			wg.Add(1)
			go func(pID uint64, reqs []*channelRecentMessageReq, uidStr string, msgCt int) {
				results, err := s.requestSyncMessage(pID, reqs, uidStr, msgCt, orderByLast)
				if err != nil {
					s.Error("请求同步消息失败！", zap.Error(err), zap.Uint64("nodeId", pID))
					reqErr = err
				} else {
					channelRecentMessagesLock.Lock()
					channelRecentMessages = append(channelRecentMessages, results...)
					channelRecentMessagesLock.Unlock()
				}
				wg.Done()
			}(nodeId, peerChannelRecentMessageReqs, uid, msgCount)
		}
		wg.Wait()
		if reqErr != nil {
			s.Error("请求同步消息失败！!", zap.Error(err))
			return nil, reqErr
		}
	}

	// 请求本地的最近消息列表
	localPeerChannelRecentMessageReqs := peerChannelRecentMessageReqsMap[options.G.Cluster.NodeId]
	if len(localPeerChannelRecentMessageReqs) > 0 {
		results, err := s.getRecentMessages(uid, msgCount, localPeerChannelRecentMessageReqs, orderByLast)
		if err != nil {
			return nil, err
		}
		channelRecentMessages = append(channelRecentMessages, results...)
	}
	return channelRecentMessages, nil
}

func (s *request) requestSyncMessage(nodeID uint64, reqs []*channelRecentMessageReq, uid string, msgCount int, orderByLast bool) ([]*channelRecentMessage, error) {

	nodeInfo := service.Cluster.NodeInfoById(nodeID) // 获取频道的领导节点
	if nodeInfo == nil {
		s.Error("节点不存在！", zap.Uint64("nodeID", nodeID))
		return nil, errors.New("节点不存在！")
	}
	reqURL := fmt.Sprintf("%s/%s", nodeInfo.ApiServerAddr, "conversation/syncMessages")
	request := rest.Request{
		Method:  rest.Method("POST"),
		BaseURL: reqURL,
		Body: []byte(wkutil.ToJSON(map[string]interface{}{
			"uid":           uid,
			"msg_count":     msgCount,
			"channels":      reqs,
			"order_by_last": wkutil.BoolToInt(orderByLast),
		})),
	}
	s.Debug("同步会话消息!", zap.String("apiURL", reqURL), zap.String("uid", uid), zap.Any("channels", reqs))
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	resp, err := rest.SendWithContext(timeoutCtx, request)
	if err != nil {
		return nil, err
	}
	if err := handlerIMError(resp); err != nil {
		return nil, err
	}
	var results []*channelRecentMessage
	if err := wkutil.ReadJSONByByte([]byte(resp.Body), &results); err != nil {
		return nil, err
	}
	return results, nil
}

// getRecentMessages 获取频道最近消息
// orderByLast: true 按照最新的消息排序 false 按照最旧的消息排序
func (s *request) getRecentMessages(uid string, msgCount int, channels []*channelRecentMessageReq, orderByLast bool) ([]*channelRecentMessage, error) {
	channelRecentMessages := make([]*channelRecentMessage, 0, len(channels))
	if len(channels) == 0 {
		return channelRecentMessages, nil
	}

	// 批量查询优化：将相同查询条件的频道分组
	type batchKey struct {
		msgSeq      uint64
		orderByLast bool
	}
	batchGroups := make(map[batchKey][]*channelRecentMessageReq)

	for _, channel := range channels {
		key := batchKey{
			msgSeq:      channel.LastMsgSeq,
			orderByLast: orderByLast,
		}
		batchGroups[key] = append(batchGroups[key], channel)
	}

	// 使用并发处理不同的批次组
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(batchGroups))

	for key, batchChannels := range batchGroups {
		wg.Add(1)
		go func(k batchKey, chs []*channelRecentMessageReq) {
			defer wg.Done()

			// 批量处理相同条件的频道
			results, err := s.processBatchChannels(uid, msgCount, chs, k.orderByLast)
			if err != nil {
				errChan <- err
				return
			}

			mu.Lock()
			channelRecentMessages = append(channelRecentMessages, results...)
			mu.Unlock()
		}(key, batchChannels)
	}

	wg.Wait()
	close(errChan)

	// 检查错误
	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	return channelRecentMessages, nil
}

// processBatchChannels 批量处理频道消息查询
func (s *request) processBatchChannels(uid string, msgCount int, channels []*channelRecentMessageReq, orderByLast bool) ([]*channelRecentMessage, error) {
	results := make([]*channelRecentMessage, 0, len(channels))

	// 如果批次较小，使用原有逻辑
	if len(channels) <= 5 {
		for _, channel := range channels {
			result, err := s.processSingleChannel(uid, msgCount, channel, orderByLast)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
		return results, nil
	}

	// 批次较大时，使用优化的批量查询
	// TODO: 这里可以进一步优化，实现真正的批量数据库查询
	// 目前先使用并发来提升性能
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(channels))

	// 限制并发数
	semaphore := make(chan struct{}, 10)

	for _, channel := range channels {
		wg.Add(1)
		go func(ch *channelRecentMessageReq) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result, err := s.processSingleChannel(uid, msgCount, ch, orderByLast)
			if err != nil {
				errChan <- err
				return
			}

			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(channel)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}

// processSingleChannel 处理单个频道的消息查询
func (s *request) processSingleChannel(uid string, msgCount int, channel *channelRecentMessageReq, orderByLast bool) (*channelRecentMessage, error) {
	var (
		recentMessages []wkdb.Message
		err            error
	)

	fakeChannelID := channel.ChannelId
	msgSeq := channel.LastMsgSeq
	messageResps := types.MessageRespSlice{}

	if orderByLast {
		if msgSeq > 0 {
			msgSeq = msgSeq - 1 // 这里减1的目的是为了获取到最后一条消息
		}

		recentMessages, err = service.Store.LoadLastMsgsWithEnd(fakeChannelID, channel.ChannelType, msgSeq, msgCount)
		if err != nil {
			s.Error("查询最近消息失败！", zap.Error(err), zap.String("uid", uid), zap.String("fakeChannelID", fakeChannelID), zap.Uint8("channelType", channel.ChannelType), zap.Uint64("LastMsgSeq", channel.LastMsgSeq))
			return nil, err
		}
		if len(recentMessages) > 0 {
			for _, recentMessage := range recentMessages {
				messageResp := &types.MessageResp{}
				messageResp.From(recentMessage, options.G.SystemUID)
				messageResps = append(messageResps, messageResp)
			}
		}
		sort.Sort(sort.Reverse(messageResps))
	} else {
		recentMessages, err = service.Store.LoadNextRangeMsgs(fakeChannelID, channel.ChannelType, msgSeq, 0, msgCount)
		if err != nil {
			s.Error("查询最近消息失败！", zap.Error(err), zap.String("uid", uid), zap.String("fakeChannelID", fakeChannelID), zap.Uint8("channelType", channel.ChannelType), zap.Uint64("LastMsgSeq", channel.LastMsgSeq))
			return nil, err
		}
		if len(recentMessages) > 0 {
			for _, recentMessage := range recentMessages {
				messageResp := &types.MessageResp{}
				messageResp.From(recentMessage, options.G.SystemUID)
				messageResps = append(messageResps, messageResp)
			}
		}
	}

	return &channelRecentMessage{
		ChannelId:   channel.ChannelId,
		ChannelType: channel.ChannelType,
		Messages:    messageResps,
	}, nil
}

func handlerIMError(resp *rest.Response) error {
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest {
			resultMap, err := wkutil.JSONToMap(resp.Body)
			if err != nil {
				return err
			}
			if resultMap != nil && resultMap["msg"] != nil {
				return fmt.Errorf("IM服务失败！ -> %s", resultMap["msg"])
			}
		}
		return fmt.Errorf("IM服务返回状态[%d]失败！", resp.StatusCode)
	}
	return nil
}
