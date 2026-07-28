package issueagentcli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	issueagentusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
)

const (
	maxCommandInput  = 20 << 20
	maxCommandOutput = 20 << 20
)

// PlanEventRequest is the flattened strict CLI input for reconciliation.
type PlanEventRequest struct {
	Now                 time.Time                          `json:"now"`
	Enabled             bool                               `json:"enabled"`
	RolloutMode         issueagentusecase.RolloutMode      `json:"rollout_mode"`
	ChainStatus         issueagentusecase.ChainStatus      `json:"chain_status"`
	Checkpoint          *issueagentcontract.Checkpoint     `json:"checkpoint,omitempty"`
	CheckpointCommentID int64                              `json:"checkpoint_comment_id,omitempty"`
	CheckpointDigest    string                             `json:"checkpoint_digest,omitempty"`
	Lease               *issueagentusecase.LeaseFacts      `json:"lease,omitempty"`
	Artifacts           []issueagentusecase.WorkerArtifact `json:"artifacts,omitempty"`
}

// PlanSweepRequest is the strict scheduler input for a repository sweep.
type PlanSweepRequest struct {
	Now         time.Time                          `json:"now"`
	Candidates  []issueagentusecase.Candidate      `json:"candidates"`
	Active      []issueagentusecase.ActiveLease    `json:"active"`
	Starts      []issueagentusecase.WorkerStart    `json:"starts"`
	Budget      issueagentusecase.RepositoryBudget `json:"budget"`
	LeaseMargin time.Duration                      `json:"lease_margin"`
}

// DocumentRequest is a closed versioned envelope for composed write commands.
type DocumentRequest struct {
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
}

// Operations are narrow composition-root functions invoked by the CLI.
type Operations struct {
	PlanEvent                func(context.Context, PlanEventRequest) (any, error)
	PlanSweep                func(context.Context, PlanSweepRequest) (any, error)
	PublishLease             func(context.Context, DocumentRequest) (any, error)
	PublishResult            func(context.Context, DocumentRequest) (any, error)
	PublishDraft             func(context.Context, DocumentRequest) (any, error)
	PublishIntake            func(context.Context, DocumentRequest) (any, error)
	PublishAuthorization     func(context.Context, DocumentRequest) (any, error)
	PublishVersionPin        func(context.Context, DocumentRequest) (any, error)
	PublishReproductionLease func(context.Context, DocumentRequest) (any, error)
	PublishWorkerArtifact    func(context.Context, DocumentRequest) (any, error)
	PublishDraftPR           func(context.Context, DocumentRequest) (any, error)
	PublishPhaseLease        func(context.Context, DocumentRequest) (any, error)
	PublishRiskAuthorization func(context.Context, DocumentRequest) (any, error)
	PublishValidationRequest func(context.Context, DocumentRequest) (any, error)
	PublishValidationResult  func(context.Context, DocumentRequest) (any, error)
	PublishExpiredLease      func(context.Context, DocumentRequest) (any, error)
	ReadCurrentCheckpoint    func(context.Context, DocumentRequest) (any, error)
	ReadCurrentTask          func(context.Context, DocumentRequest) (any, error)
	RunWorker                func(context.Context, DocumentRequest) (any, error)
	VerifyCheckpoint         func(context.Context, DocumentRequest) (any, error)
	MintAppToken             func(context.Context, DocumentRequest) (any, error)
}

// Run executes one bounded command and returns a process exit code.
func Run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	operations Operations,
) int {
	if ctx == nil || stdin == nil || stdout == nil || stderr == nil {
		return 2
	}
	if err := ctx.Err(); err != nil {
		writeDiagnostic(stderr, "command cancelled")
		return 1
	}
	if len(args) == 0 {
		writeDiagnostic(stderr, "missing command")
		return 2
	}
	if args[0] == "generate-checkpoint-key" {
		return runGenerateKey(args[1:], stdout, stderr)
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
	case "plan-event":
		var request PlanEventRequest
		if err := decodeStrict(body, &request); err != nil || operations.PlanEvent == nil {
			writeDiagnostic(stderr, "invalid plan-event input")
			return 1
		}
		result, err = operations.PlanEvent(ctx, request)
	case "plan-sweep":
		var request PlanSweepRequest
		if err := decodeStrict(body, &request); err != nil || operations.PlanSweep == nil {
			writeDiagnostic(stderr, "invalid plan-sweep input")
			return 1
		}
		result, err = operations.PlanSweep(ctx, request)
	case "publish-lease":
		result, err = runDocument(ctx, body, operations.PublishLease)
	case "publish-result":
		result, err = runDocument(ctx, body, operations.PublishResult)
	case "publish-draft":
		result, err = runDocument(ctx, body, operations.PublishDraft)
	case "publish-intake":
		result, err = runDocument(ctx, body, operations.PublishIntake)
	case "publish-authorization":
		result, err = runDocument(ctx, body, operations.PublishAuthorization)
	case "publish-version-pin":
		result, err = runDocument(ctx, body, operations.PublishVersionPin)
	case "publish-reproduction-lease":
		result, err = runDocument(ctx, body, operations.PublishReproductionLease)
	case "publish-worker-artifact":
		result, err = runDocument(ctx, body, operations.PublishWorkerArtifact)
	case "publish-draft-pr":
		result, err = runDocument(ctx, body, operations.PublishDraftPR)
	case "publish-phase-lease":
		result, err = runDocument(ctx, body, operations.PublishPhaseLease)
	case "publish-risk-authorization":
		result, err = runDocument(ctx, body, operations.PublishRiskAuthorization)
	case "publish-validation-request":
		result, err = runDocument(ctx, body, operations.PublishValidationRequest)
	case "publish-validation-result":
		result, err = runDocument(ctx, body, operations.PublishValidationResult)
	case "publish-expired-lease":
		result, err = runDocument(ctx, body, operations.PublishExpiredLease)
	case "read-current-checkpoint":
		result, err = runDocument(ctx, body, operations.ReadCurrentCheckpoint)
	case "read-current-task":
		result, err = runDocument(ctx, body, operations.ReadCurrentTask)
	case "run-worker":
		result, err = runDocument(ctx, body, operations.RunWorker)
	case "verify-checkpoint":
		result, err = runDocument(ctx, body, operations.VerifyCheckpoint)
	case "mint-app-token":
		result, err = runDocument(ctx, body, operations.MintAppToken)
	default:
		writeDiagnostic(stderr, "unknown command")
		return 2
	}
	if err != nil {
		writeDiagnostic(stderr, "command failed")
		return 1
	}
	if err := writeResult(stdout, result); err != nil {
		writeDiagnostic(stderr, "write command result")
		return 1
	}
	return 0
}

