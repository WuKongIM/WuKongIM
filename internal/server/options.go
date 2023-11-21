package server

import (
	"fmt"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wknet/crypto/tls"
	"github.com/pkg/errors"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/WuKongIM/WuKongIM/version"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/spf13/cast"
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
	vp          *viper.Viper // 内部配置对象
	ID          int64        // 节点ID
	Mode        Mode         // 模式 debug 测试 release 正式 bench 压力测试
	HTTPAddr    string       // http api的监听地址 默认为 0.0.0.0:5001
	Addr        string       // tcp监听地址 例如：tcp://0.0.0.0:5100
	RootDir     string       // 根目录
	DataDir     string       // 数据目录
	GinMode     string       // gin框架的模式
	WSAddr      string       // websocket 监听地址 例如：ws://0.0.0.0:5200
	WSSAddr     string       // wss 监听地址 例如：wss://0.0.0.0:5210
	WSTLSConfig *tls.Config
	WSSConfig   struct { // wss的证书配置
		CertFile string // 证书文件
		KeyFile  string // 私钥文件
	}

	Logger struct {
		Dir     string // 日志存储目录
		Level   zapcore.Level
		LineNum bool // 是否显示代码行数
	}
	Monitor struct {
		On   bool   // 是否开启监控
		Addr string // 监控地址 默认为 0.0.0.0:5300
	}
	Demo struct {
		On   bool   // 是否开启demo
		Addr string // demo服务地址 默认为 0.0.0.0:5172
	}
	External struct {
		IP          string // 外网IP
		TCPAddr     string // 节点的TCP地址 对外公开，APP端长连接通讯  格式： ip:port
		WSAddr      string //  节点的wsAdd地址 对外公开 WEB端长连接通讯 格式： ws://ip:port
		WSSAddr     string // 节点的wssAddr地址 对外公开 WEB端长连接通讯 格式： wss://ip:port
		MonitorAddr string // 对外访问的监控地址
		APIUrl      string // 对外访问的API基地址 格式: http://ip:port
	}
	Channel struct { // 频道配置
		CacheCount                int  // 频道缓存数量
		CreateIfNoExist           bool // 如果频道不存在是否创建
		SubscriberCompressOfCount int  // 订订阅者数组多大开始压缩（离线推送的时候订阅者数组太大 可以设置此参数进行压缩 默认为0 表示不压缩 ）

	}
	TmpChannel struct { // 临时频道配置
		Suffix     string // 临时频道的后缀
		CacheCount int    // 临时频道缓存数量
	}
	Webhook struct { // 两者配其一即可
		HTTPAddr                    string        // webhook的http地址 通过此地址通知数据给第三方 格式为 http://xxxxx
		GRPCAddr                    string        //  webhook的grpc地址 如果此地址有值 则不会再调用HttpAddr配置的地址,格式为 ip:port
		MsgNotifyEventPushInterval  time.Duration // 消息通知事件推送间隔，默认500毫秒发起一次推送
		MsgNotifyEventCountPerPush  int           // 每次webhook消息通知事件推送消息数量限制 默认一次请求最多推送100条
		MsgNotifyEventRetryMaxCount int           // 消息通知事件消息推送失败最大重试次数 默认为5次，超过将丢弃
	}
	Datasource struct { // 数据源配置，不填写则使用自身数据存储逻辑，如果填写则使用第三方数据源，数据格式请查看文档
		Addr          string // 数据源地址
		ChannelInfoOn bool   // 是否开启频道信息获取
	}
	Conversation struct {
		On           bool          // 是否开启最近会话
		CacheExpire  time.Duration // 最近会话缓存过期时间 (这个是热数据缓存时间，并非最近会话数据的缓存时间)
		SyncInterval time.Duration // 最近会话同步间隔
		SyncOnce     int           //  当多少最近会话数量发送变化就保存一次
		UserMaxCount int           // 每个用户最大最近会话数量 默认为500
	}
	ManagerToken   string // 管理者的token
	ManagerUID     string // 管理者的uid
	ManagerTokenOn bool   // 管理者的token是否开启

	Proto wkproto.Protocol // 悟空IM protocol

	Version string

	UnitTest       bool // 是否开启单元测试
	HandlePoolSize int

	ConnIdleTime    time.Duration // 连接空闲时间 超过此时间没数据传输将关闭
	TimingWheelTick time.Duration // The time-round training interval must be 1ms or more
	TimingWheelSize int64         // Time wheel size

	UserMsgQueueMaxSize int // 用户消息队列最大大小，超过此大小此用户将被限速，0为不限制

	TokenAuthOn bool // 是否开启token验证 不配置将根据mode属性判断 debug模式下默认为false release模式为true

	EventPoolSize int // 事件协程池大小,此池主要处理im的一些通知事件 比如webhook，上下线等等 默认为1024

	WhitelistOffOfPerson int
	DeliveryMsgPoolSize  int // 投递消息协程池大小，此池的协程主要用来将消息投递给在线用户 默认大小为 10240

	MessageRetry struct {
		Interval     time.Duration // 消息重试间隔，如果消息发送后在此间隔内没有收到ack，将会在此间隔后重新发送
		MaxCount     int           // 消息最大重试次数
		ScanInterval time.Duration //  每隔多久扫描一次超时队列，看超时队列里是否有需要重试的消息
	}

	SlotNum int // 槽数量

	// MsgRetryInterval     time.Duration // Message sending timeout time, after this time it will try again
	// MessageMaxRetryCount int           // 消息最大重试次数
	// TimeoutScanInterval time.Duration // 每隔多久扫描一次超时队列，看超时队列里是否有需要重试的消息

}

