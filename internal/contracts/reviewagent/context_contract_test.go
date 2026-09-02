package reviewagent_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
)

func TestReviewContextRoundTripBindsCompleteInventory(t *testing.T) {
	t.Parallel()

	context := validContext(t)
	require.NoError(t, reviewagent.ValidateReviewContext(context))
	body, err := json.Marshal(context)
	require.NoError(t, err)

	decoded, err := reviewagent.DecodeReviewContext(
		strings.NewReader(string(body)),
		int64(len(body)),
	)
	require.NoError(t, err)
	require.Equal(t, context, decoded)

	digestBefore, err := reviewagent.ReviewContextDigest(context)
	require.NoError(t, err)
	digestAfter, err := reviewagent.ReviewContextDigest(decoded)
	require.NoError(t, err)
	require.Equal(t, digestBefore, digestAfter)

	decoded.ChangedFiles[0].Additions++
	changedDigest, err := reviewagent.ReviewContextDigest(decoded)
	require.NoError(t, err)
	require.NotEqual(t, digestBefore, changedDigest)
}

func TestDecodeReviewContextRejectsUntrustedJSONShape(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(validContext(t))
	require.NoError(t, err)
	unknown := strings.Replace(
		string(body),
		`"schema_version":1`,
		`"schema_version":1,"publish_review":true`,
		1,
	)

	for name, input := range map[string]string{
		"unknown authority": unknown,
		"trailing value":    string(body) + `{}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, decodeErr := reviewagent.DecodeReviewContext(
				strings.NewReader(input),
				int64(len(input)),
			)
			require.Error(t, decodeErr)
		})
	}

	_, err = reviewagent.DecodeReviewContext(
		strings.NewReader(string(body)),
		int64(len(body)-1),
	)
	require.EqualError(t, err, "JSON input exceeds byte limit")
}

func TestReviewContextRejectsIncompleteOrAmbiguousInventories(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*reviewagent.ReviewContext){
		"schema version": func(context *reviewagent.ReviewContext) {
			context.SchemaVersion = 2
		},
		"control digest": func(context *reviewagent.ReviewContext) {
			context.PolicyDigest = "sha256:short"
		},
		"intent title": func(context *reviewagent.ReviewContext) {
			context.Title = ""
		},
		"duplicate linked issue": func(context *reviewagent.ReviewContext) {
			context.LinkedIssues = append(
				context.LinkedIssues,
				context.LinkedIssues[0],
			)
		},
		"invalid linked issue state": func(context *reviewagent.ReviewContext) {
			context.LinkedIssues[0].State = "merged"
		},
		"duplicate review thread": func(context *reviewagent.ReviewContext) {
			context.ReviewThreads = append(
				context.ReviewThreads,
				context.ReviewThreads[0],
			)
		},
		"invalid review thread path": func(context *reviewagent.ReviewContext) {
			context.ReviewThreads[0].Path = "../queue.go"
		},
		"duplicate discussion": func(context *reviewagent.ReviewContext) {
			context.Discussion = append(
				context.Discussion,
				context.Discussion[0],
			)
		},
		"formal review carries a path": func(context *reviewagent.ReviewContext) {
			context.Discussion[0].Path = "queue.go"
		},
		"issue comment carries state": func(context *reviewagent.ReviewContext) {
			context.Discussion[1].State = "open"
		},
		"review comment has invalid side": func(context *reviewagent.ReviewContext) {
			context.Discussion[2].Side = "MIDDLE"
		},
		"review comment line without side": func(context *reviewagent.ReviewContext) {
			context.Discussion[2].Side = ""
		},
		"unknown discussion kind": func(context *reviewagent.ReviewContext) {
			context.Discussion[0].Kind = "status"
		},
		"forged prior finding digest": func(context *reviewagent.ReviewContext) {
			context.PriorFindings[0].Digest = digest("f")
		},
		"duplicate prior finding": func(context *reviewagent.ReviewContext) {
			context.PriorFindings = append(
				context.PriorFindings,
				context.PriorFindings[0],
			)
		},
		"empty changed-file inventory": func(context *reviewagent.ReviewContext) {
			context.ChangedFiles = nil
		},
		"duplicate changed path": func(context *reviewagent.ReviewContext) {
			context.ChangedFiles = append(
				context.ChangedFiles,
				context.ChangedFiles[0],
			)
		},
		"duplicate context document": func(context *reviewagent.ReviewContext) {
			context.ContextDocuments = append(
				context.ContextDocuments,
				context.ContextDocuments[0],
			)
		},
		"invalid context document scope": func(context *reviewagent.ReviewContext) {
			context.ContextDocuments[0].Scope = "../internal"
		},
		"empty mandatory checks": func(context *reviewagent.ReviewContext) {
			context.MandatoryChecks = nil
		},
		"invalid mandatory check name": func(context *reviewagent.ReviewContext) {
			context.MandatoryChecks[0] = "Go Unit"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			context := validContext(t)
			mutate(&context)
			require.Error(t, reviewagent.ValidateReviewContext(context))
		})
	}
}

func TestReviewContextRejectsChangedFileContentConfusion(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*reviewagent.ChangedFile){
		"absolute path": func(file *reviewagent.ChangedFile) {
			file.Path = "/internal/runtime/delivery/queue.go"
		},
		"patch digest mismatch": func(file *reviewagent.ChangedFile) {
			file.PatchDigest = digest("f")
		},
		"text content digest mismatch": func(file *reviewagent.ChangedFile) {
			file.Content += "// injected\n"
		},
		"unsupported mode": func(file *reviewagent.ChangedFile) {
			file.Mode = "120000"
		},
		"unsupported type": func(file *reviewagent.ChangedFile) {
			file.Type = "symlink"
		},
		"non-rename previous path": func(file *reviewagent.ChangedFile) {
			file.PreviousPath = "internal/runtime/delivery/old.go"
		},
		"invalid status": func(file *reviewagent.ChangedFile) {
			file.Status = "copied"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			context := validContext(t)
			mutate(&context.ChangedFiles[0])
			require.Error(t, reviewagent.ValidateReviewContext(context))
		})
	}

	t.Run("binary raw content", func(t *testing.T) {
		t.Parallel()
		context := validContext(t)
		context.ChangedFiles[1].Content = "raw bytes"
		require.EqualError(
			t,
			reviewagent.ValidateReviewContext(context),
			"binary Review context file exposes raw content",
		)
	})

	t.Run("valid rename", func(t *testing.T) {
		t.Parallel()
		context := validContext(t)
		file := &context.ChangedFiles[0]
		file.Status = reviewagent.FileStatusRenamed
		file.PreviousPath = "internal/runtime/delivery/old_queue.go"
		require.NoError(t, reviewagent.ValidateReviewContext(context))
	})

	t.Run("rename to same path", func(t *testing.T) {
		t.Parallel()
		context := validContext(t)
		file := &context.ChangedFiles[0]
		file.Status = reviewagent.FileStatusRenamed
		file.PreviousPath = file.Path
		require.EqualError(
			t,
			reviewagent.ValidateReviewContext(context),
			"invalid Review context rename",
		)
	})
}
