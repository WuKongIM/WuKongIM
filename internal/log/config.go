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

// Config controls the zap-backed logger built by the internal composition root.
type Config struct {
	// Level is the minimum log level accepted by the logger: debug, info, warn, or error.
	Level string
	// Dir is the directory where rolling log files are created.
	Dir string
	// MaxSize is the maximum size in megabytes before one log file is rotated.
	MaxSize int
	// MaxAge is the maximum number of days to retain rotated log files.
	MaxAge int
	// MaxBackups is the maximum number of rotated files retained for each log.
	MaxBackups int
	// Compress enables gzip compression for rotated log files.
	Compress bool
	// Console enables an additional stdout sink for interactive runs.
	Console bool
	// Format selects the file encoder format; json writes structured JSON and other values use console encoding.
	Format string
	// ConsoleExcludedEvents keeps selected structured events in files without duplicating them on stdout.
	ConsoleExcludedEvents []string
}

func (c Config) withDefaults() Config {
	zeroConfig := c.Level == "" && c.Dir == "" && c.MaxSize == 0 && c.MaxAge == 0 && c.MaxBackups == 0 &&
		!c.Compress && !c.Console && c.Format == "" && len(c.ConsoleExcludedEvents) == 0
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
		c.Console = zeroConfig
	}
	if !c.Compress {
		c.Compress = zeroConfig
	}
	return c
}
