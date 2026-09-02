package issueagent_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	"github.com/stretchr/testify/require"
)

func TestContextBundleStrictRoundTripPreservesBoundedEvidence(t *testing.T) {
	t.Parallel()

	want := validContextBundle()
	body, err := json.Marshal(want)
	require.NoError(t, err)

	got, err := issueagent.DecodeContextBundle(bytes.NewReader(body), int64(len(body)))
	require.NoError(t, err)
	require.Equal(t, want, got)

	digest, err := issueagent.ContextBundleDigest(got)
	require.NoError(t, err)
	got.Untrusted.Comments[0].Body = "changed observation"
	changed, err := issueagent.ContextBundleDigest(got)
	require.NoError(t, err)
	require.NotEqual(t, digest, changed, "comment evidence must be bound to the task")
}

func TestContextBundleRejectsUntrustedOrderingAndRepositoryEscapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*issueagent.ContextBundle)
	}{
		{
			name: "issue identity mismatch",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Untrusted.Issue.Number++
			},
		},
		{
			name: "comments are not strictly ordered",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Untrusted.Comments[1].ID = bundle.Untrusted.Comments[0].ID
			},
		},
		{
			name: "comment contains a control byte",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Untrusted.Comments[0].Body = "unsafe\x00body"
			},
		},
		{
			name: "review path escapes repository",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Untrusted.ReviewThreads[0].Path = "../secret"
			},
		},
		{
			name: "review thread has no complete comments",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Untrusted.ReviewThreads[0].Comments = nil
			},
		},
		{
			name: "review comments are not strictly ordered",
			mutate: func(bundle *issueagent.ContextBundle) {
				comment := bundle.Untrusted.ReviewThreads[0].Comments[0]
				bundle.Untrusted.ReviewThreads[0].Comments = append(
					bundle.Untrusted.ReviewThreads[0].Comments, comment,
				)
			},
		},
		{
			name: "too many issue comments",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Untrusted.Comments = make([]issueagent.CommentSnapshot, 257)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bundle := validContextBundle()
			test.mutate(&bundle)
			require.Error(t, issueagent.ValidateContextBundle(bundle))
		})
	}
}

func TestContextBundleRejectsUntrustedOrUnboundedAuthority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*issueagent.ContextBundle)
	}{
		{
			name: "unknown task kind",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Task.Kind = issueagent.TaskKind("release")
			},
		},
		{
			name: "read permission cannot authorize",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.Authorization.Permission = "read"
			},
		},
		{
			name: "issue prose cannot introduce a command",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.Authorization.Command = "/agent publish"
			},
		},
		{
			name: "required tests must be sorted",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.RequiredTests = []string{"unit", "focused"}
			},
		},
		{
			name: "required tests cannot be empty",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.RequiredTests = nil
			},
		},
		{
			name: "context documents must be strictly sorted",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.ContextDocumentDigests[1].Path = "AGENTS.md"
			},
		},
		{
			name: "context document SHA must be a git object identity",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.ContextDocumentDigests[0].GitBlobSHA = "not-a-sha"
			},
		},
		{
			name: "knowledge path cannot escape repository",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.KnowledgePaths[0] = "../PROJECT_KNOWLEDGE.md"
			},
		},
		{
			name: "wall time must be bounded",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.Limits.WallTimeSeconds = 5401
			},
		},
		{
			name: "modify iterations cannot be zero",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.Trusted.Limits.ModifyTestIterations = 0
			},
		},
		{
			name: "creation timestamp must be UTC",
			mutate: func(bundle *issueagent.ContextBundle) {
				bundle.CreatedAt = bundle.CreatedAt.In(time.FixedZone("offset", 3600))
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			bundle := validContextBundle()
			test.mutate(&bundle)
			require.Error(t, issueagent.ValidateContextBundle(bundle))
		})
	}
}

func TestDecodeContextBundleFailsClosedOnEnvelopeAmbiguity(t *testing.T) {
	t.Parallel()

	bundle := validContextBundle()
	body, err := json.Marshal(bundle)
	require.NoError(t, err)

	_, err = issueagent.DecodeContextBundle(strings.NewReader(string(body)+" {}"), int64(len(body)+3))
	require.EqualError(t, err, "JSON input contains multiple values")

	_, err = issueagent.DecodeContextBundle(bytes.NewReader(body), int64(len(body)-1))
	require.EqualError(t, err, "JSON input exceeds byte limit")
}
