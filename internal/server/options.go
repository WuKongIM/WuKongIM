package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/lmproto"
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
)

type Options struct {
	NodeID         int64            // 节点ID
	Proto          lmproto.Protocol // 狸猫IM protocol
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
}

func NewOptions() *Options {

	return &Options{
		Proto:           lmproto.New(),
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
		HTTPAddr:                  "0.0.0.0:1516",
		Addr:                      "tcp://0.0.0.0:7677",
		WSSAddr:                   "0.0.0.0:2122",
		WriteTimeout:              time.Second * 5,
		ClientSendRateEverySecond: 0,
		ConnIdleTime:              time.Minute * 3,
		ConnFrameQueueMaxSize:     250,
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
		o.DataDir = o.getString("dataDir", filepath.Join(homeDir, "limaodata"))
	} else {
		o.DataDir = o.getString("dataDir", filepath.Join(homeDir, fmt.Sprintf("limaodata-%d", o.NodeID)))
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
