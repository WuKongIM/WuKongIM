package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
)

const ossChecksumMetadataKey = "wukongim-sha256"

const ossLeastPrivilegeProbeKey = "qualification/least-privilege/permission-probe"

// OSSClient is the narrow Alibaba Cloud OSS SDK surface required by backup.
type OSSClient interface {
	PutObject(context.Context, *oss.PutObjectRequest, ...func(*oss.Options)) (*oss.PutObjectResult, error)
	HeadObject(context.Context, *oss.HeadObjectRequest, ...func(*oss.Options)) (*oss.HeadObjectResult, error)
	GetObject(context.Context, *oss.GetObjectRequest, ...func(*oss.Options)) (*oss.GetObjectResult, error)
	GetBucketVersioning(context.Context, *oss.GetBucketVersioningRequest, ...func(*oss.Options)) (*oss.GetBucketVersioningResult, error)
	GetBucketObjectWormConfiguration(context.Context, *oss.GetBucketObjectWormConfigurationRequest, ...func(*oss.Options)) (*oss.GetBucketObjectWormConfigurationResult, error)
	ListObjectsV2(context.Context, *oss.ListObjectsV2Request, ...func(*oss.Options)) (*oss.ListObjectsV2Result, error)
	ListObjectVersions(context.Context, *oss.ListObjectVersionsRequest, ...func(*oss.Options)) (*oss.ListObjectVersionsResult, error)
	DeleteObject(context.Context, *oss.DeleteObjectRequest, ...func(*oss.Options)) (*oss.DeleteObjectResult, error)
	GetObjectRetention(context.Context, *oss.GetObjectRetentionRequest, ...func(*oss.Options)) (*oss.GetObjectRetentionResult, error)
}

// OSSRepositoryOptions configures one Alibaba Cloud OSS backup repository.
type OSSRepositoryOptions struct {
	// Name is the bounded operator-facing failure-domain name.
	Name string
	// Bucket is the versioned ObjectWorm-enabled destination bucket.
	Bucket string
	// Prefix is the dedicated object namespace for one repository identity.
	Prefix string
	// ObjectLockDays is the minimum default COMPLIANCE retention.
	ObjectLockDays int
	// Client is an Alibaba Cloud OSS SDK client.
	Client OSSClient
	// Now supplies UTC time for retention checks in tests.
	Now func() time.Time
}

// OSSRepairRepositoryOptions configures the separately credentialed repair adapter.
type OSSRepairRepositoryOptions struct {
	// Repository supplies ordinary reads and the exact namespace identity.
	Repository *OSSRepository
	// Client uses an explicit auditor repair role.
	Client OSSClient
}

// OSSRepository stores checksummed immutable versions in Alibaba Cloud OSS.
//
// OSS does not support conditional PutObject on versioned buckets. The adapter
// therefore rejects existing keys before upload and verifies the resulting
// current version after upload. Cluster backup's Controller Leader and
// partition single-writer fences prevent concurrent ordinary writes to one key.
type OSSRepository struct {
	name           string
	bucket         string
	prefix         string
	objectLockDays int
	client         OSSClient
	now            func() time.Time
}

