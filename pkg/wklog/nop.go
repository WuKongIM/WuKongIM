package wklog

type NopLogger struct{}

func NewNop() Logger { return &NopLogger{} }

func (n *NopLogger) Debug(string, ...Field) {}
func (n *NopLogger) Info(string, ...Field)  {}
func (n *NopLogger) Warn(string, ...Field)  {}
func (n *NopLogger) Error(string, ...Field) {}
func (n *NopLogger) Fatal(string, ...Field) {}
func (n *NopLogger) Named(string) Logger    { return n }
func (n *NopLogger) With(...Field) Logger   { return n }
func (n *NopLogger) Sync() error            { return nil }
