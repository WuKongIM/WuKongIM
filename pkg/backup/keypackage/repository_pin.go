package keypackage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	deploymentKeyPinSchema     = "wukongim/backup-key-package-pin/v1"
	deploymentKeyRootPinKey    = "control/key-authority/root-v1.json"
	deploymentKeyPinKindRoot   = "root"
	deploymentKeyPinKindActive = "active"
	maxDeploymentKeyPinBytes   = 16 << 10
)

var (
	// ErrRepositoryPinPending reports that this node must wait for the
	// deterministic Controller voter to publish a missing immutable pin.
	ErrRepositoryPinPending = errors.New(
		"backup deployment keys: repository pin publication is pending",
	)
)

// RepositoryPinnedAuthority prevents package substitution and rollback by
// pinning the package identity and activated revisions in both repositories.
type RepositoryPinnedAuthority struct {
	*DeploymentKeyAuthority
	// primary is the first independent immutable repository copy.
	primary backupartifact.Repository
	// secondary is the second independent immutable repository copy.
	secondary backupartifact.Repository
	// canPublish permits only the deterministic Controller voter to create pins.
	canPublish func() bool
	// checkMu serializes repository pin verification and publication.
	checkMu sync.Mutex
	// qualified gates every runtime cryptographic operation.
	qualified atomic.Bool
}

// deploymentKeyPinRecord is one public signed package-identity or activation
// record. It never contains secret key material.
type deploymentKeyPinRecord struct {
	// Schema identifies the strict repository pin format.
	Schema string `json:"schema"`
	// Kind distinguishes the immutable root from activation revisions.
	Kind string `json:"kind"`
	// PackageID pins the one deployment trust root accepted by a repository.
	PackageID string `json:"package_id"`
	// RepositoryID prevents a pin from crossing repository namespaces.
	RepositoryID string `json:"repository_id"`
	// Revision is the odd activated package revision represented by this pin.
	Revision uint64 `json:"revision"`
	// PreviousActiveRevision links activation pins into a monotonic chain.
	PreviousActiveRevision uint64 `json:"previous_active_revision"`
	// Signature authenticates all preceding fields with the revision signer.
	Signature backupartifact.ManifestSignature `json:"signature"`
}

// NewRepositoryPinnedAuthority binds one validated deployment authority to two
// distinct immutable repositories without adding operator configuration.
func NewRepositoryPinnedAuthority(
	authority *DeploymentKeyAuthority,
	primary backupartifact.Repository,
	secondary backupartifact.Repository,
	canPublish func() bool,
) (*RepositoryPinnedAuthority, error) {
	if authority == nil || primary == nil || secondary == nil ||
		canPublish == nil {
		return nil, fmt.Errorf(
			"backup deployment keys: authority, repositories, and publisher fence are required",
		)
	}
	if strings.TrimSpace(primary.Name()) == "" ||
		strings.TrimSpace(secondary.Name()) == "" ||
		primary.Name() == secondary.Name() {
		return nil, fmt.Errorf(
			"backup deployment keys: distinct repositories are required",
		)
	}
	return &RepositoryPinnedAuthority{
		DeploymentKeyAuthority: authority,
		primary:                primary,
		secondary:              secondary,
		canPublish:             canPublish,
	}, nil
}

// Qualify opens the runtime cryptographic gate after the composition root has
// verified both repository controls and this authority's immutable pins.
func (a *RepositoryPinnedAuthority) Qualify() {
	if a != nil {
		a.qualified.Store(true)
	}
}

// Invalidate closes the runtime cryptographic gate before qualification and
// whenever any repository, pin, staging, or clock doctor check fails.
func (a *RepositoryPinnedAuthority) Invalidate() {
	if a != nil {
		a.qualified.Store(false)
	}
}

// NewDataKey delegates only after the complete runtime qualification gate has
// succeeded.
func (a *RepositoryPinnedAuthority) NewDataKey(
	ctx context.Context,
) (backupartifact.DataKey, error) {
	if err := a.requireQualified(ctx); err != nil {
		return backupartifact.DataKey{}, err
	}
	return a.DeploymentKeyAuthority.NewDataKey(ctx)
}

