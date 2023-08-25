package logicclient

import "github.com/WuKongIM/WuKongIM/internal/logicclient/pb"

type Client interface {
	Auth(req *pb.AuthReq) (*pb.AuthResp, error)
}
