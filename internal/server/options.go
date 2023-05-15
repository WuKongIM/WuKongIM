package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/spf13/viper"
	"go.uber.org/zap/zapcore"
)

type Mode string

const (
	//debug 模式
	DebugMode Mode = "debug"
	// 正式模式
	ReleaseMode Mode = "release"
	// 压力测试模式
	BenchMode Mode = "bench"
	// TestMode indicates gin mode is test.
	TestMode = "test"
)

type Options struct {
	NodeID         int64            // 节点ID
	Proto          wkproto.Protocol // 狸猫IM protocol
	DataDir        string           // 数据目录
	Version        string
	HTTPAddr       string // http api的监听地址 默认为 0.0.0.0:1516
	Addr           string // tcp监听地址 例如：tcp://0.0.0.0:7677
	WSSOn          int    // 是否开启wss
	WSSAddr        string // websocket 监听地址 例如： 0.0.0.0:2122
	UnitTest       bool   // 是否开启单元测试
	HandlePoolSize int
	WriteTimeout   time.Duration // 写超时
	Mode           Mode          // 模式 debug 测试 release 正式 bench 压力测试
	vp             *viper.Viper  // 内部配置对象
	Logger         struct {
		Dir     string // 日志存储目录
		Level   zapcore.Level
		LineNum bool // 是否显示代码行数
	}
	ClientSendRateEverySecond int           // 客户端每秒发送消息的速率
	ConnIdleTime              time.Duration // ping的间隔
	TimingWheelTick           time.Duration // The time-round training interval must be 1ms or more
	TimingWheelSize           int64         // Time wheel size

	ConnFrameQueueMaxSize int // conn frame queue max size

	Webhook       string // webhook 通过此地址通知数据给第三方 格式为 http://xxxxx
	WebhookGRPC   string // webhook的grpc地址 如果此地址有值 则不会再调用webhook,格式为 ip:port
	EventPoolSize int    // 事件协程池大小,此池主要处理im的一些通知事件 比如webhook，上下线等等 默认为1024

	TmpChannelCacheCount    int    // 临时频道缓存数量
	ChannelCacheCount       int    // 频道缓存数量
	TmpChannelSuffix        string // Temporary channel suffix
	CreateIfChannelNotExist bool   // If the channel does not exist, whether to create it automatically
	MonitorOn               bool   // monitor on
	Datasource              string
	DatasourceChannelInfoOn bool // 数据源是否开启频道信息获取

	WhitelistOffOfPerson int

	DeliveryMsgPoolSize int // 投递消息协程池大小，此池的协程主要用来将消息投递给在线用户 默认大小为 10240

	MsgTimeout          time.Duration // Message sending timeout time, after this time it will try again
	TimeoutScanInterval time.Duration // 每隔多久扫描一次超时队列，看超时队列里是否有需要重试的消息

	SubscriberCompressOfCount int           // 订阅者数组多大开始压缩（离线推送的时候订阅者数组太大 可以设置此参数进行压缩 默认为0 表示不压缩 ）
	MessageNotifyScanInterval time.Duration // 消息推送间隔 默认500毫秒发起一次推送
	MessageNotifyMaxCount     int           // 每次webhook推送消息数量限制 默认一次请求最多推送100条
	MessageMaxRetryCount      int           // 消息最大重试次数
	// ---------- conversation ----------
	ConversationCacheExpire    int // 最近会话缓存过期时间 单位秒
	ConversationSyncInterval   time.Duration
	ConversationSyncOnce       int // 当多少最近会话数量发送变化就保存一次
	ConversationOfUserMaxCount int // 每个用户最大最近会话数量 默认为500

}