// OpenDataKey delegates only while the complete runtime qualification gate is
// healthy.
func (a *RepositoryPinnedAuthority) OpenDataKey(
	ctx context.Context,
	envelope backupartifact.DataKeyEnvelope,
) ([]byte, error) {
	if err := a.requireQualified(ctx); err != nil {
		return nil, err
	}
	return a.DeploymentKeyAuthority.OpenDataKey(ctx, envelope)
}

// Sign delegates only while the complete runtime qualification gate is
// healthy.
func (a *RepositoryPinnedAuthority) Sign(
	ctx context.Context,
	message []byte,
) (backupartifact.ManifestSignature, error) {
	if err := a.requireQualified(ctx); err != nil {
		return backupartifact.ManifestSignature{}, err
	}
	return a.DeploymentKeyAuthority.Sign(ctx, message)
}

// Verify delegates only while the complete runtime qualification gate is
// healthy.
func (a *RepositoryPinnedAuthority) Verify(
	ctx context.Context,
	signature backupartifact.ManifestSignature,
	message []byte,
) error {
	if err := a.requireQualified(ctx); err != nil {
		return err
	}
	return a.DeploymentKeyAuthority.Verify(ctx, signature, message)
}

// requireQualified rejects every runtime crypto operation until the complete
// external doctor has explicitly opened the gate.
func (a *RepositoryPinnedAuthority) requireQualified(
	ctx context.Context,
) error {
	if a == nil || a.DeploymentKeyAuthority == nil {
		return fmt.Errorf(
			"backup deployment keys: repository-pinned authority is unavailable",
		)
	}
	if ctx == nil {
		return fmt.Errorf("backup deployment keys: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !a.qualified.Load() {
		return fmt.Errorf(
			"backup deployment keys: runtime qualification is required",
		)
	}
	return nil
}

// Check proves local cryptographic readiness and repository-pinned package
// identity/freshness before the authority may protect backup operations.
func (a *RepositoryPinnedAuthority) Check(
	ctx context.Context,
) (resultErr error) {
	if a == nil || a.DeploymentKeyAuthority == nil ||
		a.primary == nil || a.secondary == nil || a.canPublish == nil {
		return fmt.Errorf(
			"backup deployment keys: repository-pinned authority is unavailable",
		)
	}
	a.checkMu.Lock()
	defer a.checkMu.Unlock()
	defer func() {
		if resultErr != nil {
			a.Invalidate()
		}
	}()
	if err := a.DeploymentKeyAuthority.Check(ctx); err != nil {
		return err
	}
	for _, repository := range []backupartifact.Repository{
		a.primary, a.secondary,
	} {
		if err := a.checkRepository(ctx, repository); err != nil {
			return fmt.Errorf(
				"backup deployment keys: repository %q pin: %w",
				repository.Name(), err,
			)
		}
	}
	return nil
}

// checkRepository establishes revision one, extends active revision pins, and
// rejects any locally older package against one immutable repository.
func (a *RepositoryPinnedAuthority) checkRepository(
	ctx context.Context,
	repository backupartifact.Repository,
) error {
	root, found, err := a.readPin(
		ctx, repository, deploymentKeyRootPinKey,
	)
	if err != nil {
		return err
	}
	if !found {
		if a.revision != 1 || a.staged {
			return fmt.Errorf(
				"package root pin is missing for revision %d", a.revision,
			)
		}
		root = a.pinRecord(deploymentKeyPinKindRoot, 1, 0)
		if err := a.putOrVerifyPin(
			ctx, repository, deploymentKeyRootPinKey, root,
		); err != nil {
			return err
		}
	} else if err := a.validatePin(
		ctx, root, deploymentKeyPinKindRoot, 1, 0,
	); err != nil {
		return err
	}

	previousActiveRevision := a.previousActiveRevision()
	if previousActiveRevision > 1 {
		previousKey := deploymentKeyActivePinKey(previousActiveRevision)
		previous, found, err := a.readPin(ctx, repository, previousKey)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf(
				"previous active revision %d pin is missing",
				previousActiveRevision,
			)
		}
		if err := a.validatePin(
			ctx, previous, deploymentKeyPinKindActive,
			previousActiveRevision, previousActiveRevision-2,
		); err != nil {
			return err
		}
	}

	if !a.staged && a.revision > 1 {
		currentKey := deploymentKeyActivePinKey(a.revision)
		current := a.pinRecord(
			deploymentKeyPinKindActive,
			a.revision,
			a.revision-2,
		)
		if err := a.putOrVerifyPin(
			ctx, repository, currentKey, current,
		); err != nil {
			return err
		}
	}

	nextRevision := a.revision + 2
	if a.staged {
		nextRevision = a.revision + 1
	}
	next, found, err := a.readPin(
		ctx, repository, deploymentKeyActivePinKey(nextRevision),
	)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := a.validatePin(
		ctx, next, deploymentKeyPinKindActive,
		nextRevision, nextRevision-2,
	); err != nil {
		return fmt.Errorf(
			"newer active revision %d pin is invalid: %w",
			nextRevision, err,
		)
	}
	return fmt.Errorf(
		"package rollback detected: local revision %d is older than active revision %d",
		a.revision, nextRevision,
	)
}

