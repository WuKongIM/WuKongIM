package webhook

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/grpcpool"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhook"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/nsqio/go-diskqueue"
	"github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type Webhook struct {
	wklog.Log
	eventPool        *ants.Pool
	httpClient       *http.Client
	webhookGRPCPool  *grpcpool.Pool // webhook grpc客户端
	stoped           chan struct{}
	onlinestatusLock sync.RWMutex
	onlinestatusList []string
	focusEvents      map[string]struct{} // 用户关注的事件类型,如果为空则推送所有类型
	backend          diskqueue.Interface
}

func New() *Webhook {
	eventPool, err := ants.NewPool(options.G.EventPoolSize, ants.WithPanicHandler(func(err interface{}) {
		wklog.Panic("Webhook panic", zap.Any("err", err), zap.Stack("stack"))
	}))
	if err != nil {
		panic(err)
	}
	var (
		webhookGRPCPool *grpcpool.Pool
	)
	if options.G.WebhookGRPCOn() {
		webhookGRPCPool, err = grpcpool.New(func() (*grpc.ClientConn, error) {
			return grpc.Dial(options.G.Webhook.GRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithKeepaliveParams(keepalive.ClientParameters{
				Time:    5 * time.Minute, // send pings every 5 minute if there is no activity
				Timeout: 2 * time.Second, // wait 1 second for ping ack before considering the connection dead
			}))
		}, 2, 20, time.Minute*5) // 初始化2个连接 最多20个连接
		if err != nil {
			panic(err)
		}

	}

	// 检查用户配置了关注的事件
	var focusEvents = make(map[string]struct{})
	if len(options.G.Webhook.FocusEvents) > 0 {
		for _, focusEvent := range options.G.Webhook.FocusEvents {
			if focusEvent == "" {
				continue
			}
			if _, ok := eventWebHook[focusEvent]; ok {
				focusEvents[focusEvent] = struct{}{}
			}
		}
	}

	minMsgSize := int32(2)
	maxMsgSize := int32(1024 * 1024 * 200)
	dataDir := path.Join(options.G.DataDir, "diskqueue")
	maxBytesPerFile := int64(1024 * 1024 * 1024)
	syncEvery := int64(2500)       // 每2500次写入同步一次
	syncTimeout := time.Second * 2 // 每次间隔2秒同步一次
	err = os.MkdirAll(dataDir, 0755)
	if err != nil {
		panic(err)
	}

	backend := diskqueue.New(
		"wk_webhook_q",
		dataDir,
		maxBytesPerFile,
		minMsgSize,
		maxMsgSize,
		syncEvery,
		syncTimeout,
		func(lvl diskqueue.LogLevel, f string, args ...interface{}) {
			wklog.Info("webhook backend", zap.String("lvl", lvl.String()), zap.String("f", f), zap.Any("args", args))
		},
	)

	return &Webhook{
		Log:              wklog.NewWKLog("Webhook"),
		eventPool:        eventPool,
		webhookGRPCPool:  webhookGRPCPool,
		onlinestatusList: make([]string, 0),
		stoped:           make(chan struct{}),
		backend:          backend,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second, // 增加KeepAlive时间
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          500,              // 增加最大空闲连接数
				MaxIdleConnsPerHost:   100,              // 增加每个主机的最大空闲连接数
				MaxConnsPerHost:       100,              // 设置每个主机的最大连接数
				IdleConnTimeout:       90 * time.Second, // 减少空闲连接超时
				TLSHandshakeTimeout:   10 * time.Second, // 增加TLS握手超时
				ResponseHeaderTimeout: 10 * time.Second, // 增加响应头超时
				ExpectContinueTimeout: 1 * time.Second,
				DisableCompression:    false, // 启用压缩
			},
			Timeout: 30 * time.Second, // 设置总体超时时间
		},
		focusEvents: focusEvents,
	}
}

func (w *Webhook) Start() error {
	go w.notifyQueueLoop()
	go w.loopOnlineStatus()

	err := w.recoverNotifyQueue()
	if err != nil {
		return err
	}

	return nil
}

// recoverNotifyQueue 恢复通知队列(兼容老版本)
func (w *Webhook) recoverNotifyQueue() error {
	messages, err := service.Store.GetMessagesOfNotifyQueue(4000)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}
	err = w.AppendMessageOfNotifyQueue(messages)
	if err != nil {
		return err
	}

	messageIDs := make([]int64, 0, len(messages))
	for _, msg := range messages {
		messageIDs = append(messageIDs, msg.MessageID)
	}
	err = service.Store.RemoveMessagesOfNotifyQueue(messageIDs)
	if err != nil {
		return err
	}

	return nil
}

