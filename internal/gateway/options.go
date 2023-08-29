package gateway

import (
	"fmt"
	"path"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wknet/crypto/tls"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type NodeID uint64

func (n NodeID) String() string {
	return fmt.Sprintf("%d", n)
}

type Options struct {
	NodeID      NodeID // 节点ID
	Addr        string // tcp监听地址 例如：tcp://0.0.0.0:5100
	WSAddr      string // websocket 监听地址 例如：ws://0.0.0.0:5200
	WSSAddr     string // wss 监听地址 例如：wss://0.0.0.0:5210
	WSTLSConfig *tls.Config
	WSSConfig   struct { // wss的证书配置
		CertFile string // 证书文件
		KeyFile  string // 私钥文件
	}
	RootDir                     string
	Proto                       wkproto.Protocol // 协议
	ManagerUID                  string           // 管理者的uid
	TimingWheelTick             time.Duration    // The time-round training interval must be 1ms or more
	TimingWheelSize             int64            // Time wheel size
	ConnIdleTime                time.Duration
	NodeMaxTransmissionCapacity int    // 节点最大传输能力 单位byte
	Slot                        uint32 // 节点槽数量
	ClusterStorePath            string
	BootstrapNodeAddr           string // 引导节点地址
	LocalID                     string
	LogicAddr                   string
}

func NewOptions() *Options {
	rootDir := "gatewaydata"
	return &Options{
		RootDir:                     rootDir,
		Addr:                        "tcp://0.0.0.0:5100",
		WSAddr:                      "ws://0.0.0.0:5200",
		WSSAddr:                     "",
		TimingWheelTick:             time.Millisecond * 10,
		TimingWheelSize:             100,
		Proto:                       wkproto.New(),
		ConnIdleTime:                3 * time.Minute,
		NodeMaxTransmissionCapacity: 1024 * 1024 * 1024, // 1G
		Slot:                        256,
		ClusterStorePath:            path.Join(rootDir, "clusterconfig"),
		BootstrapNodeAddr:           "",
		LocalID:                     "local",
		LogicAddr:                   "",
	}
}

func (o *Options) Local() bool {
	return o.NodeID == 0
}

func (o *Options) GatewayID() string {
	if o.Local() {
		return o.LocalID
	}
	return fmt.Sprintf("%d@gateway", o.NodeID)
}