// previousActiveRevision identifies the pin that must already exist before
// this staged or active revision is accepted.
func (a *RepositoryPinnedAuthority) previousActiveRevision() uint64 {
	if a.staged {
		return a.revision - 1
	}
	if a.revision <= 1 {
		return 1
	}
	return a.revision - 2
}

// pinRecord builds one unsigned pin bound to this authority and repository ID.
func (a *RepositoryPinnedAuthority) pinRecord(
	kind string,
	revision uint64,
	previous uint64,
) deploymentKeyPinRecord {
	return deploymentKeyPinRecord{
		Schema:                 deploymentKeyPinSchema,
		Kind:                   kind,
		PackageID:              a.packageID,
		RepositoryID:           a.repositoryID,
		Revision:               revision,
		PreviousActiveRevision: previous,
	}
}

// deploymentKeyActivePinKey maps one odd activated revision to a stable,
// lexically ordered immutable object key.
func deploymentKeyActivePinKey(revision uint64) string {
	return fmt.Sprintf(
		"control/key-authority/active-%020d.json", revision,
	)
}

// putOrVerifyPin signs and creates one immutable pin, or validates the exact
// record already published by a concurrent node.
func (a *RepositoryPinnedAuthority) putOrVerifyPin(
	ctx context.Context,
	repository backupartifact.Repository,
	key string,
	record deploymentKeyPinRecord,
) error {
	existing, found, err := a.readPin(ctx, repository, key)
	if err != nil {
		return err
	}
	if found {
		return a.validatePin(
			ctx,
			existing,
			record.Kind,
			record.Revision,
			record.PreviousActiveRevision,
		)
	}
	if !a.canPublish() {
		return fmt.Errorf(
			"%w: %s revision %d",
			ErrRepositoryPinPending, record.Kind, record.Revision,
		)
	}
	body, err := a.encodePin(ctx, record)
	if err != nil {
		return err
	}
	checksum := sha256.Sum256(body)
	err = repository.PutImmutable(
		ctx,
		key,
		int64(len(body)),
		hex.EncodeToString(checksum[:]),
		bytes.NewReader(body),
	)
	if err == nil {
		return nil
	}
	if !errors.Is(err, backupartifact.ErrObjectExists) {
		return err
	}
	existing, found, readErr := a.readPin(ctx, repository, key)
	if readErr != nil {
		return readErr
	}
	if !found {
		return fmt.Errorf("concurrent pin publication is not readable")
	}
	return a.validatePin(
		ctx,
		existing,
		record.Kind,
		record.Revision,
		record.PreviousActiveRevision,
	)
}

