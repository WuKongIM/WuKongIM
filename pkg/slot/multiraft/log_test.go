package multiraft

import raft "go.etcd.io/raft/v3"

func init() {
	raft.SetLogger(discardRaftLogger{})
}

type discardRaftLogger struct{}

func (discardRaftLogger) Debug(v ...interface{})                   {}
func (discardRaftLogger) Debugf(format string, v ...interface{})   {}
func (discardRaftLogger) Error(v ...interface{})                   {}
func (discardRaftLogger) Errorf(format string, v ...interface{})   {}
func (discardRaftLogger) Info(v ...interface{})                    {}
func (discardRaftLogger) Infof(format string, v ...interface{})    {}
func (discardRaftLogger) Warning(v ...interface{})                 {}
func (discardRaftLogger) Warningf(format string, v ...interface{}) {}

func (discardRaftLogger) Fatal(v ...interface{}) {
	panic(v)
}

func (discardRaftLogger) Fatalf(format string, v ...interface{}) {
	panic(format)
}

func (discardRaftLogger) Panic(v ...interface{}) {
	panic(v)
}

func (discardRaftLogger) Panicf(format string, v ...interface{}) {
	panic(format)
}
