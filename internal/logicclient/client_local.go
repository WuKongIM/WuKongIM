package logicclient

import (
	"github.com/WuKongIM/WuKongIM/internal/pb"
)

type LocalClient struct {
}

func NewLocalClient() *LocalClient {

	return &LocalClient{}
}

func (l *LocalClient) ClientAuth(req *pb.AuthReq) (*pb.AuthResp, error) {

	return localServer.OnGatewayClientAuth(req)
}

func (l *LocalClient) WriteToLogic(conn *pb.Conn, data []byte) (int, error) {
	return localServer.OnGatewayClientWrite(conn, data)
}

func (l *LocalClient) ListenWriteFromLogic(f func(conn *pb.Conn, data []byte) error) {
	onWriteFromLogic = f
}

// func (l *LocalClient) ListenDataFromLogic(f func(conn *pb.Conn, data []byte) error) {
// 	onDataFromLogic = f
// }

func (l *LocalClient) ClientClose(r *pb.ClientCloseReq) error {

	return localServer.OnGatewayClientClose(r)
}

func (l *LocalClient) Start() {

}

func (l *LocalClient) Stop() {

}

var onWriteFromLogic func(conn *pb.Conn, data []byte) error

func OnWriteFromLogic(conn *pb.Conn, data []byte) error {
	if onWriteFromLogic != nil {
		return onWriteFromLogic(conn, data)
	}
	return nil
}

type LocalServer interface {
	OnGatewayClientAuth(req *pb.AuthReq) (*pb.AuthResp, error)
	OnGatewayClientWrite(conn *pb.Conn, data []byte) (int, error)
	OnGatewayClientClose(r *pb.ClientCloseReq) error
}

var localServer LocalServer

func SetLocalServer(s LocalServer) {
	localServer = s
}
