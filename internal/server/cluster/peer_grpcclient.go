package cluster

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/server/cluster/pb"
	"github.com/WuKongIM/WuKongIM/internal/server/cluster/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/grpcpool"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

type PeerGRPCClient struct {
	wklog.Log
	callTimeout time.Duration
	// c              *Cluster
	rpcPoolMap     map[string]*grpcpool.Pool
	rpcPoolMapLock sync.RWMutex
	peerMap        map[uint64]*pb.Peer
	peerMapLock    sync.RWMutex
}

func NewPeerGRPCClient() *PeerGRPCClient {
	return &PeerGRPCClient{
		Log:         wklog.NewWKLog("PeerGRPCClient"),
		callTimeout: time.Second * 3,
		rpcPoolMap:  map[string]*grpcpool.Pool{},
		peerMap:     map[uint64]*pb.Peer{},
	}
}

// SendCMD 发送cmd给指定节点
func (p *PeerGRPCClient) SendCMD(ctx context.Context, peerID uint64, cmd *rpc.CMDReq) (*rpc.CMDResp, error) {
	connCtx, connCancel := context.WithTimeout(context.Background(), time.Second*6)
	conn, err := p.getPeerConn(connCtx, peerID)
	connCancel()
	if err != nil {
		p.Error("获取grpc连接失败！", zap.Error(err), zap.Uint64("peerID", peerID))
		return nil, err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			p.Error("关闭grpc失败！", zap.Error(err), zap.Uint64("peerID", peerID))
		}
	}()
	client := rpc.NewNodeServiceClient(conn)
	resp, err := client.SendCMD(ctx, cmd)
	if err != nil {
		conn.ErrorCountInc()
		p.Error("发送grpc cmd失败！", zap.Error(err), zap.Uint64("peerID", peerID))
		return nil, err
	}
	conn.ErrorCountReset()

	return resp, nil
}

func (p *PeerGRPCClient) getPeerConn(ctx context.Context, peerID uint64) (*grpcpool.ClientConn, error) {
	peer := p.getPeerByID(peerID)
	if peer == nil {
		p.Error("节点不存在！", zap.Uint64("peerID", peerID))
		return nil, errors.New("节点不存在！")
	}

	conn, err := p.getGrpcConn(ctx, peer.GrpcServerAddr)
	return conn, err
}

func (p *PeerGRPCClient) getPeerByID(peerID uint64) *pb.Peer {
	p.peerMapLock.RLock()
	peer := p.peerMap[peerID]
	p.peerMapLock.RUnlock()
	return peer
}

func (p *PeerGRPCClient) AddOrUpdatePeer(peer *pb.Peer) {
	p.peerMapLock.Lock()
	p.peerMap[peer.PeerID] = peer
	p.peerMapLock.Unlock()
}

func (p *PeerGRPCClient) RemovePeer(peerID uint64) {
	p.peerMapLock.Lock()
	delete(p.peerMap, peerID)
	p.peerMapLock.Unlock()
}

func (p *PeerGRPCClient) getGrpcConn(ctx context.Context, addr string) (*grpcpool.ClientConn, error) {
	p.rpcPoolMapLock.RLock()
	pool := p.rpcPoolMap[addr]

	if pool != nil {
		p.rpcPoolMapLock.RUnlock()
		return pool.Get(ctx)
	}
	p.rpcPoolMapLock.RUnlock()

	var err error
	pool, err = grpcpool.New(func() (*grpc.ClientConn, error) {
		return grpc.Dial(addr, grpc.WithInsecure(), grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second, // send pings every 10 seconds if there is no activity
			Timeout:             time.Second * 5,  // wait 3 second for ping ack before considering the connection dead
			PermitWithoutStream: true,             // send pings even without active streams
		}))
	}, 2, 10, time.Minute*5) // 初始化2个连接 最多10个连接 (连接不够会出现 connect: connection refused 类似错误)
	if err != nil {
		return nil, err
	}
	p.rpcPoolMapLock.Lock()
	p.rpcPoolMap[addr] = pool
	p.rpcPoolMapLock.Unlock()

	return pool.Get(ctx)
}
