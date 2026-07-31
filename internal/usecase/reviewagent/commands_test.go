package reviewagent_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	reviewagent "github.com/WuKongIM/WuKongIM/internal/usecase/reviewagent"
)

func TestParseCommandAcceptsOnlyExactAuthorizedSurface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   reviewagent.CommandInput
		want    reviewagent.CommandKind
		payload string
	}{
		{
			name:  "status is public",
			input: reviewagent.CommandInput{Body: "@review-agent status"},
			want:  reviewagent.CommandStatus,
		},
		{
			name: "explain is public and bounded",
			input: reviewagent.CommandInput{
				Body: "@review-agent explain Why is this blocking?",
			},
			want:    reviewagent.CommandExplain,
			payload: "Why is this blocking?",
		},
		{
			name: "author may reconsider",
			input: reviewagent.CommandInput{
				Body:     "@review-agent reconsider The queue is node-local.",
				Actor:    "alice",
				PRWriter: "alice",
			},
			want:    reviewagent.CommandReconsider,
			payload: "The queue is node-local.",
		},
		{
			name: "maintainer may retry",
			input: reviewagent.CommandInput{
				Body:       "@review-agent retry",
				Permission: reviewagent.PermissionMaintain,
			},
			want: reviewagent.CommandRetry,
		},
		{
			name: "admin may cancel",
			input: reviewagent.CommandInput{
				Body:       "@review-agent cancel",
				Permission: reviewagent.PermissionAdmin,
			},
			want: reviewagent.CommandCancel,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command, err := reviewagent.ParseCommand(test.input)
			require.NoError(t, err)
			require.Equal(t, test.want, command.Kind)
			require.Equal(t, test.payload, command.Payload)
		})
	}
}

func TestParseCommandRejectsAmbiguousOrUnauthorizedText(t *testing.T) {
	t.Parallel()

	tests := map[string]reviewagent.CommandInput{
		"quoted": {
			Body: "> @review-agent status",
		},
		"code block": {
			Body:       "```\n@review-agent retry\n```",
			Permission: reviewagent.PermissionAdmin,
		},
		"ordinary prose": {
			Body: "please run @review-agent status",
		},
		"multiple lines": {
			Body:       "@review-agent status\n@review-agent retry",
			Permission: reviewagent.PermissionAdmin,
		},
		"edited": {
			Body:   "@review-agent status",
			Edited: true,
		},
		"missing reason": {
			Body:     "@review-agent reconsider",
			Actor:    "alice",
			PRWriter: "alice",
		},
		"unauthorized reconsider": {
			Body:  "@review-agent reconsider Please retry.",
			Actor: "bob",
		},
		"unauthorized retry": {
			Body: "@review-agent retry",
		},
		"unknown": {
			Body:       "@review-agent approve",
			Permission: reviewagent.PermissionAdmin,
		},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := reviewagent.ParseCommand(input)
			require.Error(t, err)
		})
	}
}