// NewOSSRepository creates a repository around an injected OSS client.
func NewOSSRepository(options OSSRepositoryOptions) (*OSSRepository, error) {
	name := strings.TrimSpace(options.Name)
	bucket := strings.TrimSpace(options.Bucket)
	prefix := strings.Trim(strings.TrimSpace(options.Prefix), "/")
	if name == "" || bucket == "" || prefix == "" || options.Client == nil {
		return nil, fmt.Errorf("backup OSS repository: name, bucket, prefix, and client are required")
	}
	if !safeOSSRepositoryKey(prefix) {
		return nil, fmt.Errorf("backup OSS repository: unsafe prefix")
	}
	if options.ObjectLockDays < 7 || options.ObjectLockDays > 36500 {
		return nil, fmt.Errorf("backup OSS repository: object lock days must be between 7 and 36500")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &OSSRepository{
		name:           name,
		bucket:         bucket,
		prefix:         prefix,
		objectLockDays: options.ObjectLockDays,
		client:         options.Client,
		now:            now,
	}, nil
}

// Name returns the configured operator-facing repository name.
func (r *OSSRepository) Name() string {
	if r == nil {
		return ""
	}
	return r.name
}

// PutImmutable creates key once and verifies the current OSS version.
func (r *OSSRepository) PutImmutable(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	return r.putObject(ctx, key, size, checksum, body, true)
}

// OSSRepairRepository exposes overwrite-by-new-version only to the auditor.
type OSSRepairRepository struct {
	repository *OSSRepository
	client     OSSClient
}

// NewOSSRepairRepository creates a narrow repair adapter with separate credentials.
func NewOSSRepairRepository(options OSSRepairRepositoryOptions) (*OSSRepairRepository, error) {
	if options.Repository == nil || options.Repository.client == nil || options.Client == nil {
		return nil, fmt.Errorf("backup OSS repair repository: repository and repair client are required")
	}
	return &OSSRepairRepository{repository: options.Repository, client: options.Client}, nil
}

// QualifyOrdinaryRoleLeastPrivilege proves that an ordinary capture/restore
// role cannot delete repository objects.
func (r *OSSRepository) QualifyOrdinaryRoleLeastPrivilege(
	ctx context.Context,
) error {
	if r == nil {
		return fmt.Errorf("backup OSS repository: ordinary role is unavailable")
	}
	return qualifyOSSObjectDeleteDenied(
		ctx, r.client, r.bucket, r.prefix, "ordinary",
	)
}

// QualifyRepairRoleLeastPrivilege proves that the repair writer cannot delete
// repository objects.
func (r *OSSRepairRepository) QualifyRepairRoleLeastPrivilege(
	ctx context.Context,
) error {
	if r == nil || r.repository == nil {
		return fmt.Errorf("backup OSS repair repository: repair role is unavailable")
	}
	return qualifyOSSObjectDeleteDenied(
		ctx, r.client, r.repository.bucket, r.repository.prefix, "repair",
	)
}

// QualifyGarbageRoleLeastPrivilege proves that the garbage collector cannot
// read immutable backup object bodies.
func (r *OSSRepository) QualifyGarbageRoleLeastPrivilege(
	ctx context.Context,
) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("backup OSS repository: garbage role is unavailable")
	}
	fullKey := path.Join(r.prefix, ossLeastPrivilegeProbeKey)
	output, err := r.client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: ossString(r.bucket), Key: ossString(fullKey),
	})
	if output != nil && output.Body != nil {
		_ = output.Body.Close()
	}
	if ossErrorCode(err) == "AccessDenied" {
		return nil
	}
	return fmt.Errorf(
		"backup OSS repository: garbage role is over-privileged: object reads must be denied",
	)
}

func qualifyOSSObjectDeleteDenied(
	ctx context.Context,
	client OSSClient,
	bucket string,
	prefix string,
	role string,
) error {
	if client == nil {
		return fmt.Errorf("backup OSS repository: %s role is unavailable", role)
	}
	_, err := client.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: ossString(bucket),
		Key:    ossString(path.Join(prefix, ossLeastPrivilegeProbeKey)),
	})
	if ossErrorCode(err) == "AccessDenied" {
		return nil
	}
	return fmt.Errorf(
		"backup OSS repository: %s role is over-privileged: object deletion must be denied",
		role,
	)
}

// Name returns the underlying failure-domain identity.
func (r *OSSRepairRepository) Name() string {
	if r == nil || r.repository == nil {
		return ""
	}
	return r.repository.Name()
}

// PutImmutable delegates ordinary create-once writes.
func (r *OSSRepairRepository) PutImmutable(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	if r == nil || r.repository == nil {
		return fmt.Errorf("%w: OSS repair repository is invalid", backupartifact.ErrInvalidObject)
	}
	return r.repository.PutImmutable(ctx, key, size, checksum, body)
}

// Open delegates reads to the ordinary repository.
func (r *OSSRepairRepository) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	if r == nil || r.repository == nil {
		return nil, backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: OSS repair repository is invalid", backupartifact.ErrInvalidObject,
		)
	}
	return r.repository.Open(ctx, key)
}

// Stat delegates metadata reads to the ordinary repository.
func (r *OSSRepairRepository) Stat(
	ctx context.Context,
	key string,
) (backupartifact.RepositoryObject, error) {
	if r == nil || r.repository == nil {
		return backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: OSS repair repository is invalid", backupartifact.ErrInvalidObject,
		)
	}
	return r.repository.Stat(ctx, key)
}

