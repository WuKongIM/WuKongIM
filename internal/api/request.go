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
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/sendgrid/rest"
	"go.uber.org/zap"
)

type request struct {
	wklog.Log
	s *Server
}

func newRequset(s *Server) *request {
	return &request{
		Log: wklog.NewWKLog("request"),
		s:   s,
	}
}

// ==================== 公共辅助函数 ====================

// makeChannelKey 生成频道唯一键
func makeChannelKey(channelId string, channelType uint8) string {
	return channelId + ":" + string(channelType)
}

// convertMessagesToResp 将数据库消息转换为响应消息
func convertMessagesToResp(messages []wkdb.Message) types.MessageRespSlice {
	messageResps := make(types.MessageRespSlice, 0, len(messages))
	for _, msg := range messages {
		messageResp := &types.MessageResp{}
		messageResp.From(msg, options.G.SystemUID)
		messageResps = append(messageResps, messageResp)
	}
	return messageResps
}

// enrichStreamMessages 填充流消息数据
func (s *request) enrichStreamMessages(channelId string, channelType uint8, messageResps types.MessageRespSlice) error {
	// 收集流消息的ClientMsgNo
	streamClientMsgNos := make([]string, 0)
	for _, message := range messageResps {
		setting := wkproto.Setting(message.Setting)
		if setting.IsSet(wkproto.SettingStream) {
			streamClientMsgNos = append(streamClientMsgNos, message.ClientMsgNo)
		}
	}

	if len(streamClientMsgNos) == 0 {
		return nil
	}

	// 加载流消息数据
	streamV2s, err := s.loadStreamV2Messages(channelId, channelType, streamClientMsgNos)
	if err != nil {
		s.Error("enrichStreamMessages: loadStreamV2Messages failed", zap.Error(err))
		return err
	}

	// 填充流消息数据到响应中
	for _, message := range messageResps {
		setting := wkproto.Setting(message.Setting)
		if !setting.IsSet(wkproto.SettingStream) {
			continue
		}
		for _, streamV2 := range streamV2s {
			if message.MessageId == streamV2.MessageId {
				message.End = streamV2.End
				message.EndReason = streamV2.EndReason
				message.Error = streamV2.Error
				message.StreamData = streamV2.Payload
				break
			}
		}
	}
	return nil
}

// runParallel 并行执行任务并收集结果
func runParallel[K comparable, V any](
	groups map[K]V,
	process func(V) ([]*channelRecentMessage, error),
) ([]*channelRecentMessage, error) {
	totalResults := make([]*channelRecentMessage, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(groups))

	for _, items := range groups {
		wg.Add(1)
		go func(v V) {
			defer wg.Done()
			results, err := process(v)
			if err != nil {
				errChan <- err
				return
			}
			mu.Lock()
			totalResults = append(totalResults, results...)
			mu.Unlock()
		}(items)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return nil, err
		}
	}

	return totalResults, nil
}

// ==================== 集群消息同步 ====================

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
		leaderInfo, err := service.Cluster.LeaderOfChannel(channelRecentMsgReq.ChannelId, channelRecentMsgReq.ChannelType)
		if err != nil {
			continue
		}
		peerChannelRecentMessageReqsMap[leaderInfo.Id] = append(peerChannelRecentMessageReqsMap[leaderInfo.Id], channelRecentMsgReq)
	}

	// 请求远程的消息列表
	if len(peerChannelRecentMessageReqsMap) > 0 {
		var reqErr error
		wg := &sync.WaitGroup{}
		for nodeId, peerChannelRecentMessageReqs := range peerChannelRecentMessageReqsMap {
			if nodeId == options.G.Cluster.NodeId {
				continue
			}
			wg.Add(1)
			go func(pID uint64, reqs []*channelRecentMessageReq) {
				defer wg.Done()
				results, err := s.requestSyncMessage(pID, reqs, uid, msgCount, orderByLast)
				if err != nil {
					s.Error("请求同步消息失败！", zap.Error(err), zap.Uint64("nodeId", pID))
					reqErr = err
					return
				}
				channelRecentMessagesLock.Lock()
				channelRecentMessages = append(channelRecentMessages, results...)
				channelRecentMessagesLock.Unlock()
			}(nodeId, peerChannelRecentMessageReqs)
		}
		wg.Wait()
		if reqErr != nil {
			s.Error("请求同步消息失败！!", zap.Error(err))
			return nil, reqErr
		}
	}

	// 请求本地的最近消息列表
	localChannels := peerChannelRecentMessageReqsMap[options.G.Cluster.NodeId]
	if len(localChannels) > 0 {
		results, err := s.getRecentMessages(uid, msgCount, localChannels, orderByLast)
		if err != nil {
			return nil, err
		}
		channelRecentMessages = append(channelRecentMessages, results...)
	}

	return channelRecentMessages, nil
}

