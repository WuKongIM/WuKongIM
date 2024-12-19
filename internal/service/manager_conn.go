package service

import "github.com/WuKongIM/WuKongIM/pkg/wknet"

var ConnManager IConnManager

type IConnManager interface {
	// AddConn 添加连接
	AddConn(conn wknet.Conn)
	// RemoveConn 移除连接
	RemoveConn(conn wknet.Conn)
	// GetConn 获取连接
	GetConn(connID int64) wknet.Conn
	// ConnCount 获取连接数量
	ConnCount() int

	GetAllConn() []wknet.Conn
}
