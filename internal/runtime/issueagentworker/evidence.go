package issueagentworker

import (
	"regexp"
	"strings"
)

var artifactDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var sensitiveTextPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)authorization:\s*(?:bearer|token)\s+[^\s]+`),
	regexp.MustCompile(`(?i)(?:github_token|[a-z0-9_]*api_key|[a-z0-9_]*token)=[^\s]+`),
	regexp.MustCompile(`(?:gh[opsu]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

// ToolEvidence is the bounded redacted audit record for one broker operation.
type ToolEvidence struct {
	ID              uint64   `json:"id"`
	Tool            string   `json:"tool"`
	Path            string   `json:"path,omitempty"`
	Executable      string   `json:"executable,omitempty"`
	Arguments       []string `json:"arguments,omitempty"`
	ExitCode        int      `json:"exit_code,omitempty"`
	OutputSHA256    string   `json:"output_sha256,omitempty"`
	ErrorSHA256     string   `json:"error_sha256,omitempty"`
	DurationMS      int64    `json:"duration_ms,omitempty"`
	AssertionSHA256 string   `json:"assertion_sha256,omitempty"`
}

// Evidence returns a stable copy of all completed tool operations.
func (broker *Broker) Evidence() []ToolEvidence {
	if broker == nil {
		return nil
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	result := make([]ToolEvidence, len(broker.evidence))
	copy(result, broker.evidence)
	for index := range result {
		result[index].Arguments = append([]string(nil), result[index].Arguments...)
	}
	return result
}

// SanitizeText redacts credential shapes before applying a hard byte limit.
func SanitizeText(input string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		return "", input != ""
	}
	output := input
	for _, pattern := range sensitiveTextPatterns {
		output = pattern.ReplaceAllString(output, "[REDACTED]")
	}
	output = strings.ReplaceAll(output, "\x00", "")
	if len(output) <= maxBytes {
		return output, false
	}
	return output[:maxBytes], true
}
