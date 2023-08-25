package logic

import "github.com/WuKongIM/WuKongIM/internal/logicclient/pb"

type LogicServer struct {
}

func NewLogicServer() *LogicServer {

	return &LogicServer{}
}

func (l *LogicServer) Auth(req *pb.AuthReq) (*pb.AuthResp, error) {

	return &pb.AuthResp{
		DeviceLevel: 1,
	}, nil
}