func (s *request) requestSyncMessage(nodeID uint64, reqs []*channelRecentMessageReq, uid string, msgCount int, orderByLast bool) ([]*channelRecentMessage, error) {
	nodeInfo := service.Cluster.NodeInfoById(nodeID)
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

// ==================== 本地消息查询 ====================

// getRecentMessages 获取频道最近消息
// orderByLast: true 按照最新的消息排序 false 按照最旧的消息排序
func (s *request) getRecentMessages(uid string, msgCount int, channels []*channelRecentMessageReq, orderByLast bool) ([]*channelRecentMessage, error) {
	if len(channels) == 0 {
		return []*channelRecentMessage{}, nil
	}

	// 按数据库分片分组并行处理
	shardGroups := s.groupChannelsByDbShard(channels)
	return runParallel(shardGroups, func(chs []*channelRecentMessageReq) ([]*channelRecentMessage, error) {
		return s.processBatchChannels(uid, msgCount, chs, orderByLast)
	})
}

// groupChannelsByDbShard 按数据库分片对频道进行分组
func (s *request) groupChannelsByDbShard(channels []*channelRecentMessageReq) map[uint32][]*channelRecentMessageReq {
	shardGroups := make(map[uint32][]*channelRecentMessageReq)
	for _, channel := range channels {
		shardIndex := service.Store.GetChannelShardIndex(channel.ChannelId, channel.ChannelType)
		shardGroups[shardIndex] = append(shardGroups[shardIndex], channel)
	}
	return shardGroups
}

// processBatchChannels 批量处理频道消息查询（统一使用批量模式）
func (s *request) processBatchChannels(uid string, msgCount int, channels []*channelRecentMessageReq, orderByLast bool) ([]*channelRecentMessage, error) {
	if len(channels) == 0 {
		return nil, nil
	}

	// 构建批量查询请求
	wkdbChannels := make([]wkdb.Channel, 0, len(channels))
	for _, channel := range channels {
		wkdbChannels = append(wkdbChannels, wkdb.Channel{
			ChannelId:   channel.ChannelId,
			ChannelType: channel.ChannelType,
		})
	}

	// 批量获取用户最后消息序号
	userLastMsgSeqMap, err := service.Store.GetUserLastMsgSeqBatch(uid, wkdbChannels)
	if err != nil {
		s.Error("批量查询用户最后消息序号失败！", zap.Error(err), zap.String("uid", uid))
		return nil, err
	}

	// 批量加载消息
	msgMap, err := s.loadMessagesBatch(channels, msgCount, orderByLast)
	if err != nil {
		return nil, err
	}

	// 组装最终结果
	results := make([]*channelRecentMessage, 0, len(channels))
	for _, channel := range channels {
		channelKey := makeChannelKey(channel.ChannelId, channel.ChannelType)

		// 获取消息响应
		messageResps := msgMap[channelKey]

		// 处理流消息
		if err := s.enrichStreamMessages(channel.ChannelId, channel.ChannelType, messageResps); err != nil {
			return nil, err
		}

		results = append(results, &channelRecentMessage{
			ChannelId:      channel.ChannelId,
			ChannelType:    channel.ChannelType,
			Messages:       messageResps,
			UserLastMsgSeq: userLastMsgSeqMap[channelKey],
		})
	}

	return results, nil
}

// loadMessagesBatch 批量加载频道消息（使用批量查询接口）
func (s *request) loadMessagesBatch(channels []*channelRecentMessageReq, msgCount int, orderByLast bool) (map[string]types.MessageRespSlice, error) {
	// 构建批量查询请求
	batchRequests := make([]wkdb.BatchMsgRequest, 0, len(channels))
	for _, channel := range channels {
		msgSeq := channel.LastMsgSeq
		if orderByLast && msgSeq > 0 {
			msgSeq = msgSeq - 1
		}
		batchRequests = append(batchRequests, wkdb.BatchMsgRequest{
			ChannelId:   channel.ChannelId,
			ChannelType: channel.ChannelType,
			MsgSeq:      msgSeq,
			Limit:       msgCount,
			OrderByLast: orderByLast,
		})
	}

	// 批量查询消息
	batchResponses, err := service.Store.LoadMsgsBatch(batchRequests)
	if err != nil {
		s.Error("批量查询消息失败！", zap.Error(err))
		return nil, err
	}

	// 构建结果映射
	msgMap := make(map[string]types.MessageRespSlice, len(batchResponses))
	for _, resp := range batchResponses {
		channelKey := makeChannelKey(resp.ChannelId, resp.ChannelType)
		messageResps := convertMessagesToResp(resp.Messages)
		if orderByLast {
			sort.Sort(sort.Reverse(messageResps))
		}
		msgMap[channelKey] = messageResps
	}

	return msgMap, nil
}

// ==================== 工具函数 ====================

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

func (s *request) loadStreamV2Messages(channelId string, channelType uint8, clientMsgNos []string) ([]*wkdb.StreamV2, error) {
	slotLeaderId, err := service.Cluster.SlotLeaderIdOfChannel(channelId, channelType)
	if err != nil {
		s.Error("loadStreamMessages: get leader id failed", zap.Error(err),
			zap.String("channelId", channelId), zap.Uint8("channelType", channelType))
		return nil, err
	}

	if options.G.IsLocalNode(slotLeaderId) {
		return service.CommonService.GetStreamsForLocal(clientMsgNos)
	}
	return s.s.client.RequestStreamsV2(slotLeaderId, clientMsgNos)
}
