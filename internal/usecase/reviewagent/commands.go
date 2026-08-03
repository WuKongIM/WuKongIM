package reviewagent

import (
	"errors"
	"strings"
)

// Permission is the fresh repository permission of one command actor.
type Permission string

const (
	PermissionNone     Permission = "none"
	PermissionRead     Permission = "read"
	PermissionTriage   Permission = "triage"
	PermissionWrite    Permission = "write"
	PermissionMaintain Permission = "maintain"
	PermissionAdmin    Permission = "admin"
)

// CommandKind is the complete accepted Review Agent command surface.
type CommandKind string

const (
	CommandStatus     CommandKind = "status"
	CommandReview     CommandKind = "review"
	CommandExplain    CommandKind = "explain"
	CommandReconsider CommandKind = "reconsider"
	CommandRetry      CommandKind = "retry"
	CommandCancel     CommandKind = "cancel"
)

// CommandInput carries exact comment content and freshly resolved authority.
type CommandInput struct {
	Body       string
	Permission Permission
	Edited     bool
}

// Command is one authorized, bounded interaction.
type Command struct {
	Kind    CommandKind
	Payload string
}

// ParseCommand rejects prose, quotations, code blocks, edited commands, and
// stale or insufficient authority.
func ParseCommand(input CommandInput) (Command, error) {
	if input.Edited {
		return Command{}, errors.New("edited Review Agent command is not authoritative")
	}
	if len(input.Body) == 0 || len(input.Body) > 8192 ||
		strings.ContainsAny(input.Body, "\r\n") ||
		strings.Contains(input.Body, "```") ||
		strings.HasPrefix(strings.TrimSpace(input.Body), ">") ||
		strings.Count(input.Body, "@review-agent") != 1 ||
		!strings.HasPrefix(input.Body, "@review-agent ") ||
		input.Body != strings.TrimSpace(input.Body) {
		return Command{}, errors.New("ambiguous Review Agent command")
	}
	remainder := strings.TrimPrefix(input.Body, "@review-agent ")
	commandName, payload, _ := strings.Cut(remainder, " ")
	if len(payload) > 4096 || strings.ContainsRune(payload, '\x00') {
		return Command{}, errors.New("Review Agent command payload is invalid")
	}
	switch CommandKind(commandName) {
	case CommandStatus:
		if payload != "" {
			return Command{}, errors.New("status command does not accept a payload")
		}
		return Command{Kind: CommandStatus}, nil
	case CommandReview, CommandRetry, CommandCancel:
		if payload != "" {
			return Command{}, errors.New("administrative command does not accept a payload")
		}
		if input.Permission != PermissionAdmin {
			return Command{}, errors.New("administrative command is not authorized")
		}
		return Command{Kind: CommandKind(commandName)}, nil
	case CommandExplain:
		if strings.TrimSpace(payload) == "" {
			return Command{}, errors.New("explain command requires a question")
		}
		if input.Permission != PermissionAdmin {
			return Command{}, errors.New("explain command is not authorized")
		}
		return Command{Kind: CommandExplain, Payload: payload}, nil
	case CommandReconsider:
		if strings.TrimSpace(payload) == "" {
			return Command{}, errors.New("reconsider command requires a reason")
		}
		if input.Permission != PermissionAdmin {
			return Command{}, errors.New("reconsider command is not authorized")
		}
		return Command{Kind: CommandReconsider, Payload: payload}, nil
	default:
		return Command{}, errors.New("unknown Review Agent command")
	}
}