func (w *Webhook) Stop() {
	close(w.stoped)
	w.backend.Close()
}

// Online 用户设备上线通知
func (w *Webhook) Online(uid string, deviceFlag wkproto.DeviceFlag, connId int64, deviceOnlineCount int, totalOnlineCount int) {
	w.onlinestatusLock.Lock()
	defer w.onlinestatusLock.Unlock()
	online := 1
	w.onlinestatusList = append(w.onlinestatusList, fmt.Sprintf("%s-%d-%d-%d-%d-%d", uid, deviceFlag, online, connId, deviceOnlineCount, totalOnlineCount))

	w.Debug("User online", zap.String("uid", uid), zap.String("deviceFlag", deviceFlag.String()), zap.Int64("id", connId))
}

func (w *Webhook) Offline(uid string, deviceFlag wkproto.DeviceFlag, connId int64, deviceOnlineCount int, totalOnlineCount int) {
	w.onlinestatusLock.Lock()
	defer w.onlinestatusLock.Unlock()
	online := 0
	// 用户ID-用户设备标记-在线状态-socket ID-当前设备标记下的设备在线数量-当前用户下的所有设备在线数量
	w.onlinestatusList = append(w.onlinestatusList, fmt.Sprintf("%s-%d-%d-%d-%d-%d", uid, deviceFlag, online, connId, deviceOnlineCount, totalOnlineCount))

	w.Debug("User offline", zap.String("uid", uid), zap.String("deviceFlag", deviceFlag.String()))
}

// TriggerEvent 触发事件
func (w *Webhook) TriggerEvent(event *types.Event) {
	if !options.G.WebhookOn(event.Event) { // 没设置webhook直接忽略
		return
	}
	err := w.eventPool.Submit(func() {
		jsonData, err := json.Marshal(event.Data)
		if err != nil {
			w.Error("webhook的event数据不能json化！", zap.Error(err))
			return
		}

		if options.G.WebhookGRPCOn() {
			err = w.sendWebhookForGRPC(event.Event, jsonData)
		} else {
			err = w.sendWebhookForHttp(event.Event, jsonData)
		}
		if err != nil {
			w.Error("请求webhook失败！", zap.Error(err), zap.String("event", event.Event))
			return
		}

	})
	if err != nil {
		w.Error("提交事件失败", zap.Error(err))
	}
}

func (w *Webhook) NotifyOfflineMsg(msgs []*eventbus.Event) {
	for _, msg := range msgs {
		w.notifyOfflineMsg(msg, msg.OfflineUsers)
	}
}

func (w *Webhook) notifyOfflineMsg(e *eventbus.Event, subscribers []string) {
	compress := ""
	toUIDs := subscribers
	var compresssToUIDs []byte
	if options.G.Channel.SubscriberCompressOfCount > 0 && len(subscribers) > options.G.Channel.SubscriberCompressOfCount {
		buff := new(bytes.Buffer)
		gWriter := gzip.NewWriter(buff)
		defer gWriter.Close()
		_, err := gWriter.Write([]byte(wkutil.ToJSON(subscribers)))
		if err != nil {
			w.Error("压缩订阅者失败！", zap.Error(err))
		} else {
			toUIDs = make([]string, 0)
			compress = "gzip"
			compresssToUIDs = buff.Bytes()
		}
	}
	sendPacket := e.Frame.(*wkproto.SendPacket)
	// 推送离线到上层应用
	w.TriggerEvent(&types.Event{
		Event: types.EventMsgOffline,
		Data: types.MessageOfflineNotify{
			MessageResp: types.MessageResp{
				Header: types.MessageHeader{
					RedDot:    wkutil.BoolToInt(sendPacket.RedDot),
					SyncOnce:  wkutil.BoolToInt(sendPacket.SyncOnce),
					NoPersist: wkutil.BoolToInt(sendPacket.NoPersist),
				},
				Setting:      sendPacket.Setting.Uint8(),
				ClientMsgNo:  sendPacket.ClientMsgNo,
				MessageId:    e.MessageId,
				MessageIdStr: strconv.FormatInt(e.MessageId, 10),
				MessageSeq:   e.MessageSeq,
				FromUID:      e.Conn.Uid,
				ChannelID:    sendPacket.ChannelID,
				ChannelType:  sendPacket.ChannelType,
				Topic:        sendPacket.Topic,
				Expire:       sendPacket.Expire,
				Timestamp:    int32(time.Now().Unix()),
				Payload:      sendPacket.Payload,
			},
			ToUids:          toUIDs,
			Compress:        compress,
			CompresssToUids: compresssToUIDs,
			SourceId:        int64(options.G.Cluster.NodeId),
		},
	})
}

