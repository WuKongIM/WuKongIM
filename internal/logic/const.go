package logic

import "github.com/WuKongIM/WuKongIM/internal/gatewaycommon"

type NodeRole string

const (
	NodeRolePeer    NodeRole = "peer"
	NodeRoleGateway NodeRole = "gateway"
)

func (n NodeRole) String() string {
	return string(n)
}

const (
	ProtoVersionKey = "protoVersion"
	GatewayIDKey    = "gatewayID"
)

func GetProtoVersion(conn gatewaycommon.Conn) int {
	v := conn.Value(ProtoVersionKey)
	if v == nil {
		return 0
	}
	return v.(int)
}

func SetProtoVersion(conn gatewaycommon.Conn, protoVersion int) {
	conn.SetValue(ProtoVersionKey, protoVersion)
}

func SetGatewayID(conn gatewaycommon.Conn, gatewayID string) {
	conn.SetValue(GatewayIDKey, gatewayID)
}
func GetGatewayID(conn gatewaycommon.Conn) string {
	v := conn.Value(GatewayIDKey)
	if v == nil {
		return ""
	}
	return v.(string)
}