func runDocument(
	ctx context.Context,
	body []byte,
	operation func(context.Context, DocumentRequest) (any, error),
) (any, error) {
	if operation == nil {
		return nil, errors.New("command operation is unavailable")
	}
	var request DocumentRequest
	if err := decodeStrict(body, &request); err != nil {
		return nil, err
	}
	if request.SchemaVersion != 1 || len(request.Payload) == 0 ||
		len(request.Payload) > maxCommandInput {
		return nil, errors.New("command document is invalid")
	}
	return operation(ctx, request)
}

func parseInputFlag(args []string) (string, error) {
	if len(args) == 0 {
		return "-", nil
	}
	if len(args) != 2 || args[0] != "--input" ||
		strings.TrimSpace(args[1]) == "" {
		return "", errors.New("expected --input <path|->")
	}
	return args[1], nil
}

func readBoundedInput(path string, stdin io.Reader) ([]byte, error) {
	reader := stdin
	var file *os.File
	if path != "-" {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxCommandInput {
			return nil, errors.New("input file is invalid")
		}
		reader = file
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxCommandInput+1))
	if err != nil || len(body) == 0 || len(body) > maxCommandInput {
		return nil, errors.New("command input is empty or oversized")
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
		return errors.New("command input contains trailing JSON")
	}
	return nil
}

func writeResult(stdout io.Writer, result any) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if buffer.Len() > maxCommandOutput {
		return errors.New("command output exceeds limit")
	}
	_, err := io.Copy(stdout, &buffer)
	return err
}

func runGenerateKey(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "--private-key-file" ||
		strings.TrimSpace(args[1]) == "" {
		writeDiagnostic(stderr, "invalid key-generation flags")
		return 2
	}
	privatePath := filepath.Clean(args[1])
	if privatePath == "." {
		writeDiagnostic(stderr, "invalid private-key path")
		return 2
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		writeDiagnostic(stderr, "generate checkpoint key")
		return 1
	}
	file, err := os.OpenFile(privatePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		writeDiagnostic(stderr, "create private-key file")
		return 1
	}
	writeErr := writePrivateKey(file, privateKey)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		writeDiagnostic(stderr, "write private-key file")
		return 1
	}
	sum := sha256.Sum256(publicKey)
	keyID := "checkpoint-" + hex.EncodeToString(sum[:8])
	result := struct {
		KeyID     string `json:"key_id"`
		PublicKey struct {
			ID        string    `json:"id"`
			PublicKey string    `json:"public_key"`
			NotBefore time.Time `json:"not_before"`
			NotAfter  time.Time `json:"not_after"`
		} `json:"public_key_record"`
	}{KeyID: keyID}
	result.PublicKey.ID = keyID
	result.PublicKey.PublicKey = base64.StdEncoding.EncodeToString(publicKey)
	result.PublicKey.NotBefore = time.Now().UTC().Truncate(time.Second)
	result.PublicKey.NotAfter = result.PublicKey.NotBefore.Add(365 * 24 * time.Hour)
	if err := writeResult(stdout, result); err != nil {
		writeDiagnostic(stderr, "write public-key result")
		return 1
	}
	return 0
}

func writePrivateKey(file *os.File, privateKey ed25519.PrivateKey) error {
	if file == nil {
		return errors.New("private-key file is nil")
	}
	encoded := base64.StdEncoding.EncodeToString(privateKey) + "\n"
	if _, err := io.WriteString(file, encoded); err != nil {
		return err
	}
	return file.Sync()
}

func writeDiagnostic(stderr io.Writer, message string) {
	_, _ = fmt.Fprintln(stderr, message)
}
