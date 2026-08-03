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
			name: "admin may start review",
			input: reviewagent.CommandInput{
				Body:       "@review-agent review",
				Permission: reviewagent.PermissionAdmin,
			},
			want: reviewagent.CommandReview,
		},
		{
			name: "admin may request bounded explanation",
			input: reviewagent.CommandInput{
				Body:       "@review-agent explain Why is this blocking?",
				Permission: reviewagent.PermissionAdmin,
			},
			want:    reviewagent.CommandExplain,
			payload: "Why is this blocking?",
		},
		{
			name: "admin may reconsider",
			input: reviewagent.CommandInput{
				Body:       "@review-agent reconsider The queue is node-local.",
				Permission: reviewagent.PermissionAdmin,
			},
			want:    reviewagent.CommandReconsider,
			payload: "The queue is node-local.",
		},
		{
			name: "admin may retry",
			input: reviewagent.CommandInput{
				Body:       "@review-agent retry",
				Permission: reviewagent.PermissionAdmin,
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
			Body:       "@review-agent reconsider",
			Permission: reviewagent.PermissionAdmin,
		},
		"non-admin review": {
			Body:       "@review-agent review",
			Permission: reviewagent.PermissionMaintain,
		},
		"non-admin explain": {
			Body:       "@review-agent explain Why?",
			Permission: reviewagent.PermissionWrite,
		},
		"non-admin reconsider": {
			Body:       "@review-agent reconsider Please retry.",
			Permission: reviewagent.PermissionMaintain,
		},
		"non-admin retry": {
			Body:       "@review-agent retry",
			Permission: reviewagent.PermissionWrite,
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
