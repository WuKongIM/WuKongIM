package log

import "strings"

const (
	defaultLevel      = "info"
	defaultDir        = "./logs"
	defaultMaxSize    = 100
	defaultMaxAge     = 30
	defaultMaxBackups = 10
	defaultFormat     = "console"
)

// Config controls the zap-backed logger built by the app composition root.
type Config struct {
	Level      string
	Dir        string
	MaxSize    int
	MaxAge     int
	MaxBackups int
	Compress   bool
	Console    bool
	Format     string
}

func (c Config) withDefaults() Config {
	if strings.TrimSpace(c.Level) == "" {
		c.Level = defaultLevel
	}
	if strings.TrimSpace(c.Dir) == "" {
		c.Dir = defaultDir
	}
	if c.MaxSize <= 0 {
		c.MaxSize = defaultMaxSize
	}
	if c.MaxAge <= 0 {
		c.MaxAge = defaultMaxAge
	}
	if c.MaxBackups <= 0 {
		c.MaxBackups = defaultMaxBackups
	}
	if strings.TrimSpace(c.Format) == "" {
		c.Format = defaultFormat
	}
	if !c.Console {
		// Preserve explicit false; default true only when zero-value config is used.
		c.Console = c == (Config{})
	}
	if !c.Compress {
		c.Compress = c == (Config{})
	}
	return c
}