// RepairImmutable publishes a new ObjectWorm-protected current version.
func (r *OSSRepairRepository) RepairImmutable(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	if r == nil || r.repository == nil || r.client == nil {
		return fmt.Errorf("%w: OSS repair repository is invalid", backupartifact.ErrInvalidObject)
	}
	repair := *r.repository
	repair.client = r.client
	return repair.putObject(ctx, key, size, checksum, body, false)
}

func (r *OSSRepository) putObject(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
	createOnly bool,
) error {
	if r == nil || r.client == nil || body == nil || size < 0 || !validFileChecksum(checksum) {
		return fmt.Errorf("%w: OSS repository object metadata is invalid", backupartifact.ErrInvalidObject)
	}
	fullKey, err := r.fullKey(key)
	if err != nil {
		return err
	}
	if createOnly {
		_, err = r.client.HeadObject(ctx, &oss.HeadObjectRequest{
			Bucket: ossString(r.bucket), Key: ossString(fullKey),
		})
		switch {
		case err == nil:
			return backupartifact.ErrObjectExists
		case !errors.Is(mapOSSError(err), backupartifact.ErrObjectNotFound):
			return mapOSSError(err)
		}
	}
	_, err = r.client.PutObject(ctx, &oss.PutObjectRequest{
		Bucket:        ossString(r.bucket),
		Key:           ossString(fullKey),
		Body:          body,
		ContentLength: ossInt64(size),
		Metadata:      map[string]string{ossChecksumMetadataKey: checksum},
	})
	if err != nil {
		return mapOSSError(err)
	}
	current, err := r.client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: ossString(r.bucket), Key: ossString(fullKey),
	})
	if err != nil {
		return fmt.Errorf("backup OSS repository: verify uploaded object: %w", mapOSSError(err))
	}
	object, err := ossRepositoryObject(key, current)
	if err != nil {
		return err
	}
	if object.Size != size || object.SHA256 != strings.ToLower(checksum) {
		return fmt.Errorf("%w: OSS uploaded current version mismatch", backupartifact.ErrObjectCorrupt)
	}
	return nil
}

// Open returns a streaming body whose complete read verifies SHA-256 and size.
func (r *OSSRepository) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	fullKey, err := r.fullKey(key)
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, err
	}
	head, err := r.client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: ossString(r.bucket), Key: ossString(fullKey),
	})
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, mapOSSError(err)
	}
	object, err := ossRepositoryObject(key, head)
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, err
	}
	output, err := r.client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket:    ossString(r.bucket),
		Key:       ossString(fullKey),
		VersionId: head.VersionId,
	})
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, mapOSSError(err)
	}
	if output == nil || output.Body == nil {
		return nil, backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: OSS object body is missing", backupartifact.ErrObjectCorrupt,
		)
	}
	if head.VersionId != nil &&
		(output.VersionId == nil || *output.VersionId != *head.VersionId) {
		_ = output.Body.Close()
		return nil, backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: OSS object version changed during open",
			backupartifact.ErrObjectCorrupt,
		)
	}
	return newOSSVerifyingReadCloser(output.Body, object), object, nil
}

// Stat returns trusted OSS object metadata without downloading the body.
func (r *OSSRepository) Stat(
	ctx context.Context,
	key string,
) (backupartifact.RepositoryObject, error) {
	fullKey, err := r.fullKey(key)
	if err != nil {
		return backupartifact.RepositoryObject{}, err
	}
	output, err := r.client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: ossString(r.bucket), Key: ossString(fullKey),
	})
	if err != nil {
		return backupartifact.RepositoryObject{}, mapOSSError(err)
	}
	return ossRepositoryObject(key, output)
}

