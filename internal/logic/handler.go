package logic

import (
	"github.com/WuKongIM/WuKongIM/internal/logicclient/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (s *Server) initHandler() {

	s.s.Route("/client/auth", s.handleClientAuth) // trigger client connection auth

	s.s.Route("/client/close", s.handleClientClose) // trigger client connection write

	s.s.Route(s.s.Options().ConnPath, s.handleNodeConn) // trigger when node connection

	s.s.Route(s.s.Options().ClosePath, s.handleNodeConnClose) // trigger when node connection close

	s.s.Route("/node/noderest", s.handleNodeReset)

	s.s.Route("/node/conn/write", s.handleNodeConnWrite) // trigger when node connection write
}

func (s *Server) handleClientAuth(c *wkserver.Context) {
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

	authResp, err := s.logicServer.Auth(req)
	if err != nil {
		c.WriteErr(ErrNodeConnNotFound)
		return
	}

	cliConn := s.createClientConn(req, nodeConn.UID())
	nodeID := nodeConn.UID()

	node := s.nodeManager.get(nodeID)
	if node == nil {
		c.WriteErr(ErrNodeConnNotFound)
		return
	}
	node.addClientConn(cliConn)

	data, _ := authResp.Marshal()
	c.Write(data)

}

func (s *Server) handleClientClose(c *wkserver.Context) {
	req := &pb.ClientCloseReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	conn := c.Conn()
	nodeID := conn.UID()

	node := s.nodeManager.get(nodeID)
	if node == nil {
		c.WriteErr(ErrNodeConnNotFound)
		return
	}
	node.removeClientConn(req.ConnID)

	c.WriteOk()
}

func (s *Server) handleNodeConn(c *wkserver.Context) {

	req := c.ConnReq()

	conn := c.Conn()
	conn.SetUID(req.Uid)
	conn.SetAuthed(true)

	nodeID := req.Uid

	node := s.nodeManager.get(nodeID)
	if node == nil {
		node = newNode(conn)
		s.nodeManager.add(node)
	}
	s.Debug("node connected", zap.String("nodeID", c.Conn().UID()))

	c.WriteConnack(&proto.Connack{
		Id:     req.Id,
		Status: proto.Status_OK,
	})
}

func (s *Server) handleNodeReset(c *wkserver.Context) {
	req := &pb.ResetReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	nodeID := req.NodeID
	node := s.nodeManager.get(nodeID)
	if node != nil {
		node.reset()
	}
	c.WriteOk()
}

func (s *Server) handleNodeConnWrite(c *wkserver.Context) {
	nodeID := c.Conn().UID()
	node := s.nodeManager.get(nodeID)
	if node == nil {
		c.WriteErrorAndStatus(ErrNodeConnNotFound, proto.Status_NotFound)
		return
	}
	_, err := node.write(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}
	c.WriteOk()
}

func (s *Server) handleNodeConnClose(c *wkserver.Context) {

	s.Debug("node connection close", zap.String("nodeID", c.Conn().UID()))
}

func (s *Server) createClientConn(req *pb.AuthReq, nodeID string) *clientConn {
	clientConn := newClientConn()
	clientConn.connID = req.ConnID
	clientConn.nodeID = nodeID
	clientConn.uid = req.Uid
	clientConn.deviceFlag = wkproto.DeviceFlag(req.DeviceFlag)
	return clientConn
}