func NewOptions() *Options {

	// http.ServeTLS(l net.Listener, handler Handler, certFile string, keyFile string)

	homeDir, err := GetHomeDir()
	if err != nil {
		panic(err)
	}
	return &Options{
		Proto:           wkproto.New(),
		HandlePoolSize:  2048,
		Version:         version.Version,
		TimingWheelTick: time.Millisecond * 10,
		TimingWheelSize: 100,
		GinMode:         gin.ReleaseMode,
		RootDir:         path.Join(homeDir, "wukongim"),
		ManagerUID:      "____manager",
		Logger: struct {
			Dir     string
			Level   zapcore.Level
			LineNum bool
		}{
			Dir:     "",
			Level:   zapcore.InfoLevel,
			LineNum: false,
		},
		HTTPAddr:            "0.0.0.0:5001",
		Addr:                "tcp://0.0.0.0:5100",
		WSAddr:              "ws://0.0.0.0:5200",
		WSSAddr:             "",
		ConnIdleTime:        time.Minute * 3,
		UserMsgQueueMaxSize: 0,
		TmpChannel: struct {
			Suffix     string
			CacheCount int
		}{
			Suffix:     "@tmp",
			CacheCount: 500,
		},
		Channel: struct {
			CacheCount                int
			CreateIfNoExist           bool
			SubscriberCompressOfCount int
		}{
			CacheCount:                1000,
			CreateIfNoExist:           true,
			SubscriberCompressOfCount: 0,
		},
		Datasource: struct {
			Addr          string
			ChannelInfoOn bool
		}{
			Addr:          "",
			ChannelInfoOn: false,
		},
		TokenAuthOn: false,
		Conversation: struct {
			On           bool
			CacheExpire  time.Duration
			SyncInterval time.Duration
			SyncOnce     int
			UserMaxCount int
		}{
			On:           true,
			CacheExpire:  time.Hour * 24 * 1, // 1天过期
			UserMaxCount: 1000,
			SyncInterval: time.Minute * 5,
			SyncOnce:     100,
		},
		DeliveryMsgPoolSize: 10240,
		EventPoolSize:       1024,
		MessageRetry: struct {
			Interval     time.Duration
			MaxCount     int
			ScanInterval time.Duration
		}{
			Interval:     time.Second * 60,
			ScanInterval: time.Second * 5,
			MaxCount:     5,
		},
		Webhook: struct {
			HTTPAddr                    string
			GRPCAddr                    string
			MsgNotifyEventPushInterval  time.Duration
			MsgNotifyEventCountPerPush  int
			MsgNotifyEventRetryMaxCount int
		}{
			MsgNotifyEventPushInterval:  time.Millisecond * 500,
			MsgNotifyEventCountPerPush:  100,
			MsgNotifyEventRetryMaxCount: 5,
		},
		Monitor: struct {
			On   bool
			Addr string
		}{
			On:   true,
			Addr: "0.0.0.0:5300",
		},
		Demo: struct {
			On   bool
			Addr string
		}{
			On:   true,
			Addr: "0.0.0.0:5172",
		},
		SlotNum: 256,
	}
}

func GetHomeDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err == nil {
		return homeDir, nil
	}
	u, err := user.Current()
	if err == nil {
		return u.HomeDir, nil
	}

	return "", errors.New("User home directory not found.")
}