// DeleteGarbageObject permanently removes every exact version of one key.
func (r *OSSRepository) DeleteGarbageObject(ctx context.Context, key string) error {
	fullKey, err := r.fullKey(key)
	if err != nil {
		return err
	}
	const maximumVersionsPerGarbageObject = 64
	versions := make([]string, 0, 2)
	var keyMarker, versionMarker *string
	for {
		output, err := r.client.ListObjectVersions(ctx, &oss.ListObjectVersionsRequest{
			Bucket: ossString(r.bucket), Prefix: ossString(fullKey),
			KeyMarker: keyMarker, VersionIdMarker: versionMarker,
		})
		if err != nil {
			return mapOSSError(err)
		}
		if output == nil {
			return fmt.Errorf("backup OSS repository: object-version listing is missing")
		}
		for _, version := range output.ObjectVersions {
			if ossValue(version.Key) == fullKey {
				versions = append(versions, ossValue(version.VersionId))
			}
		}
		for _, marker := range output.ObjectDeleteMarkers {
			if ossValue(marker.Key) == fullKey {
				versions = append(versions, ossValue(marker.VersionId))
			}
		}
		if len(versions) > maximumVersionsPerGarbageObject {
			return fmt.Errorf("backup OSS repository: object version count exceeds garbage-collection limit")
		}
		if !output.IsTruncated {
			break
		}
		if output.NextKeyMarker == nil || output.NextVersionIdMarker == nil ||
			(ossValue(output.NextKeyMarker) == ossValue(keyMarker) &&
				ossValue(output.NextVersionIdMarker) == ossValue(versionMarker)) {
			return fmt.Errorf("backup OSS repository: invalid object-version continuation markers")
		}
		keyMarker, versionMarker = output.NextKeyMarker, output.NextVersionIdMarker
	}
	for _, versionID := range versions {
		if versionID == "" {
			return fmt.Errorf("backup OSS repository: versioned object has no version id")
		}
		_, err = r.client.DeleteObject(ctx, &oss.DeleteObjectRequest{
			Bucket: ossString(r.bucket), Key: ossString(fullKey), VersionId: ossString(versionID),
		})
		if err != nil {
			return r.classifyGarbageDeleteError(ctx, fullKey, versionID, err)
		}
	}
	return nil
}

// QualifyGarbageAccess clears one stable node slot, then proves list,
// delete-marker creation, and exact-version deletion permissions without
// creating or deleting a data object. ObjectWorm does not retain delete markers.
func (r *OSSRepository) QualifyGarbageAccess(
	ctx context.Context,
	probeID string,
) error {
	if r == nil || r.client == nil || len(probeID) != 16 {
		return fmt.Errorf("backup OSS repository: invalid garbage-access probe")
	}
	for _, character := range probeID {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return fmt.Errorf("backup OSS repository: invalid garbage-access probe")
		}
	}
	fullKey, err := r.fullKey("_qualification/garbage-role/" + probeID)
	if err != nil {
		return err
	}
	staleMarkers, err := r.garbageProbeMarkers(ctx, fullKey)
	if err != nil {
		return err
	}
	for _, versionID := range staleMarkers {
		if err := r.deleteGarbageProbeMarker(
			ctx, fullKey, versionID,
		); err != nil {
			return err
		}
	}
	created, err := r.client.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: ossString(r.bucket), Key: ossString(fullKey),
	})
	if err != nil {
		return fmt.Errorf(
			"backup OSS repository %s: create garbage-role delete marker: %w",
			r.name, mapOSSError(err),
		)
	}
	versionID := ""
	if created != nil {
		versionID = ossValue(created.VersionId)
	}
	if created == nil || !created.DeleteMarker || versionID == "" {
		return fmt.Errorf(
			"backup OSS repository %s: garbage-role delete marker evidence is incomplete",
			r.name,
		)
	}
	markers, err := r.garbageProbeMarkers(ctx, fullKey)
	if err != nil {
		return err
	}
	found := false
	for _, markerVersionID := range markers {
		if markerVersionID == versionID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf(
			"backup OSS repository %s: garbage-role delete marker was not listed",
			r.name,
		)
	}
	return r.deleteGarbageProbeMarker(ctx, fullKey, versionID)
}

func (r *OSSRepository) garbageProbeMarkers(
	ctx context.Context,
	fullKey string,
) ([]string, error) {
	versions, err := r.client.ListObjectVersions(
		ctx,
		&oss.ListObjectVersionsRequest{
			Bucket: ossString(r.bucket), Prefix: ossString(fullKey),
			MaxKeys: 8,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"backup OSS repository %s: list garbage-role marker: %w",
			r.name, mapOSSError(err),
		)
	}
	if versions == nil || versions.IsTruncated {
		return nil, fmt.Errorf(
			"backup OSS repository %s: garbage-role marker listing is incomplete",
			r.name,
		)
	}
	for _, version := range versions.ObjectVersions {
		if ossValue(version.Key) == fullKey {
			return nil, fmt.Errorf(
				"backup OSS repository %s: garbage-role probe collides with a data object",
				r.name,
			)
		}
	}
	markers := make([]string, 0, len(versions.ObjectDeleteMarkers))
	for _, marker := range versions.ObjectDeleteMarkers {
		if ossValue(marker.Key) != fullKey ||
			ossValue(marker.VersionId) == "" {
			return nil, fmt.Errorf(
				"backup OSS repository %s: garbage-role marker metadata is invalid",
				r.name,
			)
		}
		markers = append(markers, ossValue(marker.VersionId))
	}
	return markers, nil
}

