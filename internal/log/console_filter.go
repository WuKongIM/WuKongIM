package log

import (
	"go.uber.org/zap/zapcore"
)

type consoleEventFilterCore struct {
	// core is the underlying stdout core used for non-excluded entries.
	core zapcore.Core
	// excluded contains structured event names that must remain file-only.
	excluded map[string]struct{}
	// contextExcluded remembers an excluded event attached through Logger.With.
	contextExcluded bool
}

func newConsoleEventFilterCore(core zapcore.Core, events []string) zapcore.Core {
	if len(events) == 0 {
		return core
	}
	excluded := make(map[string]struct{}, len(events))
	for _, event := range events {
		if event != "" {
			excluded[event] = struct{}{}
		}
	}
	if len(excluded) == 0 {
		return core
	}
	return &consoleEventFilterCore{core: core, excluded: excluded}
}

func (c *consoleEventFilterCore) Enabled(level zapcore.Level) bool {
	return c.core.Enabled(level)
}

func (c *consoleEventFilterCore) With(fields []zapcore.Field) zapcore.Core {
	return &consoleEventFilterCore{
		core:            c.core.With(fields),
		excluded:        c.excluded,
		contextExcluded: c.contextExcluded || c.hasExcludedEvent(fields),
	}
}

func (c *consoleEventFilterCore) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if !c.Enabled(entry.Level) {
		return checked
	}
	return checked.AddCore(entry, c)
}

func (c *consoleEventFilterCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	if c.contextExcluded || c.hasExcludedEvent(fields) {
		return nil
	}
	return c.core.Write(entry, fields)
}

func (c *consoleEventFilterCore) Sync() error {
	return c.core.Sync()
}

func (c *consoleEventFilterCore) hasExcludedEvent(fields []zapcore.Field) bool {
	for _, field := range fields {
		if field.Key != "event" || field.Type != zapcore.StringType {
			continue
		}
		if _, ok := c.excluded[field.String]; ok {
			return true
		}
	}
	return false
}