func (w *Webhook) AppendMessageOfNotifyQueue(messages []wkdb.Message) error {
	for _, msg := range messages {
		data, err := msg.Marshal()
		if err != nil {
			return err
		}
		err = w.backend.Put(data)
		if err != nil {
			return err
		}
	}
	return nil
}

// 通知上层应用 TODO: 此初报错可以做一个邮件报警处理类的东西，
func (w *Webhook) notifyQueueLoop() {
	errorSleepTime := time.Second * 1 // 发生错误后sleep时间
	ticker := time.NewTicker(options.G.Webhook.MsgNotifyEventPushInterval)
	defer ticker.Stop()

	// 本地缓存从diskqueue读取的消息
	// 初始化容量可以根据平均的 MsgNotifyEventCountPerPush 调整，例如其2倍，以减少重分配
	localMessageCache := make([]wkdb.Message, 0, options.G.Webhook.MsgNotifyEventCountPerPush*2)

	// 触发立即推送的缓存阈值
	// 如果配置了 MsgNotifyEventCountPerPush，则优先使用它作为即时推送的批大小，否则默认20
	// 确保 pushThreshold 是一个正数
	pushThreshold := options.G.Webhook.MsgNotifyEventCountPerPush
	if pushThreshold <= 0 {
		pushThreshold = 20 // 默认阈值
	}

	errMessageIDMap := make(map[int64]int) // 记录错误的消息ID value为错误次数

	if !options.G.WebhookOn(types.EventMsgNotify) {
		w.Info("Webhook for EventMsgNotify is not enabled.")
		return
	}
	w.Info("notifyQueueLoop started", zap.Duration("pushInterval", options.G.Webhook.MsgNotifyEventPushInterval), zap.Int("pushThreshold", pushThreshold))

	for {
		var shouldPush bool // 标记是否应该推送消息

		select {
		case data, ok := <-w.backend.ReadChan():
			if !ok { // diskqueue的ReadChan被关闭
				w.Info("diskqueue ReadChan closed. Attempting to push remaining cached messages before exiting notifyQueueLoop.")
				// 在退出前尝试推送剩余的缓存消息
				if len(localMessageCache) > 0 {
					w.pushMessages(localMessageCache, errMessageIDMap)
					// localMessageCache = localMessageCache[:0] // 清空缓存 (可选，因为即将退出)
				}
				return
			}

			var msg wkdb.Message // 假设 wkdb.Message 是一个结构体。如果它是指针类型，应为 *wkdb.Message
			err := msg.Unmarshal(data)
			if err != nil {
				w.Error("Failed to unmarshal message from diskqueue", zap.Error(err), zap.Int("data_len", len(data)))
				continue // 跳过这条无法解析的消息
			}
			localMessageCache = append(localMessageCache, msg)
			w.Debug("Message added to local cache from diskqueue", zap.Int64("messageID", msg.MessageID), zap.Int("cacheSize", len(localMessageCache)), zap.Int64("diskqueueDepth", w.backend.Depth()))

			if len(localMessageCache) >= pushThreshold {
				w.Debug("Push threshold reached, preparing to push messages.", zap.Int("cacheSize", len(localMessageCache)), zap.Int("threshold", pushThreshold))
				shouldPush = true
			}

		case <-ticker.C:
			w.Debug("Ticker triggered.", zap.Int("cacheSize", len(localMessageCache)))
			if len(localMessageCache) > 0 {
				shouldPush = true
			}

		case <-w.stoped:
			w.Info("notifyQueueLoop stopping. Attempting to push any remaining cached messages.")
			// 在退出前尝试推送剩余的缓存消息
			if len(localMessageCache) > 0 {
				w.pushMessages(localMessageCache, errMessageIDMap)
			}
			return
		}

		if shouldPush && len(localMessageCache) > 0 {
			// 创建待推送消息的副本，以避免在推送操作（可能是异步或耗时）中修改原始缓存
			messagesToPush := make([]wkdb.Message, len(localMessageCache))
			copy(messagesToPush, localMessageCache)

			// 清空本地缓存，准备接收新消息
			localMessageCache = localMessageCache[:0]

			w.Debug("Attempting to push messages", zap.Int("count", len(messagesToPush)))
			allSucceeded, retryableMessages := w.pushMessages(messagesToPush, errMessageIDMap)
			if !allSucceeded {
				w.Warn("Failed to push a batch of messages. Some may be retried.", zap.Int("count", len(messagesToPush)), zap.Int("retryable_count", len(retryableMessages)))
				if len(retryableMessages) > 0 {
					// 将需要重试的消息放回本地缓存的开头，以便优先处理
					w.Debug("Adding retryable messages back to local cache", zap.Int("count", len(retryableMessages)))
					localMessageCache = append(retryableMessages, localMessageCache...)
					// 限制localMessageCache的最大长度，防止无限增长，虽然diskqueue本身有大小限制
					// 这个最大长度可以是一个比较大的合理值，例如 pushThreshold 的几倍
					maxLocalCacheSize := pushThreshold * 5
					if len(localMessageCache) > maxLocalCacheSize {
						w.Warn("Local message cache has grown too large after adding retryable messages. Truncating.", zap.Int("current_size", len(localMessageCache)), zap.Int("max_size", maxLocalCacheSize))
						localMessageCache = localMessageCache[len(localMessageCache)-maxLocalCacheSize:] // 保留最新的部分，或者也可以选择保留最旧的
					}
				}
				time.Sleep(errorSleepTime)
			}
		}
	}
}