func (r *OSSRepository) deleteGarbageProbeMarker(
	ctx context.Context,
	fullKey string,
	versionID string,
) error {
	_, err := r.client.DeleteObject(
		ctx,
		&oss.DeleteObjectRequest{
			Bucket: ossString(r.bucket), Key: ossString(fullKey),
			VersionId: ossString(versionID),
		},
	)
	if err != nil {
		return fmt.Errorf(
			"backup OSS repository %s: delete garbage-role marker: %w",
			r.name, mapOSSError(err),
		)
	}
	return nil
}

// DeleteGenerationGarbageObject removes bounded exact versions.
func (r *OSSRepository) DeleteGenerationGarbageObject(
	ctx context.Context,
	key string,
	maxRequests int,
) (int, error) {
	if maxRequests < 1 {
		return 0, errGenerationGCRequestBudget
	}
	fullKey, err := r.fullKey(key)
	if err != nil {
		return 0, err
	}
	const maxGenerationRepairVersions = 64
	output, err := r.client.ListObjectVersions(ctx, &oss.ListObjectVersionsRequest{
		Bucket: ossString(r.bucket), Prefix: ossString(fullKey),
		MaxKeys: maxGenerationRepairVersions + 1,
	})
	if err != nil {
		return 1, mapOSSError(err)
	}
	if output == nil {
		return 1, fmt.Errorf("backup OSS repository: object-version listing is missing")
	}
	versions := make([]string, 0, 2)
	for _, version := range output.ObjectVersions {
		if ossValue(version.Key) == fullKey {
			versions = append(versions, ossValue(version.VersionId))
		}
	}
	for _, marker := range output.ObjectDeleteMarkers {
		if ossValue(marker.Key) == fullKey {
			versions = append(versions, ossValue(marker.VersionId))
		}
	}
	if output.IsTruncated || len(versions) > maxGenerationRepairVersions {
		return 1, fmt.Errorf(
			"%w: generation object repair-version count exceeds limit",
			backupartifact.ErrObjectCorrupt,
		)
	}
	if len(versions) == 0 {
		return 1, nil
	}
	for _, versionID := range versions {
		if versionID == "" {
			return 1, fmt.Errorf(
				"%w: generation object version id is empty",
				backupartifact.ErrObjectCorrupt,
			)
		}
	}
	used := 1
	for _, versionID := range versions {
		if used >= maxRequests {
			return used, errGenerationGCRequestBudget
		}
		_, err = r.client.DeleteObject(ctx, &oss.DeleteObjectRequest{
			Bucket: ossString(r.bucket), Key: ossString(fullKey),
			VersionId: ossString(versionID),
		})
		used++
		if err == nil {
			continue
		}
		if ossErrorCode(err) == "FileImmutable" {
			return used, backupartifact.ErrObjectLocked
		}
		if !ossGarbageDeleteMayBeLocked(err) {
			return used, mapOSSError(err)
		}
		if used >= maxRequests {
			return used, errGenerationGCRequestBudget
		}
		locked := r.objectVersionLocked(ctx, fullKey, versionID)
		used++
		if locked {
			return used, backupartifact.ErrObjectLocked
		}
		return used, mapOSSError(err)
	}
	return used, nil
}

