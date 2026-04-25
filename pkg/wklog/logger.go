package wklog

// Logger defines the logging contract used by packages outside the app
// composition root.
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)
	Named(name string) Logger
	With(fields ...Field) Logger
	Sync() error
}
