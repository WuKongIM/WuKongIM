package issueagentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	issueagentverify "github.com/WuKongIM/WuKongIM/internal/runtime/issueagentverify"
)

const (
	maxCommandInput  = 40 << 20
	maxCommandOutput = 40 << 20
)

// ReconcileGitHubRequest identifies one event hint or bounded sweep.
type ReconcileGitHubRequest struct {
	Repository  string    `json:"repository"`
	EventName   string    `json:"event_name"`
	EventPath   string    `json:"event_path"`
	IssueNumber int64     `json:"issue_number"`
	ControlSHA  string    `json:"control_sha"`
	Now         time.Time `json:"now"`
}

// RecoverTaskRequest binds a reusable workflow to one signed task.
type RecoverTaskRequest struct {
	Repository   string `json:"repository"`
	IssueNumber  int64  `json:"issue_number"`
	TaskID       string `json:"task_id"`
	BaseSHA      string `json:"base_sha"`
	ControlSHA   string `json:"control_sha"`
	StateHeadSHA string `json:"state_head_sha"`
}

// BuildContextRequest asks for one freshly re-derived Context Bundle.
type BuildContextRequest struct {
	Repository   string    `json:"repository"`
	IssueNumber  int64     `json:"issue_number"`
	TaskID       string    `json:"task_id"`
	ControlSHA   string    `json:"control_sha"`
	StateHeadSHA string    `json:"state_head_sha"`
	Now          time.Time `json:"now"`
}

// CaptureCandidateRequest is the strict trusted capture boundary.
type CaptureCandidateRequest struct {
	Baseline  string                         `json:"baseline"`
	Workspace string                         `json:"workspace"`
	TaskID    string                         `json:"task_id"`
	BaseSHA   string                         `json:"base_sha"`
	Limits    issueagentverify.CaptureLimits `json:"limits"`
}

// VerifyCandidateRequest is the strict clean-checkout Verifier boundary.
type VerifyCandidateRequest struct {
	Checkout      string                              `json:"checkout"`
	TemporaryRoot string                              `json:"temporary_root"`
	Snapshot      issueagentverify.CandidateSnapshot  `json:"snapshot"`
	Policy        issueagentverify.VerificationPolicy `json:"policy"`
	Now           time.Time                           `json:"now"`
}

// MintAppTokenRequest selects only the already configured repository.
type MintAppTokenRequest struct {
	Repository string `json:"repository"`
}

// PublishCandidateRequest references bounded artifacts outside the checkout.
type PublishCandidateRequest struct {
	Repository         string    `json:"repository"`
	IssueNumber        int64     `json:"issue_number"`
	ControlSHA         string    `json:"control_sha"`
	ExpectedStateHead  string    `json:"expected_state_head"`
	ContextPath        string    `json:"context_path"`
	EngineerResultPath string    `json:"engineer_result_path"`
	CandidatePath      string    `json:"candidate_path"`
	EvidencePath       string    `json:"evidence_path"`
	Now                time.Time `json:"now"`
}

// Operations are the seven narrow v2 composition-root functions.
type Operations struct {
	ReconcileGitHub  func(context.Context, ReconcileGitHubRequest) (any, error)
	RecoverTask      func(context.Context, RecoverTaskRequest) (any, error)
	BuildContext     func(context.Context, BuildContextRequest) (any, error)
	CaptureCandidate func(context.Context, CaptureCandidateRequest) (any, error)
	VerifyCandidate  func(context.Context, VerifyCandidateRequest) (any, error)
	MintAppToken     func(context.Context, MintAppTokenRequest) (any, error)
	PublishCandidate func(context.Context, PublishCandidateRequest) (any, error)
}

// Run executes one strict JSON command and returns a process exit code.
func Run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	operations Operations,
) int {
	if ctx == nil || stdin == nil || stdout == nil || stderr == nil ||
		len(args) == 0 {
		writeDiagnostic(stderr, "missing command")
		return 2
	}
	inputPath, err := parseInputFlag(args[1:])
	if err != nil {
		writeDiagnostic(stderr, "invalid command flags")
		return 2
	}
	body, err := readBoundedInput(inputPath, stdin)
	if err != nil {
		writeDiagnostic(stderr, "read command input")
		return 1
	}
	var result any
	switch args[0] {
	case "reconcile-github":
		var request ReconcileGitHubRequest
		if decodeStrict(body, &request) != nil ||
			operations.ReconcileGitHub == nil {
			return invalidInput(stderr, "reconcile-github")
		}
		result, err = operations.ReconcileGitHub(ctx, request)
	case "recover-task":
		var request RecoverTaskRequest
		if decodeStrict(body, &request) != nil ||
			operations.RecoverTask == nil {
			return invalidInput(stderr, "recover-task")
		}
		result, err = operations.RecoverTask(ctx, request)
	case "build-context":
		var request BuildContextRequest
		if decodeStrict(body, &request) != nil ||
			operations.BuildContext == nil {
			return invalidInput(stderr, "build-context")
		}
		result, err = operations.BuildContext(ctx, request)
	case "capture-candidate":
		var request CaptureCandidateRequest
		if decodeStrict(body, &request) != nil ||
			operations.CaptureCandidate == nil {
			return invalidInput(stderr, "capture-candidate")
		}
		result, err = operations.CaptureCandidate(ctx, request)
	case "verify-candidate":
		var request VerifyCandidateRequest
		if decodeStrict(body, &request) != nil ||
			operations.VerifyCandidate == nil {
			return invalidInput(stderr, "verify-candidate")
		}
		result, err = operations.VerifyCandidate(ctx, request)
	case "mint-app-token":
		var request MintAppTokenRequest
		if decodeStrict(body, &request) != nil ||
			operations.MintAppToken == nil {
			return invalidInput(stderr, "mint-app-token")
		}
		result, err = operations.MintAppToken(ctx, request)
	case "publish-candidate":
		var request PublishCandidateRequest
		if decodeStrict(body, &request) != nil ||
			operations.PublishCandidate == nil {
			return invalidInput(stderr, "publish-candidate")
		}
		result, err = operations.PublishCandidate(ctx, request)
	default:
		writeDiagnostic(stderr, "unknown command")
		return 2
	}
	if err != nil {
		writeDiagnostic(stderr, "command failed")
		return 1
	}
	if err := writeBoundedJSON(stdout, result); err != nil {
		writeDiagnostic(stderr, "write command output")
		return 1
	}
	return 0
}

func invalidInput(stderr io.Writer, command string) int {
	writeDiagnostic(stderr, "invalid "+command+" input")
	return 1
}

func parseInputFlag(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) != 2 || args[0] != "--input" ||
		strings.TrimSpace(args[1]) == "" {
		return "", errors.New("invalid input flag")
	}
	return args[1], nil
}

func readBoundedInput(path string, stdin io.Reader) ([]byte, error) {
	reader := stdin
	var file *os.File
	if path != "" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return nil, errors.New("open command input")
		}
		defer file.Close()
		reader = file
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxCommandInput+1))
	if err != nil || len(body) > maxCommandInput {
		return nil, errors.New("command input exceeds limit")
	}
	return body, nil
}

func decodeStrict(body []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON input")
	}
	return nil
}

func writeBoundedJSON(writer io.Writer, value any) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if buffer.Len() > maxCommandOutput {
		return errors.New("command output exceeds limit")
	}
	_, err := io.Copy(writer, &buffer)
	return err
}

func writeDiagnostic(writer io.Writer, message string) {
	_, _ = fmt.Fprintln(writer, "wkissueagent:", message)
}
