//go:build e2e

package suite

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
	"github.com/pelletier/go-toml/v2"
)

const (
	defaultStopTimeout   = 5 * time.Second
	diagnosticsTailBytes = 4096
	diagnosticsTailLines = 16
	e2eHarnessEnvPrefix  = "WK_E2E_"
	redactedConfigValue  = "[REDACTED]"
	omittedConfigValue   = "[invalid or unsupported TOML; content omitted]"
)

var sensitiveConfigKeyMarkers = [...]string{
	"password",
	"passwd",
	"secret",
	"token",
	"credential",
	"privatekey",
	"apikey",
	"accesskey",
	"signingkey",
	"encryptionkey",
}

var diagnosticConfigSchema = newDiagnosticConfigSchema(productconfig.SchemaFields())

type diagnosticSchema struct {
	// leaves maps exact public TOML paths to their validation and redaction metadata.
	leaves map[string]productconfig.SchemaField
	// prefixes contains valid schema group paths used to reject unknown config trees.
	prefixes map[string]struct{}
}

// NodeProcess wraps one real child process used by the e2e suite.
type NodeProcess struct {
	Spec        NodeSpec
	BinaryPath  string
	StopTimeout time.Duration
	Cmd         *exec.Cmd
	StdoutLog   *os.File
	StderrLog   *os.File
	command     *exec.Cmd
}

// Start launches the child process and redirects stdout and stderr to files.
func (p *NodeProcess) Start() error {
	rootDir := p.Spec.RootDir
	if rootDir == "" {
		rootDir = filepath.Dir(p.Spec.StdoutPath)
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return err
	}

	stdoutLog, err := os.Create(p.Spec.StdoutPath)
	if err != nil {
		return err
	}
	stderrLog, err := os.Create(p.Spec.StderrPath)
	if err != nil {
		_ = stdoutLog.Close()
		return err
	}

	cmd := p.command
	if cmd == nil {
		cmd = exec.Command(p.BinaryPath, "-config", p.Spec.ConfigPath)
	}
	baseEnv := cmd.Env
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	childEnv := make([]string, 0, len(baseEnv)+len(p.Spec.Env))
	childEnv = appendChildEnvironment(childEnv, baseEnv)
	cmd.Env = appendChildEnvironment(childEnv, p.Spec.Env)
	cmd.Stdout = stdoutLog
	cmd.Stderr = stderrLog

	p.StdoutLog = stdoutLog
	p.StderrLog = stderrLog
	p.Cmd = cmd

	if err := cmd.Start(); err != nil {
		_ = stdoutLog.Close()
		_ = stderrLog.Close()
		return err
	}
	return nil
}

func appendChildEnvironment(dst, src []string) []string {
	for _, entry := range src {
		if strings.HasPrefix(entry, e2eHarnessEnvPrefix) {
			continue
		}
		dst = append(dst, entry)
	}
	return dst
}

// Stop terminates the child process and waits for it to exit.
func (p *NodeProcess) Stop() error {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- p.Cmd.Wait()
	}()

	if err := p.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}

	timeout := p.StopTimeout
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}

	select {
	case err := <-waitCh:
		p.closeLogs()
		return normalizeStopError(err)
	case <-time.After(timeout):
		if err := p.Cmd.Process.Kill(); err != nil {
			p.closeLogs()
			return err
		}
		err := <-waitCh
		p.closeLogs()
		return normalizeStopError(err)
	}
}

// DumpDiagnostics returns a small human-readable snapshot of process artifacts.
func (p *NodeProcess) DumpDiagnostics() string {
	var b strings.Builder
	fmt.Fprintf(&b, "process: %s\n", p.processStatus())
	fmt.Fprintf(&b, "config: %s\n", p.Spec.ConfigPath)
	appendConfigTail(&b, "config", p.Spec.ConfigPath)
	fmt.Fprintf(&b, "stdout: %s\n", p.Spec.StdoutPath)
	fmt.Fprintf(&b, "stderr: %s\n", p.Spec.StderrPath)
	appendLogTail(&b, "stdout", p.Spec.StdoutPath)
	appendLogTail(&b, "stderr", p.Spec.StderrPath)

	logDir := p.Spec.LogDir
	if logDir == "" && p.Spec.RootDir != "" {
		logDir = filepath.Join(p.Spec.RootDir, "logs")
	}
	if logDir != "" {
		fmt.Fprintf(&b, "app-log: %s\n", filepath.Join(logDir, "app.log"))
		appendLogTail(&b, "app-log", filepath.Join(logDir, "app.log"))
		fmt.Fprintf(&b, "error-log: %s\n", filepath.Join(logDir, "error.log"))
		appendLogTail(&b, "error-log", filepath.Join(logDir, "error.log"))
	}
	return b.String()
}

func (p *NodeProcess) processStatus() string {
	if p == nil || p.Cmd == nil || p.Cmd.Process == nil {
		return "not_started"
	}
	if p.Cmd.ProcessState != nil {
		return fmt.Sprintf("pid=%d exited=%v", p.Cmd.Process.Pid, p.Cmd.ProcessState.Exited())
	}
	if err := p.Cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return fmt.Sprintf("pid=%d signal_error=%v", p.Cmd.Process.Pid, err)
	}
	return fmt.Sprintf("pid=%d running", p.Cmd.Process.Pid)
}