func (o *Options) ConfigureWithViper(vp *viper.Viper) {
	o.vp = vp
	// o.ID = o.getInt64("id", o.ID)

	o.RootDir = o.getString("rootDir", o.RootDir)

	o.Mode = Mode(o.getString("mode", string(DebugMode)))
	o.GinMode = o.getString("ginMode", o.GinMode)

	o.HTTPAddr = o.getString("httpAddr", o.HTTPAddr)
	o.Addr = o.getString("addr", o.Addr)

	o.ManagerToken = o.getString("managerToken", o.ManagerToken)
	o.ManagerUID = o.getString("managerUID", o.ManagerUID)

	if strings.TrimSpace(o.ManagerToken) != "" {
		o.ManagerTokenOn = true
	}

	o.External.IP = o.getString("external.ip", o.External.IP)
	o.External.TCPAddr = o.getString("external.tcpAddr", o.External.TCPAddr)
	o.External.WSAddr = o.getString("external.wsAddr", o.External.WSAddr)
	o.External.WSSAddr = o.getString("external.wssAddr", o.External.WSSAddr)
	o.External.MonitorAddr = o.getString("external.monitorAddr", o.External.MonitorAddr)
	o.External.APIUrl = o.getString("external.apiUrl", o.External.APIUrl)

	o.Monitor.On = o.getBool("monitor.on", o.Monitor.On)
	o.Monitor.Addr = o.getString("monitor.addr", o.Monitor.Addr)

	o.Demo.On = o.getBool("demo.on", o.Demo.On)
	o.Demo.Addr = o.getString("demo.addr", o.Demo.Addr)

	o.WSAddr = o.getString("wsAddr", o.WSAddr)
	o.WSSAddr = o.getString("wssAddr", o.WSSAddr)

	o.WSSConfig.CertFile = o.getString("wssConfig.certFile", o.WSSConfig.CertFile)
	o.WSSConfig.KeyFile = o.getString("wssConfig.keyFile", o.WSSConfig.KeyFile)

	o.Channel.CacheCount = o.getInt("channel.cacheCount", o.Channel.CacheCount)
	o.Channel.CreateIfNoExist = o.getBool("channel.createIfNoExist", o.Channel.CreateIfNoExist)
	o.Channel.SubscriberCompressOfCount = o.getInt("channel.subscriberCompressOfCount", o.Channel.SubscriberCompressOfCount)

	o.ConnIdleTime = o.getDuration("connIdleTime", o.ConnIdleTime)

	o.TimingWheelTick = o.getDuration("timingWheelTick", o.TimingWheelTick)
	o.TimingWheelSize = o.getInt64("timingWheelSize", o.TimingWheelSize)

	o.UserMsgQueueMaxSize = o.getInt("userMsgQueueMaxSize", o.UserMsgQueueMaxSize)

	o.TokenAuthOn = o.getBool("tokenAuthOn", o.TokenAuthOn)

	o.UnitTest = o.vp.GetBool("unitTest")

	o.Webhook.GRPCAddr = o.getString("webhook.grpcAddr", o.Webhook.GRPCAddr)
	o.Webhook.HTTPAddr = o.getString("webhook.httpAddr", o.Webhook.HTTPAddr)
	o.Webhook.MsgNotifyEventRetryMaxCount = o.getInt("webhook.msgNotifyEventRetryMaxCount", o.Webhook.MsgNotifyEventRetryMaxCount)
	o.Webhook.MsgNotifyEventCountPerPush = o.getInt("webhook.msgNotifyEventCountPerPush", o.Webhook.MsgNotifyEventCountPerPush)
	o.Webhook.MsgNotifyEventPushInterval = o.getDuration("webhook.msgNotifyEventPushInterval", o.Webhook.MsgNotifyEventPushInterval)

	o.EventPoolSize = o.getInt("eventPoolSize", o.EventPoolSize)
	o.DeliveryMsgPoolSize = o.getInt("deliveryMsgPoolSize", o.DeliveryMsgPoolSize)
	o.HandlePoolSize = o.getInt("handlePoolSize", o.HandlePoolSize)

	o.TmpChannel.CacheCount = o.getInt("tmpChannel.cacheCount", o.TmpChannel.CacheCount)
	o.TmpChannel.Suffix = o.getString("tmpChannel.suffix", o.TmpChannel.Suffix)

	o.Datasource.Addr = o.getString("datasource.addr", o.Datasource.Addr)
	o.Datasource.ChannelInfoOn = o.getBool("datasource.channelInfoOn", o.Datasource.ChannelInfoOn)

	o.WhitelistOffOfPerson = o.getInt("whitelistOffOfPerson", o.WhitelistOffOfPerson)

	o.MessageRetry.Interval = o.getDuration("messageRetry.interval", o.MessageRetry.Interval)
	o.MessageRetry.ScanInterval = o.getDuration("messageRetry.scanInterval", o.MessageRetry.ScanInterval)
	o.MessageRetry.MaxCount = o.getInt("messageRetry.maxCount", o.MessageRetry.MaxCount)

	o.Conversation.On = o.getBool("conversation.on", o.Conversation.On)
	o.Conversation.CacheExpire = o.getDuration("conversation.cacheExpire", o.Conversation.CacheExpire)
	o.Conversation.SyncInterval = o.getDuration("conversation.syncInterval", o.Conversation.SyncInterval)
	o.Conversation.SyncOnce = o.getInt("conversation.syncOnce", o.Conversation.SyncOnce)
	o.Conversation.UserMaxCount = o.getInt("conversation.userMaxCount", o.Conversation.UserMaxCount)

	o.SlotNum = o.getInt("slotNum", o.SlotNum)

	if o.WSSConfig.CertFile != "" && o.WSSConfig.KeyFile != "" {
		certificate, err := tls.LoadX509KeyPair(o.WSSConfig.CertFile, o.WSSConfig.KeyFile)
		if err != nil {
			panic(err)
		}
		o.WSTLSConfig = &tls.Config{
			Certificates: []tls.Certificate{
				certificate,
			},
		}
	}

	o.configureDataDir() // 数据目录
	o.configureLog(vp)   // 日志配置

	ip := o.External.IP
	if strings.TrimSpace(ip) == "" {
		ip = getIntranetIP()
	}
	if strings.TrimSpace(o.External.TCPAddr) == "" {
		addrPairs := strings.Split(o.Addr, ":")
		portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)

		var err error

		if err != nil {
			panic(err)
		}

		o.External.TCPAddr = fmt.Sprintf("%s:%d", ip, portInt64)
	}
	if strings.TrimSpace(o.External.WSAddr) == "" {
		addrPairs := strings.Split(o.WSAddr, ":")
		portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)
		o.External.WSAddr = fmt.Sprintf("%s://%s:%d", addrPairs[0], ip, portInt64)
	}
	if strings.TrimSpace(o.WSSAddr) != "" && strings.TrimSpace(o.External.WSSAddr) == "" {
		addrPairs := strings.Split(o.WSSAddr, ":")
		portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)
		o.External.WSSAddr = fmt.Sprintf("%s://%s:%d", addrPairs[0], ip, portInt64)
	}

	if strings.TrimSpace(o.External.MonitorAddr) == "" {
		addrPairs := strings.Split(o.Monitor.Addr, ":")
		portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)
		o.External.MonitorAddr = fmt.Sprintf("%s:%d", ip, portInt64)
	}

	if strings.TrimSpace(o.External.APIUrl) == "" {
		addrPairs := strings.Split(o.HTTPAddr, ":")
		portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)
		o.External.APIUrl = fmt.Sprintf("http://%s:%d", ip, portInt64)
	}

}