// pushMessages 是一个辅助函数，用于处理实际的消息推送逻辑
// 返回 allSucceeded: 如果批次中所有消息都成功发送（或者那些失败的都已达到最大重试次数被放弃），则为 true
// 返回 retryableMessages: 如果批次发送失败，这里包含那些未达到最大重试次数且需要被重新尝试的消息。
func (w *Webhook) pushMessages(messages []wkdb.Message, errMessageIDMap map[int64]int) (allSucceeded bool, retryableMessages []wkdb.Message) {
	if len(messages) == 0 {
		return true, nil
	}

	messageResps := make([]*types.MessageResp, 0, len(messages))
	for _, msg := range messages {
		resp := &types.MessageResp{}
		resp.From(msg, options.G.SystemUID)
		messageResps = append(messageResps, resp)
	}

	messageData, err := w.marshalMessages(messageResps)
	if err != nil {
		w.Error("Failed to marshal messages for webhook", zap.Error(err), zap.Int("message_count", len(messages)))
		return true, nil // 序列化错误，不重试此批次
	}

	var sendErr error
	if options.G.WebhookGRPCOn() {
		sendErr = w.sendWebhookForGRPC(types.EventMsgNotify, messageData)
	} else {
		sendErr = w.sendWebhookForHttp(types.EventMsgNotify, messageData)
	}

	if sendErr != nil {
		w.Error("Failed to send webhook for a batch of messages", zap.Error(sendErr), zap.Int("message_count", len(messages)))

		retryableMessages = make([]wkdb.Message, 0)
		processedAllInBatch := true // 假设一开始批次中的消息都处理完了（可能成功，可能达到最大重试）

		for _, originalMsg := range messages {
			errCount := errMessageIDMap[originalMsg.MessageID]
			errCount++
			errMessageIDMap[originalMsg.MessageID] = errCount

			if errCount >= options.G.Webhook.MsgNotifyEventRetryMaxCount {
				w.Warn("Message reached max retry count and will be dropped from error tracking map for this webhook cycle.", zap.Int64("messageID", originalMsg.MessageID))
				delete(errMessageIDMap, originalMsg.MessageID) // 从重试map中删除
			} else {
				retryableMessages = append(retryableMessages, originalMsg) // 加入到可重试列表
				processedAllInBatch = false                                // 只要有一个消息需要重试，就标记整个批次未完全处理
			}
		}
		return processedAllInBatch, retryableMessages
	}

	// 推送成功，清理 errMessageIDMap 中这些消息的错误计数
	for _, originalMsg := range messages {
		delete(errMessageIDMap, originalMsg.MessageID)
	}
	w.Info("Successfully pushed messages to webhook", zap.Int("count", len(messages)))
	return true, nil
}

// marshalMessages 序列化消息（可以后续优化为使用对象池）
func (w *Webhook) marshalMessages(messages []*types.MessageResp) ([]byte, error) {
	return json.Marshal(messages)
}

