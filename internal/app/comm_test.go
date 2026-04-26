package app

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/stretchr/testify/require"
)

var appWKProtoClients sync.Map

type sendStressAcceptanceSpec struct {
	Benchmark                        sendStressConfig
	GatewaySendTimeout               time.Duration
	FollowerReplicationRetryInterval time.Duration
	AppendGroupCommitMaxWait         time.Duration
	AppendGroupCommitMaxRecords      int
	AppendGroupCommitMaxBytes        int
	DataPlanePoolSize                int
	DataPlaneMaxFetchInflight        int
	DataPlaneMaxPendingFetch         int
	MinISR                           int
}

func sendStressAcceptancePreset() sendStressAcceptanceSpec {
	return sendStressAcceptanceSpec{
		Benchmark: sendStressConfig{
			Mode:                 sendStressModeThroughput,
			Duration:             15 * time.Second,
			Workers:              16,
			Senders:              32,
			MessagesPerWorker:    50,
			DialTimeout:          3 * time.Second,
			AckTimeout:           20 * time.Second,
			MaxInflightPerWorker: 64,
			Seed:                 20260408,
		},
		GatewaySendTimeout:               25 * time.Second,
		FollowerReplicationRetryInterval: 250 * time.Millisecond,
		AppendGroupCommitMaxWait:         2 * time.Millisecond,
		AppendGroupCommitMaxRecords:      128,
		AppendGroupCommitMaxBytes:        256 * 1024,
		DataPlanePoolSize:                8,
		DataPlaneMaxFetchInflight:        16,
		DataPlaneMaxPendingFetch:         16,
		MinISR:                           2,
	}
}

func reserveTestTCPAddrs(t *testing.T, count int) map[uint64]string {
	t.Helper()

	addrs := make(map[uint64]string, count)
	for i := 0; i < count; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addrs[uint64(i+1)] = ln.Addr().String()
		require.NoError(t, ln.Close())
	}
	return addrs
}

func appWKProtoClientForConnErr(conn net.Conn) (*testkit.WKProtoClient, error) {
	if value, ok := appWKProtoClients.Load(conn); ok {
		return value.(*testkit.WKProtoClient), nil
	}
	client, err := testkit.NewWKProtoClient()
	if err != nil {
		return nil, err
	}
	appWKProtoClients.Store(conn, client)
	return client, nil
}
