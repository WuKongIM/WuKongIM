package gateway

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wknet/crypto/tls"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type Options struct {
	NodeID      uint64 // 节点ID
	Addr        string // tcp监听地址 例如：tcp://0.0.0.0:5100
	WSAddr      string // websocket 监听地址 例如：ws://0.0.0.0:5200
	WSSAddr     string // wss 监听地址 例如：wss://0.0.0.0:5210
	WSTLSConfig *tls.Config
	WSSConfig   struct { // wss的证书配置
		CertFile string // 证书文件
		KeyFile  string // 私钥文件
	}
	Proto                       wkproto.Protocol // 协议
	ManagerUID                  string           // 管理者的uid
	TimingWheelTick             time.Duration    // The time-round training interval must be 1ms or more
	TimingWheelSize             int64            // Time wheel size
	ConnIdleTime                time.Duration
	NodeMaxTransmissionCapacity int // 节点最大传输能力 单位byte
}

func NewOptions() *Options {
	return &Options{
		Addr:                        "tcp://0.0.0.0:5100",
		WSAddr:                      "ws://0.0.0.0:5200",
		WSSAddr:                     "",
		TimingWheelTick:             time.Millisecond * 10,
		TimingWheelSize:             100,
		Proto:                       wkproto.New(),
		ConnIdleTime:                3 * time.Minute,
		NodeMaxTransmissionCapacity: 1024 * 1024 * 1024, // 1G
	}
}