func (w *Webhook) loopOnlineStatus() {
	if !options.G.WebhookOn(types.EventOnlineStatus) {
		return
	}
	opLen := 0    // 最后一次操作在线状态数组的长度
	errCount := 0 // webhook请求失败重试次数
	for {
		if opLen == 0 {
			w.onlinestatusLock.Lock()
			opLen = len(w.onlinestatusList)
			w.onlinestatusLock.Unlock()
		}
		if opLen == 0 {
			time.Sleep(time.Second * 2) // 没有数据就休息2秒
			continue
		}
		w.onlinestatusLock.Lock()
		data := w.onlinestatusList[:opLen]
		w.onlinestatusLock.Unlock()
		jsonData, err := json.Marshal(data)
		if err != nil {
			w.Error("webhook的event数据不能json化！", zap.Error(err))
			time.Sleep(time.Second * 1)
			continue
		}

		if options.G.WebhookGRPCOn() {
			err = w.sendWebhookForGRPC(types.EventOnlineStatus, jsonData)
		} else {
			err = w.sendWebhookForHttp(types.EventOnlineStatus, jsonData)
		}
		if err != nil {
			errCount++
			w.Error("请求在线状态webhook失败！", zap.Error(err))
			if errCount >= options.G.Webhook.MsgNotifyEventRetryMaxCount {
				w.Error("请求在线状态webhook失败通知超过最大次数！", zap.Int("MsgNotifyEventRetryMaxCount", options.G.Webhook.MsgNotifyEventRetryMaxCount))

				w.onlinestatusLock.Lock()
				w.onlinestatusList = w.onlinestatusList[opLen:]
				opLen = 0
				w.onlinestatusLock.Unlock()

				errCount = 0
			}

			time.Sleep(time.Second * 1) // 如果报错就休息下
			continue
		}

		w.onlinestatusLock.Lock()
		w.onlinestatusList = w.onlinestatusList[opLen:]
		opLen = 0
		w.onlinestatusLock.Unlock()

	}
}

func (w *Webhook) sendWebhookForHttp(event string, data []byte) error {
	eventURL := fmt.Sprintf("%s?event=%s", options.G.Webhook.HTTPAddr, event)
	startTime := time.Now().UnixNano() / 1000 / 1000
	w.Debug("webhook开始请求", zap.String("eventURL", eventURL))
	resp, err := w.httpClient.Post(eventURL, "application/json", bytes.NewBuffer(data))
	w.Debug("webhook请求结束 耗时", zap.Int64("mill", time.Now().UnixNano()/1000/1000-startTime))
	if err != nil {
		w.Warn("调用第三方消息通知失败！", zap.String("Webhook", options.G.Webhook.HTTPAddr), zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		w.Warn("第三方消息通知接口返回状态错误！", zap.Int("status", resp.StatusCode), zap.String("Webhook", options.G.Webhook.HTTPAddr))
		return errors.New("第三方消息通知接口返回状态错误！")
	}
	return nil
}

func (w *Webhook) sendWebhookForGRPC(event string, data []byte) error {

	startNow := time.Now()
	startTime := startNow.UnixNano() / 1000 / 1000
	w.Debug("webhook grpc 开始请求", zap.String("event", event))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	clientConn, err := w.webhookGRPCPool.Get(ctx)
	if err != nil {
		return err
	}
	defer clientConn.Close()
	// cliConn, err := grpc.Dial(l.opts.WebhookGRPC, grpc.WithInsecure())
	// if err != nil {
	// 	return err
	// }
	cli := wkhook.NewWebhookServiceClient(clientConn)

	sendCtx, sendCancel := context.WithTimeout(context.Background(), time.Second*10)
	defer sendCancel()
	resp, err := cli.SendWebhook(sendCtx, &wkhook.EventReq{
		Event: event,
		Data:  data,
	})
	w.Debug("webhook grpc 请求结束 耗时", zap.Int64("mill", time.Now().UnixNano()/1000/1000-startTime))

	if err != nil {
		return err
	}
	if resp.Status != wkhook.EventStatus_Success {
		return errors.New("grpc返回状态错误！")
	}
	return nil
}

var (
	// eventWebHook 用于快速校验用用户配置的关注事件
	eventWebHook = map[string]map[string]struct{}{
		types.EventMsgOffline:   {},
		types.EventMsgNotify:    {},
		types.EventOnlineStatus: {},
	}
)
