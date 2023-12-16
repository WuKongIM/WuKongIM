package raftgroup

import (
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type raftLogger struct {
	lg wklog.Log
}

func newRaftLogger(lg wklog.Log) *raftLogger {

	return &raftLogger{
		lg: lg,
	}
}

func (r *raftLogger) Debug(v ...interface{}) {
	r.lg.Debug(fmt.Sprint(v...))
}

func (r *raftLogger) Debugf(format string, v ...interface{}) {
	r.lg.Debug(fmt.Sprintf(format, v...))
}

func (r *raftLogger) Info(v ...interface{}) {
	r.lg.Info(fmt.Sprint(v...))
}

func (r *raftLogger) Infof(format string, v ...interface{}) {
	if strings.Contains(format, "became leader") {
		r.lg.Info(fmt.Sprintf(format, v...))
	}
}

func (r *raftLogger) Warning(v ...interface{}) {
	r.lg.Warn(fmt.Sprint(v...))
}

func (r *raftLogger) Warningf(format string, v ...interface{}) {
	r.lg.Warn(fmt.Sprintf(format, v...))
}

func (r *raftLogger) Error(v ...interface{}) {
	r.lg.Error(fmt.Sprint(v...))
}

func (r *raftLogger) Errorf(format string, v ...interface{}) {
	r.lg.Error(fmt.Sprintf(format, v...))
}

func (r *raftLogger) Fatal(v ...interface{}) {
	r.lg.Fatal(fmt.Sprint(v...))
}
func (r *raftLogger) Fatalf(format string, v ...interface{}) {
	r.lg.Fatal(fmt.Sprintf(format, v...))
}
func (r *raftLogger) Panic(v ...interface{}) {
	r.lg.Panic(fmt.Sprint(v...))
}
func (r *raftLogger) Panicf(format string, v ...interface{}) {
	r.lg.Panic(fmt.Sprintf(format, v...))
}