// WalkGarbageObjects streams keys older than before.
func (r *OSSRepository) WalkGarbageObjects(
	ctx context.Context,
	before time.Time,
	visit func(backupartifact.RepositoryObject) (bool, error),
) error {
	if r == nil || r.client == nil || visit == nil || before.IsZero() {
		return fmt.Errorf("backup OSS repository: garbage walk options are invalid")
	}
	prefix := r.prefix + "/"
	var continuationToken *string
	for {
		output, err := r.client.ListObjectsV2(ctx, &oss.ListObjectsV2Request{
			Bucket: ossString(r.bucket), Prefix: ossString(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return mapOSSError(err)
		}
		if output == nil {
			return fmt.Errorf("backup OSS repository: object listing is missing")
		}
		for _, object := range output.Contents {
			if object.Key == nil || object.LastModified == nil {
				return fmt.Errorf("backup OSS repository: garbage object metadata is incomplete")
			}
			if !object.LastModified.UTC().Before(before.UTC()) {
				continue
			}
			fullKey := ossValue(object.Key)
			relative := strings.TrimPrefix(fullKey, prefix)
			if relative == fullKey || !safeRepositoryKey(relative) {
				return fmt.Errorf("backup OSS repository: listed garbage key escapes repository prefix")
			}
			keepWalking, err := visit(backupartifact.RepositoryObject{
				Key: relative, Size: object.Size,
			})
			if err != nil {
				return err
			}
			if !keepWalking {
				return nil
			}
		}
		if !output.IsTruncated {
			return nil
		}
		if output.NextContinuationToken == nil || ossValue(output.NextContinuationToken) == "" ||
			(continuationToken != nil &&
				ossValue(output.NextContinuationToken) == ossValue(continuationToken)) {
			return fmt.Errorf("backup OSS repository: invalid garbage-list continuation token")
		}
		continuationToken = output.NextContinuationToken
	}
}

// ListGarbageObjects returns one lexicographically bounded provider page.
func (r *OSSRepository) ListGarbageObjects(
	ctx context.Context,
	before time.Time,
	afterKey string,
	limit int,
) (GarbageObjectPage, error) {
	if r == nil || r.client == nil || before.IsZero() || limit <= 0 || limit > 4096 ||
		(afterKey != "" && !safeRepositoryKey(afterKey)) {
		return GarbageObjectPage{}, fmt.Errorf("backup OSS repository: garbage page options are invalid")
	}
	prefix := r.prefix + "/"
	input := &oss.ListObjectsV2Request{
		Bucket: ossString(r.bucket), Prefix: ossString(prefix), MaxKeys: int32(limit),
	}
	if afterKey != "" {
		input.StartAfter = ossString(prefix + afterKey)
	}
	output, err := r.client.ListObjectsV2(ctx, input)
	if err != nil {
		return GarbageObjectPage{}, mapOSSError(err)
	}
	if output == nil {
		return GarbageObjectPage{}, fmt.Errorf("backup OSS repository: object listing is missing")
	}
	page := GarbageObjectPage{
		Objects:  make([]backupartifact.RepositoryObject, 0, len(output.Contents)),
		AfterKey: afterKey, Complete: !output.IsTruncated,
	}
	previousFullKey := prefix + afterKey
	for _, object := range output.Contents {
		if object.Key == nil || object.LastModified == nil || object.Size < 0 {
			return GarbageObjectPage{}, fmt.Errorf("backup OSS repository: garbage object metadata is incomplete")
		}
		fullKey := ossValue(object.Key)
		relative := strings.TrimPrefix(fullKey, prefix)
		if relative == fullKey || !safeRepositoryKey(relative) || fullKey <= previousFullKey {
			return GarbageObjectPage{}, fmt.Errorf("backup OSS repository: garbage page is not strictly ordered")
		}
		previousFullKey = fullKey
		page.AfterKey = relative
		if object.LastModified.UTC().Before(before.UTC()) {
			page.Objects = append(page.Objects, backupartifact.RepositoryObject{
				Key: relative, Size: object.Size,
			})
		}
	}
	if !page.Complete && len(output.Contents) == 0 {
		return GarbageObjectPage{}, fmt.Errorf("backup OSS repository: truncated garbage page made no progress")
	}
	return page, nil
}

// ListErasureLedgerCommitKeys returns bounded sorted commit-marker keys.
func (r *OSSRepository) ListErasureLedgerCommitKeys(
	ctx context.Context,
	namespace string,
) ([]string, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("backup OSS repository: repository is required")
	}
	if _, _, _, err := backupartifact.ParseErasureLedgerCommitKey(
		backupartifact.ErasureLedgerCommitKey(namespace, 0, 1),
	); err != nil {
		return nil, fmt.Errorf("backup OSS repository: erasure-ledger namespace is invalid")
	}
	prefix := r.prefix + "/erasure-ledger/streams/" + namespace + "/"
	keys := make([]string, 0)
	var continuationToken *string
	for {
		output, err := r.client.ListObjectsV2(ctx, &oss.ListObjectsV2Request{
			Bucket: ossString(r.bucket), Prefix: ossString(prefix),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, mapOSSError(err)
		}
		if output == nil {
			return nil, fmt.Errorf("backup OSS repository: erasure-ledger listing is missing")
		}
		for _, object := range output.Contents {
			fullKey := ossValue(object.Key)
			relative := strings.TrimPrefix(fullKey, r.prefix+"/")
			if relative == fullKey || !validErasureLedgerCommitKey(relative, namespace) {
				return nil, fmt.Errorf(
					"%w: invalid listed erasure-ledger commit key",
					backupartifact.ErrObjectCorrupt,
				)
			}
			keys = append(keys, relative)
			if len(keys) > backupartifact.MaxErasureLedgerEvents {
				return nil, fmt.Errorf("backup OSS repository: erasure-ledger commit listing exceeds limit")
			}
		}
		if !output.IsTruncated {
			break
		}
		if output.NextContinuationToken == nil ||
			ossValue(output.NextContinuationToken) == "" ||
			(continuationToken != nil &&
				ossValue(output.NextContinuationToken) == ossValue(continuationToken)) {
			return nil, fmt.Errorf("backup OSS repository: invalid erasure-ledger listing continuation token")
		}
		continuationToken = output.NextContinuationToken
	}
	sort.Strings(keys)
	return keys, nil
}

// Check verifies versioning and default COMPLIANCE ObjectWorm retention.
func (r *OSSRepository) Check(ctx context.Context) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("backup OSS repository: client is required")
	}
	versioning, err := r.client.GetBucketVersioning(ctx, &oss.GetBucketVersioningRequest{
		Bucket: ossString(r.bucket),
	})
	if err != nil {
		return fmt.Errorf(
			"backup OSS repository %s: get versioning: %w",
			r.name, mapOSSError(err),
		)
	}
	if versioning == nil || ossValue(versioning.VersionStatus) != "Enabled" {
		return fmt.Errorf("backup OSS repository %s: bucket versioning must be enabled", r.name)
	}
	worm, err := r.client.GetBucketObjectWormConfiguration(
		ctx,
		&oss.GetBucketObjectWormConfigurationRequest{Bucket: ossString(r.bucket)},
	)
	if err != nil {
		return fmt.Errorf(
			"backup OSS repository %s: get ObjectWorm: %w",
			r.name, mapOSSError(err),
		)
	}
	if !validOSSObjectWorm(worm, r.objectLockDays) {
		return fmt.Errorf(
			"backup OSS repository %s: ObjectWorm must default to COMPLIANCE for at least %d days",
			r.name, r.objectLockDays,
		)
	}
	return nil
}

