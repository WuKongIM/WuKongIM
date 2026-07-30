package issueagent

import "strings"

// IssueCommand is the complete v2 maintainer command vocabulary.
type IssueCommand string

const (
	IssueCommandFix      IssueCommand = "/agent fix"
	IssueCommandRetry    IssueCommand = "/agent retry"
	IssueCommandCancel   IssueCommand = "/agent cancel"
	IssueCommandTakeOver IssueCommand = "/agent take-over"
)

// ParseIssueCommand accepts an exact command only on the first comment line.
func ParseIssueCommand(body string) (IssueCommand, bool) {
	firstLine, _, _ := strings.Cut(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	command := IssueCommand(firstLine)
	switch command {
	case IssueCommandFix,
		IssueCommandRetry,
		IssueCommandCancel,
		IssueCommandTakeOver:
		return command, true
	default:
		return "", false
	}
}
