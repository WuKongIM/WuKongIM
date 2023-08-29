package logicclient

import (
	"github.com/WuKongIM/WuKongIM/internal/pb"
)

type Client interface {
	// Connect() error
	// Auth(req *pb.AuthReq) (*pb.AuthResp, error)
	// GatewayReset() error
	// Append(data []byte) (int, error)
	// AddClientConn(clientConn ClientConn)

	ClientAuth(req *pb.AuthReq) (*pb.AuthResp, error)

	// 写数据到logic
	WriteToLogic(conn *pb.Conn, data []byte) (int, error)

	// 监听logic写过来的数据
	// ListenDataFromLogic(f func(conn *pb.Conn, data []byte))

	ClientClose(r *pb.ClientCloseReq) error

	ListenWriteFromLogic(f func(conn *pb.Conn, data []byte) error)

	Start()

	Stop()

	// ClientAuth(req *pb.AuthReq) (*pb.AuthResp, error)

	// ClientClose(req *pb.ClientCloseReq) error

	// GatewayReset(req *pb.ResetReq) error

	// GatewayClientWrite(data []byte) error

	// Append(data []byte) error

	// GetClusterConfig() *pb.ClusterConfig

	// Start() error

	// Stop()
}
