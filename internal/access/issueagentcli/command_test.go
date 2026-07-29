package issueagentcli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	"github.com/stretchr/testify/require"
)

func TestCommandGenerateCheckpointKeyKeepsPrivateBytesOutOfOutput(t *testing.T) {
	t.Parallel()

	privatePath := filepath.Join(t.TempDir(), "checkpoint.key")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := issueagentcli.Run(
		context.Background(),
		[]string{"generate-checkpoint-key", "--private-key-file", privatePath},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		issueagentcli.Operations{},
	)
	require.Equal(t, 0, exitCode, stderr.String())
	info, err := os.Stat(privatePath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	privateBytes, err := os.ReadFile(privatePath)
	require.NoError(t, err)
	require.NotContains(t, stdout.String(), string(privateBytes))
	require.NotContains(t, stderr.String(), string(privateBytes))
	var public map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &public))
	require.NotEmpty(t, public["key_id"])

	exitCode = issueagentcli.Run(
		context.Background(),
		[]string{"generate-checkpoint-key", "--private-key-file", privatePath},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		issueagentcli.Operations{},
	)
	require.NotEqual(t, 0, exitCode)
}

func TestCommandUsesBoundedStrictJSONAndOneResult(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	var received issueagentcli.PlanEventRequest
	operations := issueagentcli.Operations{
		PlanEvent: func(_ context.Context, request issueagentcli.PlanEventRequest) (any, error) {
			received = request
			return map[string]string{"operation": "wait"}, nil
		},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := issueagentcli.Run(
		context.Background(),
		[]string{"plan-event", "--input", "-"},
		bytes.NewBufferString(`{"now":"`+now.Format(time.RFC3339)+`","enabled":true,"rollout_mode":"intake","chain_status":"missing"}`),
		&stdout,
		&stderr,
		operations,
	)
	require.Equal(t, 0, exitCode, stderr.String())
	require.Equal(t, now, received.Now)
	require.Equal(t, "{\"operation\":\"wait\"}\n", stdout.String())

	stdout.Reset()
	stderr.Reset()
	exitCode = issueagentcli.Run(
		context.Background(),
		[]string{"plan-event", "--input", "-"},
		bytes.NewBufferString(`{"now":"`+now.Format(time.RFC3339)+`","unknown":true}`),
		&stdout,
		&stderr,
		operations,
	)
	require.NotEqual(t, 0, exitCode)
	require.Empty(t, stdout.String())
}

func TestCommandRejectsUnknownCommandCancellationAndSecretDiagnostics(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	require.NotEqual(t, 0, issueagentcli.Run(
		context.Background(), []string{"unknown"}, bytes.NewReader(nil),
		&stdout, &stderr, issueagentcli.Operations{},
	))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stdout.Reset()
	stderr.Reset()
	secret := "super-secret-token"
	require.NotEqual(t, 0, issueagentcli.Run(
		ctx, []string{"plan-event", "--input", "-"},
		bytes.NewBufferString(`{"token":"`+secret+`"}`),
		&stdout, &stderr,
		issueagentcli.Operations{
			PlanEvent: func(context.Context, issueagentcli.PlanEventRequest) (any, error) {
				return nil, errors.New(secret)
			},
		},
	))
	require.NotContains(t, stderr.String(), secret)
}

func TestCommandReportsOnlyAllowlistedWorkerStageDiagnostics(t *testing.T) {
	t.Parallel()

	secret := "provider-secret-detail"
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := issueagentcli.Run(
		context.Background(), []string{"run-worker", "--input", "-"},
		bytes.NewBufferString(`{"schema_version":1,"payload":{}}`),
		&stdout, &stderr,
		issueagentcli.Operations{
			RunWorker: func(
				context.Context,
				issueagentcli.DocumentRequest,
			) (any, error) {
				return nil, testSafeDiagnosticError{
					code: "worker-model-setup", detail: secret,
				}
			},
		},
	)
	require.Equal(t, 1, exitCode)
	require.Empty(t, stdout.String())
	require.Equal(t, "command failed: worker-model-setup\n", stderr.String())
	require.NotContains(t, stderr.String(), secret)

	stderr.Reset()
	exitCode = issueagentcli.Run(
		context.Background(), []string{"run-worker", "--input", "-"},
		bytes.NewBufferString(`{"schema_version":1,"payload":{}}`),
		&stdout, &stderr,
		issueagentcli.Operations{
			RunWorker: func(
				context.Context,
				issueagentcli.DocumentRequest,
			) (any, error) {
				return nil, testSafeDiagnosticError{
					code: "untrusted-value", detail: secret,
				}
			},
		},
	)
	require.Equal(t, 1, exitCode)
	require.Equal(t, "command failed\n", stderr.String())
	require.NotContains(t, stderr.String(), secret)

	stderr.Reset()
	exitCode = issueagentcli.Run(
		context.Background(), []string{"run-worker", "--input", "-"},
		bytes.NewBufferString(`{"schema_version":1,"payload":{}}`),
		&stdout, &stderr,
		issueagentcli.Operations{
			RunWorker: func(
				context.Context,
				issueagentcli.DocumentRequest,
			) (any, error) {
				return nil, &testChangingSafeDiagnosticError{secret: secret}
			},
		},
	)
	require.Equal(t, 1, exitCode)
	require.Equal(t, "command failed: worker-model-setup\n", stderr.String())
	require.NotContains(t, stderr.String(), secret)

	stderr.Reset()
	exitCode = issueagentcli.Run(
		context.Background(), []string{"plan-event", "--input", "-"},
		bytes.NewBufferString(`{}`), &stdout, &stderr,
		issueagentcli.Operations{
			PlanEvent: func(
				context.Context,
				issueagentcli.PlanEventRequest,
			) (any, error) {
				return nil, testSafeDiagnosticError{
					code: "worker-model-setup", detail: secret,
				}
			},
		},
	)
	require.Equal(t, 1, exitCode)
	require.Equal(t, "command failed\n", stderr.String())
	require.NotContains(t, stderr.String(), secret)
}

type testSafeDiagnosticError struct {
	code   string
	detail string
}

func (failure testSafeDiagnosticError) Error() string {
	return failure.detail
}

func (failure testSafeDiagnosticError) SafeDiagnosticCode() string {
	return failure.code
}

type testChangingSafeDiagnosticError struct {
	calls  int
	secret string
}

func (failure *testChangingSafeDiagnosticError) Error() string {
	return failure.secret
}

func (failure *testChangingSafeDiagnosticError) SafeDiagnosticCode() string {
	failure.calls++
	if failure.calls == 1 {
		return "worker-model-setup"
	}
	return failure.secret
}