func validOSSObjectWorm(result *oss.GetBucketObjectWormConfigurationResult, minimumDays int) bool {
	if result == nil || result.ObjectWormConfiguration == nil ||
		ossValue(result.ObjectWormConfiguration.ObjectWormEnabled) != "Enabled" ||
		result.ObjectWormConfiguration.Rule == nil ||
		result.ObjectWormConfiguration.Rule.DefaultRetention == nil {
		return false
	}
	retention := result.ObjectWormConfiguration.Rule.DefaultRetention
	if ossValue(retention.Mode) != "COMPLIANCE" {
		return false
	}
	var days int64
	if retention.Days != nil {
		days = int64(*retention.Days)
	} else if retention.Years != nil {
		days = int64(*retention.Years) * 365
	}
	return days >= int64(minimumDays)
}

func (r *OSSRepository) fullKey(key string) (string, error) {
	if r == nil || !safeOSSRepositoryKey(key) {
		return "", fmt.Errorf("%w: unsafe OSS repository key", backupartifact.ErrInvalidObject)
	}
	return r.prefix + "/" + key, nil
}

func safeOSSRepositoryKey(key string) bool {
	return key != "" && !strings.Contains(key, "\\") && !strings.HasPrefix(key, "/") &&
		path.Clean(key) == key && key != "." && key != ".." && !strings.HasPrefix(key, "../")
}

func ossRepositoryObject(
	key string,
	output *oss.HeadObjectResult,
) (backupartifact.RepositoryObject, error) {
	if output == nil {
		return backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: OSS object metadata is missing", backupartifact.ErrObjectCorrupt,
		)
	}
	return ossRepositoryObjectFromValues(key, output.ContentLength, output.Metadata)
}

