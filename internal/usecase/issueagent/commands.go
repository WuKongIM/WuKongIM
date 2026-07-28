package issueagent

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	adoptHeadCommandPattern = regexp.MustCompile(`^/agent adopt-head ([0-9a-f]{40})$`)
	backportCommandPattern  = regexp.MustCompile(`^/agent backport ([A-Za-z0-9][A-Za-z0-9._/-]{0,127})$`)
	recoverCommandPattern   = regexp.MustCompile(`^/agent recover-chain ([1-9][0-9]*) (sha256:[0-9a-f]{64})$`)
)

// CommandKind is one exact maintainer control command.
type CommandKind string

const (
	CommandRevise        CommandKind = "revise"
	CommandCancel        CommandKind = "cancel"
	CommandAddressReview CommandKind = "address_review"
	CommandAdoptHead     CommandKind = "adopt_head"
	CommandBackport      CommandKind = "backport"
	CommandRecoverChain  CommandKind = "recover_chain"
)

// CommandActor is a fresh GitHub actor permission snapshot.
type CommandActor struct {
	Login      string
	Type       string
	Permission Permission
}

// CommandPolicy contains trusted allowlists for parameterized commands.
type CommandPolicy struct {
	AllowedBackportBranches []string
}

// CommandIntent is a parsed command with no executable text.
type CommandIntent struct {
	Kind                CommandKind
	HeadSHA             string
	BackportBranch      string
	CheckpointCommentID int64
	CheckpointDigest    string
	Actor               string
}

// ParseCommand accepts only an exact first-line command from an authorized user.
func ParseCommand(
	body string,
	actor CommandActor,
	policy CommandPolicy,
) (CommandIntent, error) {
	if len(body) == 0 || len(body) > 64<<10 {
		return CommandIntent{}, errors.New("command comment is empty or oversized")
	}
	if actor.Type != "User" || actor.Login == "" || !canAuthorize(actor.Permission) {
		return CommandIntent{}, errors.New("actor cannot issue Issue Agent commands")
	}
	firstLine, _, _ := strings.Cut(body, "\n")
	firstLine = strings.TrimSuffix(firstLine, "\r")
	if len(firstLine) > 512 {
		return CommandIntent{}, errors.New("command line is oversized")
	}

	intent := CommandIntent{Actor: actor.Login}
	switch firstLine {
	case "/agent revise":
		intent.Kind = CommandRevise
	case "/agent cancel":
		intent.Kind = CommandCancel
	case "/agent address-review":
		intent.Kind = CommandAddressReview
	default:
		if matches := adoptHeadCommandPattern.FindStringSubmatch(firstLine); matches != nil {
			intent.Kind = CommandAdoptHead
			intent.HeadSHA = matches[1]
		} else if matches := backportCommandPattern.FindStringSubmatch(firstLine); matches != nil {
			if !slices.Contains(policy.AllowedBackportBranches, matches[1]) {
				return CommandIntent{}, fmt.Errorf("backport branch %q is not allowed", matches[1])
			}
			intent.Kind = CommandBackport
			intent.BackportBranch = matches[1]
		} else if matches := recoverCommandPattern.FindStringSubmatch(firstLine); matches != nil {
			if actor.Permission != PermissionAdmin {
				return CommandIntent{}, errors.New("checkpoint-chain recovery requires admin permission")
			}
			commentID, err := strconv.ParseInt(matches[1], 10, 64)
			if err != nil {
				return CommandIntent{}, errors.New("invalid checkpoint comment ID")
			}
			intent.Kind = CommandRecoverChain
			intent.CheckpointCommentID = commentID
			intent.CheckpointDigest = matches[2]
		} else {
			return CommandIntent{}, errors.New("comment does not begin with an exact Issue Agent command")
		}
	}
	return intent, nil
}
