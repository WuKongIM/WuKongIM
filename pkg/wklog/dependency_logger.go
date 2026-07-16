package wklog

import (
	"fmt"
	"strings"
)

// DependencyLogger adapts printf-style dependency logs to the structured
// application logger. Routine dependency info is debug-only so application
// lifecycle records remain the authoritative startup surface.
type DependencyLogger struct {
	logger Logger
}

// NewDependencyLogger builds a printf-style bridge for one dependency.
func NewDependencyLogger(logger Logger, sourceModule string) *DependencyLogger {
	if logger == nil {
		logger = NewNop()
	}
	return &DependencyLogger{logger: logger.With(
		Event("dependency.log"),
		SourceModule(strings.TrimSpace(sourceModule)),
	)}
}

// Debug logs dependency diagnostics at debug level.
func (l *DependencyLogger) Debug(msg string, fields ...Field) {
	l.logger.Debug(strings.TrimSpace(msg), fields...)
}

// Info demotes routine dependency lifecycle details to debug level.
func (l *DependencyLogger) Info(msg string, fields ...Field) {
	l.logger.Debug(strings.TrimSpace(msg), fields...)
}

// Warn preserves dependency warnings at warn level.
func (l *DependencyLogger) Warn(msg string, fields ...Field) {
	l.logger.Warn(strings.TrimSpace(msg), fields...)
}

// Error preserves dependency failures at error level.
func (l *DependencyLogger) Error(msg string, fields ...Field) {
	l.logger.Error(strings.TrimSpace(msg), fields...)
}

// Fatal preserves the dependency's fatal termination semantics.
func (l *DependencyLogger) Fatal(msg string, fields ...Field) {
	l.logger.Fatal(strings.TrimSpace(msg), fields...)
}

// Debugf logs formatted dependency diagnostics at debug level.
func (l *DependencyLogger) Debugf(format string, args ...any) {
	l.Debug(formatDependencyMessage(format, args...))
}

// Infof demotes routine dependency lifecycle details to debug level.
func (l *DependencyLogger) Infof(format string, args ...any) {
	l.Info(formatDependencyMessage(format, args...))
}

// Warnf preserves dependency warnings at warn level.
func (l *DependencyLogger) Warnf(format string, args ...any) {
	l.Warn(formatDependencyMessage(format, args...))
}

// Errorf preserves dependency failures at error level.
func (l *DependencyLogger) Errorf(format string, args ...any) {
	l.Error(formatDependencyMessage(format, args...))
}

// Fatalf preserves the dependency's fatal termination semantics.
func (l *DependencyLogger) Fatalf(format string, args ...any) {
	l.Fatal(formatDependencyMessage(format, args...))
}

func formatDependencyMessage(format string, args ...any) string {
	return strings.TrimSpace(fmt.Sprintf(format, args...))
}
