package multiraft

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.uber.org/zap"
)

type Logger struct {
	lg     wklog.Log
	prefix string
}

func NewLogger(prefix string) *Logger {
	return &Logger{
		lg:     wklog.NewWKLog(prefix),
		prefix: prefix,
	}
}

func (l *Logger) Debug(v ...interface{}) {
	l.lg.Debug(l.prefix, zap.Any("v", v))
}
func (l *Logger) Debugf(format string, v ...interface{}) {
	l.lg.Debug(fmt.Sprintf(fmt.Sprintf("%s%s", l.prefix, format), v...))
}

func (l *Logger) Error(v ...interface{}) {
	l.lg.Error(l.prefix, zap.Any("v", v))
}
func (l *Logger) Errorf(format string, v ...interface{}) {
	l.lg.Error(fmt.Sprintf(fmt.Sprintf("%s%s", l.prefix, format), v...))
}

func (l *Logger) Info(v ...interface{}) {
	l.lg.Info(l.prefix, zap.Any("v", v))
}
func (l *Logger) Infof(format string, v ...interface{}) {
	l.lg.Info(fmt.Sprintf(fmt.Sprintf("%s%s", l.prefix, format), v...))
}

func (l *Logger) Warning(v ...interface{}) {
	l.lg.Warn(l.prefix, zap.Any("v", v))

}
func (l *Logger) Warningf(format string, v ...interface{}) {
	l.lg.Warn(fmt.Sprintf(fmt.Sprintf("%s%s", l.prefix, format), v...))
}

func (l *Logger) Fatal(v ...interface{}) {
	l.lg.Fatal(l.prefix, zap.Any("v", v))

}
func (l *Logger) Fatalf(format string, v ...interface{}) {
	l.lg.Fatal(fmt.Sprintf(fmt.Sprintf("%s%s", l.prefix, format), v...))
}

func (l *Logger) Panic(v ...interface{}) {
	l.lg.Panic(l.prefix, zap.Any("v", v))

}
func (l *Logger) Panicf(format string, v ...interface{}) {
	l.lg.Panic(fmt.Sprintf(fmt.Sprintf("%s%s", l.prefix, format), v...))
}