func NewOptions() *Options {

	return &Options{
		Proto:           wkproto.New(),
		HandlePoolSize:  2048,
		Version:         "5.0.0",
		TimingWheelTick: time.Millisecond * 10,
		TimingWheelSize: 100,
		Logger: struct {
			Dir     string
			Level   zapcore.Level
			LineNum bool
		}{
			Dir:     "",
			Level:   zapcore.InfoLevel,
			LineNum: false,
		},
		HTTPAddr:                   "0.0.0.0:1516",
		Addr:                       "tcp://0.0.0.0:7677",
		WSSAddr:                    "0.0.0.0:2122",
		WriteTimeout:               time.Second * 5,
		ClientSendRateEverySecond:  0,
		ConnIdleTime:               time.Minute * 3,
		ConnFrameQueueMaxSize:      250,
		TmpChannelCacheCount:       500,
		ChannelCacheCount:          1000,
		TmpChannelSuffix:           "@tmp",
		CreateIfChannelNotExist:    false,
		DatasourceChannelInfoOn:    false,
		ConversationCacheExpire:    60 * 60 * 24 * 1, // 1天过期
		ConversationSyncInterval:   time.Minute * 5,
		ConversationSyncOnce:       100,
		ConversationOfUserMaxCount: 500,
		DeliveryMsgPoolSize:        10240,
		EventPoolSize:              1024,
		MsgTimeout:                 time.Second * 60,
		TimeoutScanInterval:        time.Second * 5,
		MessageNotifyScanInterval:  time.Millisecond * 500,
		MessageNotifyMaxCount:      100,
		MessageMaxRetryCount:       5,
	}
}

func (o *Options) ConfigureWithViper(vp *viper.Viper) {
	o.vp = vp
	o.Mode = Mode(o.getString("mode", string(ReleaseMode)))
	o.configureLog(vp)   // 日志配置
	o.configureDataDir() // 数据目录

}

func (o *Options) configureDataDir() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	// 数据目录
	if o.NodeID == 0 {
		o.DataDir = o.getString("dataDir", filepath.Join(homeDir, "wukongimdata"))
	} else {
		o.DataDir = o.getString("dataDir", filepath.Join(homeDir, fmt.Sprintf("wukongimdata-%d", o.NodeID)))
	}
	if strings.TrimSpace(o.DataDir) != "" {
		err = os.MkdirAll(o.DataDir, 0755)
		if err != nil {
			panic(err)
		}
	}
}

func (o *Options) configureLog(vp *viper.Viper) {
	logLevel := vp.GetInt("logger.level")
	// level
	if logLevel == 0 { // 没有设置
		if o.Mode == DebugMode {
			logLevel = int(zapcore.DebugLevel)
		} else {
			logLevel = int(zapcore.InfoLevel)
		}
	}
	o.Logger.Level = zapcore.Level(logLevel)
	o.Logger.Dir = vp.GetString("logger.dir")
	o.Logger.LineNum = vp.GetBool("logger.lineNum")
}

func (o *Options) getString(key string, defaultValue string) string {
	v := o.vp.GetString(key)
	if v == "" {
		return defaultValue
	}
	return v
}

func (o *Options) getInt(key string, defaultValue int) int {
	v := o.vp.GetInt(key)
	if v == 0 {
		return defaultValue
	}
	return v
}
func (o *Options) getInt64(key string, defaultValue int64) int64 {
	v := o.vp.GetInt64(key)
	if v == 0 {
		return defaultValue
	}
	return v
}

func (o *Options) getDuration(key string, defaultValue time.Duration) time.Duration {
	v := o.vp.GetDuration(key)
	if v == 0 {
		return defaultValue
	}
	return v
}

// WebhookOn WebhookOn
func (o *Options) WebhookOn() bool {
	return strings.TrimSpace(o.Webhook) != "" || o.WebhookGRPCOn()
}

// WebhookGRPCOn 是否配置了webhook grpc地址
func (o *Options) WebhookGRPCOn() bool {
	return strings.TrimSpace(o.WebhookGRPC) != ""
}

// HasDatasource 是否有配置数据源
func (o *Options) HasDatasource() bool {
	return strings.TrimSpace(o.Datasource) != ""
}

// 获取客服频道的访客id
func (o *Options) GetCustomerServiceVisitorUID(channelID string) (string, bool) {
	if !strings.Contains(channelID, "|") {
		return "", false
	}
	channelIDs := strings.Split(channelID, "|")
	return channelIDs[0], true
}

// IsFakeChannel 是fake频道
func (o *Options) IsFakeChannel(channelID string) bool {
	return strings.Contains(channelID, "@")
}