func ossRepositoryObjectFromValues(
	key string,
	size int64,
	metadata map[string]string,
) (backupartifact.RepositoryObject, error) {
	if size < 0 {
		return backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: OSS object size is missing", backupartifact.ErrObjectCorrupt,
		)
	}
	checksum := strings.ToLower(strings.TrimSpace(metadata[ossChecksumMetadataKey]))
	if !validFileChecksum(checksum) {
		return backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: OSS object checksum metadata is invalid", backupartifact.ErrObjectCorrupt,
		)
	}
	return backupartifact.RepositoryObject{Key: key, Size: size, SHA256: checksum}, nil
}

type ossVerifyingReadCloser struct {
	body     io.ReadCloser
	expected backupartifact.RepositoryObject
	hash     hashWriter
	read     int64
	checked  bool
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func newOSSVerifyingReadCloser(
	body io.ReadCloser,
	expected backupartifact.RepositoryObject,
) io.ReadCloser {
	return &ossVerifyingReadCloser{
		body: body, expected: expected, hash: sha256.New(),
	}
}

func (r *ossVerifyingReadCloser) Read(buffer []byte) (int, error) {
	n, err := r.body.Read(buffer)
	if n > 0 {
		_, _ = r.hash.Write(buffer[:n])
		r.read += int64(n)
	}
	if err == io.EOF && !r.checked {
		r.checked = true
		actual := hex.EncodeToString(r.hash.Sum(nil))
		if r.read != r.expected.Size || actual != r.expected.SHA256 {
			return n, fmt.Errorf(
				"%w: OSS object body checksum mismatch: size=%d/%d sha256=%s/%s",
				backupartifact.ErrObjectCorrupt,
				r.read, r.expected.Size, actual, r.expected.SHA256,
			)
		}
	}
	return n, err
}

func (r *ossVerifyingReadCloser) Close() error {
	return r.body.Close()
}

func mapOSSError(err error) error {
	if err == nil {
		return nil
	}
	switch ossErrorCode(err) {
	case "NoSuchKey", "NoSuchBucket", "NoSuchVersion", "NotFound":
		return backupartifact.ErrObjectNotFound
	case "FileAlreadyExists", "ObjectAlreadyExists":
		return backupartifact.ErrObjectExists
	case "FileImmutable":
		return backupartifact.ErrObjectLocked
	}
	return err
}

func ossErrorCode(err error) string {
	var serviceError *oss.ServiceError
	if errors.As(err, &serviceError) {
		return serviceError.Code
	}
	return ""
}

func ossGarbageDeleteMayBeLocked(err error) bool {
	switch ossErrorCode(err) {
	case "AccessDenied", "InvalidRequest":
		return true
	default:
		return false
	}
}

func (r *OSSRepository) classifyGarbageDeleteError(
	ctx context.Context,
	fullKey string,
	versionID string,
	err error,
) error {
	if ossErrorCode(err) == "FileImmutable" {
		return backupartifact.ErrObjectLocked
	}
	if ossGarbageDeleteMayBeLocked(err) &&
		r.objectVersionLocked(ctx, fullKey, versionID) {
		return backupartifact.ErrObjectLocked
	}
	return mapOSSError(err)
}

func (r *OSSRepository) objectVersionLocked(
	ctx context.Context,
	fullKey string,
	versionID string,
) bool {
	output, err := r.client.GetObjectRetention(ctx, &oss.GetObjectRetentionRequest{
		Bucket: ossString(r.bucket), Key: ossString(fullKey), VersionId: ossString(versionID),
	})
	if err != nil || output == nil || output.Retention == nil ||
		ossValue(output.Retention.Mode) != "COMPLIANCE" {
		return false
	}
	retainUntil, err := time.Parse(time.RFC3339, ossValue(output.Retention.RetainUntilDate))
	return err == nil && retainUntil.After(r.now().UTC())
}

func ossString(value string) *string {
	return &value
}

func ossInt64(value int64) *int64 {
	return &value
}

func ossValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var _ backupartifact.Repository = (*OSSRepository)(nil)
var _ backupartifact.RepairRepository = (*OSSRepairRepository)(nil)
var _ GenerationGarbageRepository = (*OSSRepository)(nil)
var _ RepositoryDoctor = (*OSSRepository)(nil)
