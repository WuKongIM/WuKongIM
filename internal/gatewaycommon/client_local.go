package gatewaycommon

type LocalGatewayServer interface {
	GetConnManager() ConnManager
}

var localGatewayServer LocalGatewayServer

func SetLocalGatewayServer(s LocalGatewayServer) {
	localGatewayServer = s
}

func GetLocalGatewayServer() LocalGatewayServer {
	return localGatewayServer
}

// type LocalGatewayClient struct {
// }

// func NewLocalGatewayClient() *LocalGatewayClient {
// 	return &LocalGatewayClient{}
// }

// func (l *LocalGatewayClient) GetConnManager() ConnManager {
// 	return localGatewayServer.GetConnManager()
// }

// func (l *LocalGatewayClient) WriteToGateway(conn *pb.Conn, data []byte) error {
// 	return localGatewayServer.WriteToGateway(conn, data)
// }
