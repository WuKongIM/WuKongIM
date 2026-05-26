package transport

import "github.com/WuKongIM/WuKongIM/pkg/wklog"

const (
	eventConnConnected     = "gateway.transport.conn.connected"
	eventConnConnectFailed = "gateway.transport.conn.connect_failed"
)

func LogConnectSuccess(opts ListenerOptions, conn Conn) {
	if conn == nil {
		return
	}
	loggerForListener(opts).Debug("accept client connection", connectionFields(opts, conn.ID(), conn.LocalAddr(), conn.RemoteAddr(), nil)...)
}

func LogConnectFailure(opts ListenerOptions, connID uint64, localAddr, remoteAddr string, err error) {
	if err == nil {
		return
	}
	loggerForListener(opts).Debug("reject client connection", connectionFields(opts, connID, localAddr, remoteAddr, err)...)
}

func loggerForListener(opts ListenerOptions) wklog.Logger {
	if opts.Logger == nil {
		return wklog.NewNop()
	}
	return opts.Logger
}

func connectionFields(opts ListenerOptions, connID uint64, localAddr, remoteAddr string, err error) []wklog.Field {
	event := eventConnConnected
	if err != nil {
		event = eventConnConnectFailed
	}

	fields := []wklog.Field{
		wklog.Event(event),
		wklog.String("listener", opts.Name),
		wklog.String("network", opts.Network),
	}
	if connID != 0 {
		fields = append(fields, wklog.ConnID(connID))
	}
	if localAddr != "" {
		fields = append(fields, wklog.String("localAddr", localAddr))
	}
	if remoteAddr != "" {
		fields = append(fields, wklog.String("remoteAddr", remoteAddr))
	}
	if err != nil {
		fields = append(fields, wklog.Error(err))
	}
	return fields
}
