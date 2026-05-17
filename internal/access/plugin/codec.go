package plugin

import (
	"fmt"

	"google.golang.org/protobuf/proto"
)

func (s *Server) checkBodyLimit(c rpcContext) bool {
	body := c.Body()
	if int64(len(body)) > s.maxBodyBytes {
		c.WriteErr(fmt.Errorf("plugin host rpc body exceeds max bytes: %d > %d", len(body), s.maxBodyBytes))
		return false
	}
	return true
}

func (s *Server) decodeProto(c rpcContext, msg proto.Message) bool {
	if !s.checkBodyLimit(c) {
		return false
	}
	body := c.Body()
	if len(body) == 0 {
		return true
	}
	if err := proto.Unmarshal(body, msg); err != nil {
		c.WriteErr(err)
		return false
	}
	return true
}

func (s *Server) writeProto(c rpcContext, msg proto.Message) {
	if size := proto.Size(msg); int64(size) > s.maxBodyBytes {
		c.WriteErr(fmt.Errorf("plugin host rpc response exceeds max bytes: %d > %d", size, s.maxBodyBytes))
		return
	}
	data, err := proto.Marshal(msg)
	if err != nil {
		c.WriteErr(err)
		return
	}
	if int64(len(data)) > s.maxBodyBytes {
		c.WriteErr(fmt.Errorf("plugin host rpc response exceeds max bytes: %d > %d", len(data), s.maxBodyBytes))
		return
	}
	c.Write(data)
}