func (p *NodeProcess) closeLogs() {
	if p.StdoutLog != nil {
		_ = p.StdoutLog.Close()
	}
	if p.StderrLog != nil {
		_ = p.StderrLog.Close()
	}
}

func appendLogTail(b *strings.Builder, name, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(b, "%s-read-error: %v\n", name, err)
		return
	}
	appendBoundedContent(b, name, data)
}

func appendConfigTail(b *strings.Builder, name, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(b, "%s-read-error: %v\n", name, err)
		return
	}
	redacted, err := redactTOMLConfig(data)
	if err != nil {
		fmt.Fprintf(b, "%s-content: %s\n", name, omittedConfigValue)
		return
	}
	appendBoundedContent(b, name, redacted)
}

func redactTOMLConfig(data []byte) ([]byte, error) {
	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	if err := redactSchemaConfigMap(config, "", diagnosticConfigSchema); err != nil {
		return nil, err
	}
	return toml.Marshal(config)
}

func newDiagnosticConfigSchema(fields []productconfig.SchemaField) diagnosticSchema {
	schema := diagnosticSchema{
		leaves:   make(map[string]productconfig.SchemaField, len(fields)),
		prefixes: make(map[string]struct{}),
	}
	for _, field := range fields {
		schema.leaves[field.TOMLPath] = field
		parts := strings.Split(field.TOMLPath, ".")
		for i := 1; i < len(parts); i++ {
			schema.prefixes[strings.Join(parts[:i], ".")] = struct{}{}
		}
	}
	return schema
}

func redactSchemaConfigMap(config map[string]any, parentPath string, schema diagnosticSchema) error {
	for key, value := range config {
		path := key
		if parentPath != "" {
			path = parentPath + "." + key
		}
		if field, ok := schema.leaves[path]; ok {
			sanitized, err := sanitizeSchemaLeafValue(field, value)
			if err != nil {
				return fmt.Errorf("config path %q has unsupported %s value", path, field.Kind)
			}
			config[key] = sanitized
			continue
		}
		if _, ok := schema.prefixes[path]; !ok {
			return fmt.Errorf("unknown config path %q", path)
		}
		group, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("config path %q must be a group", path)
		}
		if err := redactSchemaConfigMap(group, path, schema); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeSchemaLeafValue(field productconfig.SchemaField, value any) (any, error) {
	if field.Kind == "string_list" || field.Kind == "object_list" {
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			if field.DiagnosticSensitive {
				return redactedConfigValue, nil
			}
			return value, nil
		}
		items, encodedAsJSON, err := schemaListValue(field.Kind, value)
		if err != nil {
			return nil, err
		}
		if field.DiagnosticSensitive {
			return redactedConfigValue, nil
		}
		sanitized := redactNestedConfigValue(items)
		if !encodedAsJSON {
			return sanitized, nil
		}
		data, err := json.Marshal(sanitized)
		if err != nil {
			return nil, err
		}
		return string(data), nil
	}

	if !schemaScalarKindMatches(field.Kind, value) {
		return nil, fmt.Errorf("unsupported schema kind")
	}
	if field.DiagnosticSensitive {
		return redactedConfigValue, nil
	}
	return value, nil
}

func schemaListValue(kind string, value any) ([]any, bool, error) {
	if text, ok := value.(string); ok {
		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, false, err
		}
		items, ok := decoded.([]any)
		if !ok || !schemaListItemsMatch(kind, items) {
			return nil, false, fmt.Errorf("unsupported list value")
		}
		return items, true, nil
	}
	items, ok := value.([]any)
	if !ok || !schemaListItemsMatch(kind, items) {
		return nil, false, fmt.Errorf("unsupported list value")
	}
	return items, false, nil
}

func schemaListItemsMatch(kind string, items []any) bool {
	for _, item := range items {
		switch kind {
		case "string_list":
			if _, ok := item.(string); !ok {
				return false
			}
		case "object_list":
			if _, ok := item.(map[string]any); !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func schemaScalarKindMatches(kind string, value any) bool {
	switch kind {
	case "string", "duration":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "int", "uint64", "uint32", "uint16":
		switch value.(type) {
		case int, int64:
			return true
		default:
			return false
		}
	case "float":
		switch value.(type) {
		case int, int64, float64:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func redactNestedConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if isSensitiveConfigKey(key) {
				typed[key] = redactedConfigValue
				continue
			}
			typed[key] = redactNestedConfigValue(child)
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = redactNestedConfigValue(typed[i])
		}
		return typed
	default:
		return value
	}
}

func isSensitiveConfigKey(key string) bool {
	var normalized strings.Builder
	normalized.Grow(len(key))
	for _, r := range strings.ToLower(key) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			normalized.WriteRune(r)
		}
	}
	compact := normalized.String()
	for _, marker := range sensitiveConfigKeyMarkers {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

func appendBoundedContent(b *strings.Builder, name string, data []byte) {
	truncated := false
	if len(data) > diagnosticsTailBytes {
		data = data[len(data)-diagnosticsTailBytes:]
		truncated = true
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > diagnosticsTailLines {
		lines = lines[len(lines)-diagnosticsTailLines:]
		truncated = true
		data = []byte(strings.Join(lines, "\n"))
	}
	if truncated {
		fmt.Fprintf(b, "%s-tail: [truncated]\n", name)
	}
	fmt.Fprintf(b, "%s-content:\n%s", name, data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		b.WriteByte('\n')
	}
}

func normalizeStopError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
