package logic

import (
	bproto "github.com/WuKongIM/WuKongIM/internal/gateway/proto"
	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type gatewayHandler struct {
	s *Server
	wklog.Log
	proto *bproto.Proto
}

func newGatewayHandler(s *Server) *gatewayHandler {
	return &gatewayHandler{
		s:     s,
		Log:   wklog.NewWKLog("gatewayHandler"),
		proto: bproto.New(),
	}
}

func (g *gatewayHandler) initHandler() {

	wks := g.s.s

	wks.Route("/gateway/client/auth", g.handleClientAuth) // trigger client connection auth

	wks.Route("/gateway/client/close", g.handleClientClose) // trigger client connection write

	wks.Route("/gateway/client/write", g.handleGatewayConnWrite) // trigger when node connection write

	wks.Route("/gateway/reset", g.handleGatewayReset) // trigger when node restart

	wks.Route("/getclusterconfigset", g.handleGetClusterConfigSet)
}

func (g *gatewayHandler) handleClientAuth(c *wkserver.Context) {
	req := &pb.AuthReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	nodeConn := c.Conn()
	if nodeConn == nil {
		c.WriteErr(ErrNodeConnNotFound)
		return
	}

	gatewayID := nodeConn.UID()
	gateway := g.s.logic.gatewayManager.get(gatewayID)
	if gateway == nil {
		c.WriteErr(ErrNodeConnNotFound)
		return
	}

	authResp, err := gateway.ClientAuth(req)
	if err != nil {
		c.WriteErr(ErrNodeConnNotFound)
		return
	}

	cliConn := g.createClientConn(req)
	cliConn.SetValue(aesKeyKey, authResp.AesKey)
	cliConn.SetValue(aesIVKey, authResp.AesIV)

	gateway.AddConn(cliConn)

	data, _ := authResp.Marshal()
	c.Write(data)

}

func (g *gatewayHandler) handleClientClose(c *wkserver.Context) {
	req := &pb.ClientCloseReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	conn := c.Conn()
	gatewayID := conn.UID()

	gateway := g.s.logic.gatewayManager.get(gatewayID)
	if gateway == nil {
		c.WriteErr(ErrNodeConnNotFound)
		return
	}
	gateway.ClientClose(req)
	c.WriteOk()
}

func (g *gatewayHandler) handleGatewayReset(c *wkserver.Context) {
	req := &pb.ResetReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	gatewayID := req.NodeID
	gateway := g.s.logic.gatewayManager.get(gatewayID)
	if gateway != nil {
		gateway.Reset()
	}

	c.WriteOk()
}

func (g *gatewayHandler) handleGatewayConnWrite(c *wkserver.Context) {
	gatewayID := c.Conn().UID()
	gateway := g.s.logic.gatewayManager.get(gatewayID)
	if gateway == nil {
		c.WriteErrorAndStatus(ErrGatewayNotFound, proto.Status_NotFound)
		return
	}
	block, size, err := g.proto.Decode(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	if size == 0 {
		c.WriteOk()
		return
	}
	conn := gateway.GetConn(block.Conn.Id)
	if conn == nil {
		c.WriteErrorAndStatus(ErrNodeConnNotFound, proto.Status_NotFound)
		return
	}

	cliConn := conn.(*clientConn)
	cliConn.tmpBufferLock.Lock()
	cliConn.tmpBuffer = append(cliConn.tmpBuffer, block.Data...)
	cliConn.tmpBufferLock.Unlock()

	size, err = g.s.logic.OnGatewayClientWrite(block.Conn, cliConn.tmpBuffer)
	if err != nil {
		c.WriteErr(err)
		return
	}
	cliConn.tmpBufferLock.Lock()
	cliConn.tmpBuffer = cliConn.tmpBuffer[size:]
	cliConn.tmpBufferLock.Unlock()

	// _, err := gateway.putToInbound(c.Body())
	// if err != nil {
	// 	c.WriteErr(err)
	// 	return
	// }
	c.WriteOk()
}

func (g *gatewayHandler) createClientConn(req *pb.AuthReq) *clientConn {
	clientConn := newClientConn(req.Conn())
	return clientConn
}

func (g *gatewayHandler) handleGetClusterConfigSet(c *wkserver.Context) {

}