// encodePin signs the canonical public record and serializes the complete
// immutable trust pin without exposing deployment package secrets.
func (a *RepositoryPinnedAuthority) encodePin(
	ctx context.Context,
	record deploymentKeyPinRecord,
) ([]byte, error) {
	canonical, err := canonicalDeploymentKeyPin(record)
	if err != nil {
		return nil, err
	}
	signature, err := a.DeploymentKeyAuthority.Sign(ctx, canonical)
	if err != nil {
		return nil, err
	}
	record.Signature = signature
	body, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf(
			"backup deployment keys: encode repository pin: %w", err,
		)
	}
	return append(body, '\n'), nil
}

// readPin downloads one bounded immutable record and verifies repository
// checksum metadata before strict decoding.
func (a *RepositoryPinnedAuthority) readPin(
	ctx context.Context,
	repository backupartifact.Repository,
	key string,
) (deploymentKeyPinRecord, bool, error) {
	reader, object, err := repository.Open(ctx, key)
	if errors.Is(err, backupartifact.ErrObjectNotFound) {
		return deploymentKeyPinRecord{}, false, nil
	}
	if err != nil {
		return deploymentKeyPinRecord{}, false, err
	}
	if reader == nil {
		return deploymentKeyPinRecord{}, false, fmt.Errorf(
			"repository pin reader is nil",
		)
	}
	body, readErr := io.ReadAll(
		io.LimitReader(reader, maxDeploymentKeyPinBytes+1),
	)
	closeErr := reader.Close()
	if readErr != nil {
		return deploymentKeyPinRecord{}, false, readErr
	}
	if closeErr != nil {
		return deploymentKeyPinRecord{}, false, closeErr
	}
	if len(body) == 0 ||
		len(body) > maxDeploymentKeyPinBytes ||
		int64(len(body)) != object.Size {
		return deploymentKeyPinRecord{}, false, fmt.Errorf(
			"repository pin size is invalid",
		)
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != object.SHA256 {
		return deploymentKeyPinRecord{}, false, fmt.Errorf(
			"repository pin checksum mismatch",
		)
	}
	var record deploymentKeyPinRecord
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return deploymentKeyPinRecord{}, false, fmt.Errorf(
			"backup deployment keys: decode repository pin: %w", err,
		)
	}
	if err := requireDeploymentJSONEOF(decoder); err != nil {
		return deploymentKeyPinRecord{}, false, err
	}
	return record, true, nil
}

// validatePin authenticates the exact expected package, repository, revision,
// and chain edge with a key retained by the local package.
func (a *RepositoryPinnedAuthority) validatePin(
	ctx context.Context,
	record deploymentKeyPinRecord,
	kind string,
	revision uint64,
	previous uint64,
) error {
	record.Schema = strings.TrimSpace(record.Schema)
	record.Kind = strings.TrimSpace(record.Kind)
	record.PackageID = strings.TrimSpace(record.PackageID)
	record.RepositoryID = strings.TrimSpace(record.RepositoryID)
	if record.Schema != deploymentKeyPinSchema ||
		record.Kind != kind ||
		record.PackageID != a.packageID ||
		record.RepositoryID != a.repositoryID ||
		record.Revision != revision ||
		record.PreviousActiveRevision != previous {
		return fmt.Errorf("repository pin metadata mismatch")
	}
	canonical, err := canonicalDeploymentKeyPin(record)
	if err != nil {
		return err
	}
	return a.DeploymentKeyAuthority.Verify(
		ctx, record.Signature, canonical,
	)
}

// canonicalDeploymentKeyPin returns the signature-free bytes signed by every
// node for one deterministic pin record.
func canonicalDeploymentKeyPin(
	record deploymentKeyPinRecord,
) ([]byte, error) {
	record.Signature = backupartifact.ManifestSignature{}
	body, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf(
			"backup deployment keys: encode canonical repository pin: %w",
			err,
		)
	}
	return body, nil
}
