package clusterset

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/pb"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

func (s *Server) initServerRoute() {
	s.s.Route("/join", s.join)                           // 加入集群
	s.s.Route("/syncClusterset", s.handleSyncClusterset) // 同步集群配置

	s.s.Route(s.s.Options().ConnPath, s.onNodeConnect)
	s.s.Route(s.s.Options().ClosePath, s.onNodeClose)
}

func (s *Server) join(c *wkserver.Context) {
	req := &pb.JoinReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		c.WriteErr(err)
		return
	}

	err = s.clusterset.addNewNode(&pb.NodeConfig{
		NodeID:     req.NodeID,
		ServerAddr: req.ServerAddr,
		Status:     pb.NodeStatus_New,
	})
	if err != nil {
		c.WriteErr(err)
		return
	}
	data, _ := s.clusterset.clusterConfigSet.Marshal()
	c.Write(data)
}

func (s *Server) handleSyncClusterset(c *wkserver.Context) {
	data, err := s.clusterset.clusterConfigSet.Marshal()
	if err != nil {
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (s *Server) onNodeConnect(c *wkserver.Context) {
	req := c.ConnReq()
	conn := c.Conn()
	conn.SetUID(req.Uid)
	conn.SetAuthed(true)

	if !strings.Contains(req.Uid, "@") {
		s.Error("uid is error", zap.String("uid", req.Uid))
		c.WriteConnack(&proto.Connack{
			Id:     req.Id,
			Status: proto.Status_ERROR,
		})
		return
	}
	uidStrs := strings.Split(req.Uid, "@")
	role := uidStrs[1]
	if role == NodeRoleGateway.String() {

		s.Debug("gateway connected", zap.String("gatewayID", c.Conn().UID()))
	} else if role == NodeRolePeer.String() {
		nodeIDStr := uidStrs[0]
		nodeID, _ := strconv.ParseUint(nodeIDStr, 10, 64)
		if nodeID == 0 {
			c.WriteErr(fmt.Errorf("node id is error -> %s", nodeIDStr))
			return
		}
		s.Debug("peer connected", zap.Uint64("nodeID", nodeID))
		s.nodeManager.addNode(newNode(nodeID, conn))

	}
	c.WriteConnack(&proto.Connack{
		Id:     req.Id,
		Status: proto.Status_OK,
	})
}

func (s *Server) onNodeClose(c *wkserver.Context) {
	uid := c.Conn().UID()
	if !strings.Contains(uid, "@") {
		s.Error("uid is error", zap.String("uid", uid))
		return
	}
	uidStrs := strings.Split(uid, "@")
	role := uidStrs[1]

	if role == NodeRoleGateway.String() {

	} else if role == NodeRolePeer.String() {
		nodeIDStr := uidStrs[0]
		nodeID, _ := strconv.ParseUint(nodeIDStr, 10, 64)
		if nodeID == 0 {
			return
		}
		s.nodeManager.removeNode(nodeID)
	}
}
