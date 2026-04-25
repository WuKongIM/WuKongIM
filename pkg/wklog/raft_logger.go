package wklog

import (
	"fmt"
	"strings"
)

type raftLogLevel uint8

const (
	raftLogLevelDebug raftLogLevel = iota + 1
	raftLogLevelInfo
	raftLogLevelWarn
	raftLogLevelError
)

type RaftLogger struct {
	logger Logger
}

func NewRaftLogger(logger Logger, fields ...Field) *RaftLogger {
	if logger == nil {
		logger = NewNop()
	}
	if len(fields) > 0 {
		logger = logger.With(fields...)
	}
	return &RaftLogger{logger: logger}
}

func (l *RaftLogger) Debug(v ...interface{}) {
	l.log(renderRaftArgs(v...), raftLogLevelDebug)
}

func (l *RaftLogger) Debugf(format string, v ...interface{}) {
	l.log(fmt.Sprintf(format, v...), raftLogLevelDebug)
}

func (l *RaftLogger) Error(v ...interface{}) {
	l.log(renderRaftArgs(v...), raftLogLevelError)
}

func (l *RaftLogger) Errorf(format string, v ...interface{}) {
	l.log(fmt.Sprintf(format, v...), raftLogLevelError)
}

func (l *RaftLogger) Info(v ...interface{}) {
	l.log(renderRaftArgs(v...), raftLogLevelInfo)
}

func (l *RaftLogger) Infof(format string, v ...interface{}) {
	l.log(fmt.Sprintf(format, v...), raftLogLevelInfo)
}

func (l *RaftLogger) Warning(v ...interface{}) {
	l.log(renderRaftArgs(v...), raftLogLevelWarn)
}

func (l *RaftLogger) Warningf(format string, v ...interface{}) {
	l.log(fmt.Sprintf(format, v...), raftLogLevelWarn)
}

func (l *RaftLogger) Fatal(v ...interface{}) {
	msg := renderRaftArgs(v...)
	l.logger.Fatal(msg, Event("raft.log"))
	panic(msg)
}

func (l *RaftLogger) Fatalf(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	l.logger.Fatal(msg, Event("raft.log"))
	panic(msg)
}

func (l *RaftLogger) Panic(v ...interface{}) {
	msg := renderRaftArgs(v...)
	l.logger.Error(msg, Event("raft.log"))
	panic(msg)
}

func (l *RaftLogger) Panicf(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	l.logger.Error(msg, Event("raft.log"))
	panic(msg)
}

func (l *RaftLogger) log(msg string, level raftLogLevel) {
	event, effectiveLevel := classifyRaftLog(msg, level)
	fields := []Field{Event("raft.log"), RaftEvent(event)}
	switch effectiveLevel {
	case raftLogLevelDebug:
		l.logger.Debug(msg, fields...)
	case raftLogLevelWarn:
		l.logger.Warn(msg, fields...)
	case raftLogLevelError:
		l.logger.Error(msg, fields...)
	default:
		l.logger.Info(msg, fields...)
	}
}

func renderRaftArgs(v ...interface{}) string {
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintln(v...))
}

func classifyRaftLog(msg string, level raftLogLevel) (string, raftLogLevel) {
	lower := strings.ToLower(strings.TrimSpace(msg))
	switch {
	case containsAny(lower, "became leader", "leader changed", "lost leader"):
		return "leader_change", level
	case containsAny(lower, "starting a new election", "became candidate", "campaign"):
		return "campaign", level
	case containsAny(lower, "msgheartbeat", "heartbeat"):
		if level == raftLogLevelInfo {
			return "heartbeat", raftLogLevelDebug
		}
		return "heartbeat", level
	case containsAny(lower, "switched to configuration", "confchange", "configuration voters"):
		return "config_change", level
	case containsAny(lower, "snapshot"):
		return "snapshot", level
	case containsAny(lower, "msgprop", "proposal"):
		return "proposal", level
	case containsAny(lower, "readindex", "read index", "readonly"):
		if level == raftLogLevelInfo {
			return "read_index", raftLogLevelDebug
		}
		return "read_index", level
	case containsAny(lower, "probe"):
		if level == raftLogLevelInfo {
			return "probe", raftLogLevelDebug
		}
		return "probe", level
	default:
		return "general", level
	}
}

func containsAny(msg string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}
