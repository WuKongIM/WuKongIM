package logic

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/internal/gateway/proto"
	"github.com/WuKongIM/WuKongIM/internal/gatewaycommon"
	"github.com/WuKongIM/WuKongIM/internal/logicclient"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type ClientAuthListener func(r *pb.AuthReq) (*pb.AuthResp, error)

type ClientWriteListener func(conn *pb.Conn, data []byte) (int, error)

type ClientCloseListener func(r *pb.ClientCloseReq) error

type gatewayClient struct {
	gatewayID   string
	local       bool
	connManager gatewaycommon.ConnManager
	s           *Server
	blockSeq    atomic.Uint64
	blockProto  *proto.Proto
	wklog.Log
	clientAuthListener  ClientAuthListener
	clientWriteListener ClientWriteListener
	clientCloseListener ClientCloseListener
}

func newRemoteGatewayClient(gatewayID string, s *Server) *gatewayClient {

	g := &gatewayClient{
		gatewayID:   gatewayID,
		local:       false,
		s:           s,
		connManager: newClientConnManager(),
		blockProto:  proto.New(),
		Log:         wklog.NewWKLog("gatewayClient"),
	}
	g.ListenClientAuth(s.logic.OnGatewayClientAuth)
	g.ListenClientWrite(s.logic.OnGatewayClientWrite)
	g.ListenClientClose(s.logic.OnGatewayClientClose)
	return g
}

func newLocalGatewayClient(s *Server) *gatewayClient {
	g := &gatewayClient{
		local:       true,
		connManager: gatewaycommon.GetLocalGatewayServer().GetConnManager(),
		gatewayID:   s.opts.LocalID,
	}
	g.ListenClientAuth(s.logic.OnGatewayClientAuth)
	g.ListenClientWrite(s.logic.OnGatewayClientWrite)
	g.ListenClientClose(s.logic.OnGatewayClientClose)
	return g
}

func (g *gatewayClient) WriteToGateway(conn *pb.Conn, data []byte) error {
	if g.local {
		return logicclient.OnWriteFromLogic(conn, data)
	}
	blockData, err := g.blockProto.Encode(&proto.Block{
		Seq:  g.blockSeq.Inc(),
		Conn: conn,
		Data: data,
	})
	if err != nil {
		return err
	}

	err = g.s.writePool.Submit(func() {
		_, err = g.s.s.Request(g.gatewayID, "/logic/client/write", blockData)
		if err != nil {
			g.Debug("failed to the write to the gateway", zap.Error(err))
		}
	})
	// return g.cli.WriteToGateway(conn, data)
	return err
}

func (g *gatewayClient) GetConn(id int64) gatewaycommon.Conn {
	fmt.Println("connManager--->", g, id)
	return g.connManager.GetConn(id)
}
func (g *gatewayClient) GetConnsWithUID(uid string) []gatewaycommon.Conn {

	return g.connManager.GetConnsWithUID(uid)
}

func (g *gatewayClient) AddConn(conn gatewaycommon.Conn) {
	g.connManager.AddConn(conn)
}

func (g *gatewayClient) RemoveConnWithID(id int64) {
	g.connManager.RemoveConnWithID(id)
}

func (g *gatewayClient) ListenClientAuth(f ClientAuthListener) {
	g.clientAuthListener = f
}

func (g *gatewayClient) Reset() {
}

func (g *gatewayClient) ClientAuth(r *pb.AuthReq) (*pb.AuthResp, error) {
	return g.clientAuthListener(r)
}

func (g *gatewayClient) ListenClientWrite(f ClientWriteListener) {
	g.clientWriteListener = f
}

func (g *gatewayClient) ClientWrite(conn *pb.Conn, data []byte) (int, error) {
	if g.clientWriteListener == nil {
		return 0, nil
	}
	return g.clientWriteListener(conn, data)
}

func (g *gatewayClient) ListenClientClose(f ClientCloseListener) {
	g.clientCloseListener = f
}

func (g *gatewayClient) ClientClose(r *pb.ClientCloseReq) error {
	if g.clientCloseListener == nil {
		return nil
	}
	g.RemoveConnWithID(r.ConnID)
	return g.clientCloseListener(r)
}