func (o *Options) configureDataDir() {

	// 数据目录
	o.DataDir = o.getString("dataDir", filepath.Join(o.RootDir, "data"))

	if strings.TrimSpace(o.DataDir) != "" {
		err := os.MkdirAll(o.DataDir, 0755)
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
	} else {
		logLevel = logLevel - 2
	}
	o.Logger.Level = zapcore.Level(logLevel)
	o.Logger.Dir = vp.GetString("logger.dir")
	if strings.TrimSpace(o.Logger.Dir) == "" {
		o.Logger.Dir = "logs"
	}
	if !strings.HasPrefix(strings.TrimSpace(o.Logger.Dir), "/") {
		o.Logger.Dir = filepath.Join(o.RootDir, o.Logger.Dir)
	}
	o.Logger.LineNum = vp.GetBool("logger.lineNum")
}

// IsTmpChannel 是否是临时频道
func (o *Options) IsTmpChannel(channelID string) bool {
	return strings.HasSuffix(channelID, o.TmpChannel.Suffix)
}

func (o *Options) ConfigFileUsed() string {
	return o.vp.ConfigFileUsed()
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

func (o *Options) getBool(key string, defaultValue bool) bool {
	objV := o.vp.Get(key)
	if objV == nil {
		return defaultValue
	}
	return cast.ToBool(objV)
}

// func (o *Options) isSet(key string) bool {
// 	return o.vp.IsSet(key)
// }

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
	return strings.TrimSpace(o.Webhook.HTTPAddr) != "" || o.WebhookGRPCOn()
}

// WebhookGRPCOn 是否配置了webhook grpc地址
func (o *Options) WebhookGRPCOn() bool {
	return strings.TrimSpace(o.Webhook.GRPCAddr) != ""
}

// HasDatasource 是否有配置数据源
func (o *Options) HasDatasource() bool {
	return strings.TrimSpace(o.Datasource.Addr) != ""
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

// 获取内网地址
func getIntranetIP() string {
	intranetIPs, err := wkutil.GetIntranetIP()
	if err != nil {
		panic(err)
	}
	if len(intranetIPs) > 0 {
		return intranetIPs[0]
	}
	return ""
}
