package backup

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

const s3ChecksumMetadataKey = "wukongim-sha256"

// S3Client is the narrow AWS SDK v2 surface required by the immutable repository.
type S3Client interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
	GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error)
	GetObjectLockConfiguration(context.Context, *s3.GetObjectLockConfigurationInput, ...func(*s3.Options)) (*s3.GetObjectLockConfigurationOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	ListObjectVersions(context.Context, *s3.ListObjectVersionsInput, ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// S3RepositoryOptions configures one S3-compatible repository adapter.
type S3RepositoryOptions struct {
	// Name is the bounded operator-facing failure-domain name.
	Name string
	// Bucket is the versioned, Object-Lock-enabled destination bucket.
	Bucket string
	// Prefix is the dedicated object namespace for one repository identity.
	Prefix string
	// ObjectLockDays is the compliance-mode retention applied to every write.
	ObjectLockDays int
	// Client is an AWS SDK v2 compatible S3 client.
	Client S3Client
	// Now supplies UTC time for deterministic retention dates in tests.
	Now func() time.Time
}

// S3RepairRepositoryOptions configures the separately credentialed repair adapter.
type S3RepairRepositoryOptions struct {
	// Repository supplies immutable reads and the exact namespace identity.
	Repository *S3Repository
	// Client uses an explicit auditor repair role, never ordinary upload credentials.
	Client S3Client
}

// S3Repository stores immutable checksummed objects in one S3-compatible bucket.
type S3Repository struct {
	name           string
	bucket         string
	prefix         string
	objectLockDays int
	client         S3Client
	now            func() time.Time
}

// NewS3Repository creates a repository around an injected S3 client.
func NewS3Repository(options S3RepositoryOptions) (*S3Repository, error) {
	name := strings.TrimSpace(options.Name)
	bucket := strings.TrimSpace(options.Bucket)
	prefix := strings.Trim(strings.TrimSpace(options.Prefix), "/")
	if name == "" || bucket == "" || prefix == "" || options.Client == nil {
		return nil, fmt.Errorf("backup s3 repository: name, bucket, prefix, and client are required")
	}
	if !safeRepositoryKey(prefix) {
		return nil, fmt.Errorf("backup s3 repository: unsafe prefix")
	}
	if options.ObjectLockDays < 7 || options.ObjectLockDays > 36500 {
		return nil, fmt.Errorf("backup s3 repository: object lock days must be between 7 and 36500")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &S3Repository{
		name:           name,
		bucket:         bucket,
		prefix:         prefix,
		objectLockDays: options.ObjectLockDays,
		client:         options.Client,
		now:            now,
	}, nil
}

// LoadS3Repository loads the AWS SDK default credential chain and creates an
// S3-compatible client for the explicit HTTPS endpoint and region.
func LoadS3Repository(ctx context.Context, name, endpoint, region, bucket, prefix string, objectLockDays int) (*S3Repository, error) {
	return loadS3Repository(ctx, name, endpoint, region, bucket, prefix, objectLockDays, "")
}

// LoadS3GarbageRepository creates a delete-capable repository client by
// assuming an explicit role separate from the upload credential identity.
func LoadS3GarbageRepository(ctx context.Context, name, endpoint, region, bucket, prefix string, objectLockDays int, roleARN string) (*S3Repository, error) {
	roleARN = strings.TrimSpace(roleARN)
	if roleARN == "" {
		return nil, fmt.Errorf("backup s3 repository: garbage collector role ARN is required")
	}
	return loadS3Repository(ctx, name, endpoint, region, bucket, prefix, objectLockDays, roleARN)
}

// LoadS3RepairRepository assumes an explicit auditor role and binds it to an
// already configured ordinary repository. The default upload identity is never reused.
func LoadS3RepairRepository(
	ctx context.Context,
	repository *S3Repository,
	endpoint string,
	region string,
	roleARN string,
) (*S3RepairRepository, error) {
	endpoint = strings.TrimSpace(endpoint)
	region = strings.TrimSpace(region)
	roleARN = strings.TrimSpace(roleARN)
	if repository == nil || endpoint == "" || region == "" || roleARN == "" {
		return nil, fmt.Errorf(
			"backup s3 repair repository: repository, endpoint, region, and role ARN are required",
		)
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("backup s3 repair repository: load AWS credentials: %w", err)
	}
	provider := stscreds.NewAssumeRoleProvider(
		sts.NewFromConfig(cfg), roleARN,
		func(options *stscreds.AssumeRoleOptions) {
			options.RoleSessionName = "wukongim-backup-integrity-auditor"
		},
	)
	cfg.Credentials = aws.NewCredentialsCache(provider)
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	return NewS3RepairRepository(S3RepairRepositoryOptions{
		Repository: repository,
		Client:     client,
	})
}

func loadS3Repository(ctx context.Context, name, endpoint, region, bucket, prefix string, objectLockDays int, roleARN string) (*S3Repository, error) {
	endpoint = strings.TrimSpace(endpoint)
	region = strings.TrimSpace(region)
	if endpoint == "" || region == "" {
		return nil, fmt.Errorf("backup s3 repository: endpoint and region are required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("backup s3 repository: load AWS credentials: %w", err)
	}
	if roleARN != "" {
		provider := stscreds.NewAssumeRoleProvider(sts.NewFromConfig(cfg), roleARN, func(options *stscreds.AssumeRoleOptions) {
			options.RoleSessionName = "wukongim-backup-garbage-collector"
		})
		cfg.Credentials = aws.NewCredentialsCache(provider)
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	return NewS3Repository(S3RepositoryOptions{
		Name:           name,
		Bucket:         bucket,
		Prefix:         prefix,
		ObjectLockDays: objectLockDays,
		Client:         client,
	})
}

// Name returns the configured operator-facing repository name.
func (r *S3Repository) Name() string {
	if r == nil {
		return ""
	}
	return r.name
}

// PutImmutable uploads key with a create-only precondition, SHA-256 checksum,
// and compliance-mode Object Lock retention.
func (r *S3Repository) PutImmutable(ctx context.Context, key string, size int64, checksum string, body io.Reader) error {
	return r.putObject(ctx, key, size, checksum, body, true)
}

// S3RepairRepository exposes overwrite-by-new-version only to the auditor.
type S3RepairRepository struct {
	repository *S3Repository
	client     S3Client
}

// NewS3RepairRepository creates a narrow repair adapter with separate credentials.
func NewS3RepairRepository(options S3RepairRepositoryOptions) (*S3RepairRepository, error) {
	if options.Repository == nil || options.Repository.client == nil || options.Client == nil {
		return nil, fmt.Errorf("backup s3 repair repository: repository and repair client are required")
	}
	return &S3RepairRepository{
		repository: options.Repository,
		client:     options.Client,
	}, nil
}

// Name returns the underlying failure-domain identity.
func (r *S3RepairRepository) Name() string {
	if r == nil || r.repository == nil {
		return ""
	}
	return r.repository.Name()
}

// PutImmutable delegates create-only writes to the ordinary repository boundary.
func (r *S3RepairRepository) PutImmutable(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	if r == nil || r.repository == nil {
		return fmt.Errorf("%w: S3 repair repository is invalid", backupartifact.ErrInvalidObject)
	}
	return r.repository.PutImmutable(ctx, key, size, checksum, body)
}

// Open delegates reads to the ordinary repository boundary.
func (r *S3RepairRepository) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	if r == nil || r.repository == nil {
		return nil, backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: S3 repair repository is invalid", backupartifact.ErrInvalidObject,
		)
	}
	return r.repository.Open(ctx, key)
}

// Stat delegates metadata reads to the ordinary repository boundary.
func (r *S3RepairRepository) Stat(
	ctx context.Context,
	key string,
) (backupartifact.RepositoryObject, error) {
	if r == nil || r.repository == nil {
		return backupartifact.RepositoryObject{}, fmt.Errorf(
			"%w: S3 repair repository is invalid", backupartifact.ErrInvalidObject,
		)
	}
	return r.repository.Stat(ctx, key)
}

// RepairImmutable publishes a new Object-Locked current version from an
// authenticated healthy peer using only the explicit repair client.
func (r *S3RepairRepository) RepairImmutable(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
) error {
	if r == nil || r.repository == nil || r.client == nil {
		return fmt.Errorf("%w: S3 repair repository is invalid", backupartifact.ErrInvalidObject)
	}
	repair := *r.repository
	repair.client = r.client
	return repair.putObject(ctx, key, size, checksum, body, false)
}

func (r *S3Repository) putObject(
	ctx context.Context,
	key string,
	size int64,
	checksum string,
	body io.Reader,
	createOnly bool,
) error {
	if r == nil || r.client == nil || body == nil || size < 0 || !validFileChecksum(checksum) {
		return fmt.Errorf("%w: S3 repository object metadata is invalid", backupartifact.ErrInvalidObject)
	}
	fullKey, err := r.fullKey(key)
	if err != nil {
		return err
	}
	checksumBytes, _ := hex.DecodeString(checksum)
	checksumBase64 := base64.StdEncoding.EncodeToString(checksumBytes)
	retainUntil := r.now().UTC().Add(time.Duration(r.objectLockDays) * 24 * time.Hour)
	input := &s3.PutObjectInput{
		Bucket:                    aws.String(r.bucket),
		Key:                       aws.String(fullKey),
		Body:                      body,
		ContentLength:             aws.Int64(size),
		ChecksumAlgorithm:         types.ChecksumAlgorithmSha256,
		ChecksumSHA256:            aws.String(checksumBase64),
		Metadata:                  map[string]string{s3ChecksumMetadataKey: checksum},
		ObjectLockMode:            types.ObjectLockModeCompliance,
		ObjectLockRetainUntilDate: &retainUntil,
	}
	if createOnly {
		input.IfNoneMatch = aws.String("*")
	}
	_, err = r.client.PutObject(ctx, input)
	if err != nil {
		return mapS3Error(err)
	}
	return nil
}

// Open returns a streaming object body only when immutable provider metadata
// matches the stored WuKongIM SHA-256 checksum.
func (r *S3Repository) Open(ctx context.Context, key string) (io.ReadCloser, backupartifact.RepositoryObject, error) {
	fullKey, err := r.fullKey(key)
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, err
	}
	output, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket:       aws.String(r.bucket),
		Key:          aws.String(fullKey),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return nil, backupartifact.RepositoryObject{}, mapS3Error(err)
	}
	if output == nil || output.Body == nil {
		return nil, backupartifact.RepositoryObject{}, fmt.Errorf("%w: S3 object body is missing", backupartifact.ErrObjectCorrupt)
	}
	object, err := s3RepositoryObject(key, output.ContentLength, output.Metadata, output.ChecksumSHA256)
	if err != nil {
		_ = output.Body.Close()
		return nil, backupartifact.RepositoryObject{}, err
	}
	return output.Body, object, nil
}

// Stat returns trusted immutable object metadata without downloading the body.
func (r *S3Repository) Stat(ctx context.Context, key string) (backupartifact.RepositoryObject, error) {
	fullKey, err := r.fullKey(key)
	if err != nil {
		return backupartifact.RepositoryObject{}, err
	}
	output, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket:       aws.String(r.bucket),
		Key:          aws.String(fullKey),
		ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return backupartifact.RepositoryObject{}, mapS3Error(err)
	}
	if output == nil {
		return backupartifact.RepositoryObject{}, fmt.Errorf("%w: S3 object metadata is missing", backupartifact.ErrObjectCorrupt)
	}
	return s3RepositoryObject(key, output.ContentLength, output.Metadata, output.ChecksumSHA256)
}

// DeleteGarbageObject permanently removes every exact version of one
// unreachable immutable key. Compliance Object Lock remains authoritative and
// causes the operation to fail until retention expires.
func (r *S3Repository) DeleteGarbageObject(ctx context.Context, key string) error {
	fullKey, err := r.fullKey(key)
	if err != nil {
		return err
	}
	const maximumVersionsPerGarbageObject = 64
	versions := make([]string, 0, 2)
	var keyMarker, versionMarker *string
	for {
		output, err := r.client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
			Bucket: aws.String(r.bucket), Prefix: aws.String(fullKey), KeyMarker: keyMarker, VersionIdMarker: versionMarker,
		})
		if err != nil {
			return mapS3Error(err)
		}
		if output == nil {
			return fmt.Errorf("backup s3 repository: object-version listing is missing")
		}
		for _, objectVersion := range output.Versions {
			if aws.ToString(objectVersion.Key) == fullKey {
				versions = append(versions, aws.ToString(objectVersion.VersionId))
			}
		}
		for _, marker := range output.DeleteMarkers {
			if aws.ToString(marker.Key) == fullKey {
				versions = append(versions, aws.ToString(marker.VersionId))
			}
		}
		if len(versions) > maximumVersionsPerGarbageObject {
			return fmt.Errorf("backup s3 repository: object version count exceeds garbage-collection limit")
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		if output.NextKeyMarker == nil || output.NextVersionIdMarker == nil ||
			(aws.ToString(output.NextKeyMarker) == aws.ToString(keyMarker) && aws.ToString(output.NextVersionIdMarker) == aws.ToString(versionMarker)) {
			return fmt.Errorf("backup s3 repository: invalid object-version continuation markers")
		}
		keyMarker, versionMarker = output.NextKeyMarker, output.NextVersionIdMarker
	}
	for _, versionID := range versions {
		if versionID == "" {
			return fmt.Errorf("backup s3 repository: versioned object has no version id")
		}
		if _, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(r.bucket), Key: aws.String(fullKey), VersionId: aws.String(versionID),
		}); err != nil {
			return r.classifyGarbageDeleteError(ctx, fullKey, versionID, err)
		}
	}
	return nil
}

// DeleteGenerationGarbageObject removes every bounded exact version with a
// provider-request ceiling. A repaired current copy may have an older
// Object-Locked version, so retries list again and continue from remaining
// versions without advancing the Generation GC cursor prematurely.
func (r *S3Repository) DeleteGenerationGarbageObject(
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
	output, err := r.client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{
		Bucket: aws.String(r.bucket), Prefix: aws.String(fullKey),
		MaxKeys: aws.Int32(maxGenerationRepairVersions + 1),
	})
	if err != nil {
		return 1, mapS3Error(err)
	}
	if output == nil {
		return 1, fmt.Errorf("backup s3 repository: object-version listing is missing")
	}
	versions := make([]string, 0, 2)
	for _, version := range output.Versions {
		if aws.ToString(version.Key) == fullKey {
			versions = append(versions, aws.ToString(version.VersionId))
		}
	}
	for _, marker := range output.DeleteMarkers {
		if aws.ToString(marker.Key) == fullKey {
			versions = append(versions, aws.ToString(marker.VersionId))
		}
	}
	if aws.ToBool(output.IsTruncated) || len(versions) > maxGenerationRepairVersions {
		return 1, fmt.Errorf("%w: generation object repair-version count exceeds limit", backupartifact.ErrObjectCorrupt)
	}
	if len(versions) == 0 {
		return 1, nil
	}
	for _, versionID := range versions {
		if versionID == "" {
			return 1, fmt.Errorf("%w: generation object version id is empty", backupartifact.ErrObjectCorrupt)
		}
	}
	used := 1
	for _, versionID := range versions {
		if used >= maxRequests {
			return used, errGenerationGCRequestBudget
		}
		_, err = r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(r.bucket), Key: aws.String(fullKey),
			VersionId: aws.String(versionID),
		})
		used++
		if err == nil {
			continue
		}
		if !s3GarbageDeleteMayBeLocked(err) {
			return used, mapS3Error(err)
		}
		if used >= maxRequests {
			// The caller must reserve one request to distinguish Object Lock
			// from an IAM denial; never guess from a provider AccessDenied.
			return used, errGenerationGCRequestBudget
		}
		locked := r.objectVersionLocked(ctx, fullKey, versionID)
		used++
		if locked {
			return used, backupartifact.ErrObjectLocked
		}
		return used, mapS3Error(err)
	}
	return used, nil
}

// WalkGarbageObjects streams immutable keys older than before without loading
// object bodies. Returning false from visit stops the walk cleanly.
func (r *S3Repository) WalkGarbageObjects(ctx context.Context, before time.Time, visit func(backupartifact.RepositoryObject) (bool, error)) error {
	if r == nil || r.client == nil || visit == nil || before.IsZero() {
		return fmt.Errorf("backup s3 repository: garbage walk options are invalid")
	}
	prefix := r.prefix + "/"
	var continuationToken *string
	for {
		output, err := r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(r.bucket), Prefix: aws.String(prefix), ContinuationToken: continuationToken,
		})
		if err != nil {
			return mapS3Error(err)
		}
		if output == nil {
			return fmt.Errorf("backup s3 repository: object listing is missing")
		}
		for _, object := range output.Contents {
			if object.Key == nil || object.LastModified == nil {
				return fmt.Errorf("backup s3 repository: garbage object metadata is incomplete")
			}
			if !object.LastModified.UTC().Before(before.UTC()) {
				continue
			}
			fullKey := aws.ToString(object.Key)
			relative := strings.TrimPrefix(fullKey, prefix)
			if relative == fullKey || !safeRepositoryKey(relative) {
				return fmt.Errorf("backup s3 repository: listed garbage key escapes repository prefix")
			}
			keepWalking, err := visit(backupartifact.RepositoryObject{Key: relative, Size: aws.ToInt64(object.Size)})
			if err != nil {
				return err
			}
			if !keepWalking {
				return nil
			}
		}
		if !aws.ToBool(output.IsTruncated) {
			return nil
		}
		if output.NextContinuationToken == nil || aws.ToString(output.NextContinuationToken) == "" ||
			(continuationToken != nil && aws.ToString(output.NextContinuationToken) == aws.ToString(continuationToken)) {
			return fmt.Errorf("backup s3 repository: invalid garbage-list continuation token")
		}
		continuationToken = output.NextContinuationToken
	}
}

// ListGarbageObjects returns at most one provider page after a durable
// lexicographic cursor. Objects newer than before advance AfterKey but are not
// returned because the cycle cutoff is immutable.
func (r *S3Repository) ListGarbageObjects(
	ctx context.Context,
	before time.Time,
	afterKey string,
	limit int,
) (GarbageObjectPage, error) {
	if r == nil || r.client == nil || before.IsZero() || limit <= 0 || limit > 4096 ||
		(afterKey != "" && !safeRepositoryKey(afterKey)) {
		return GarbageObjectPage{}, fmt.Errorf("backup s3 repository: garbage page options are invalid")
	}
	prefix := r.prefix + "/"
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(r.bucket), Prefix: aws.String(prefix),
		MaxKeys: aws.Int32(int32(limit)),
	}
	if afterKey != "" {
		input.StartAfter = aws.String(prefix + afterKey)
	}
	output, err := r.client.ListObjectsV2(ctx, input)
	if err != nil {
		return GarbageObjectPage{}, mapS3Error(err)
	}
	if output == nil {
		return GarbageObjectPage{}, fmt.Errorf("backup s3 repository: object listing is missing")
	}
	page := GarbageObjectPage{
		Objects:  make([]backupartifact.RepositoryObject, 0, len(output.Contents)),
		AfterKey: afterKey, Complete: !aws.ToBool(output.IsTruncated),
	}
	previousFullKey := prefix + afterKey
	for _, object := range output.Contents {
		if object.Key == nil || object.LastModified == nil || object.Size == nil {
			return GarbageObjectPage{}, fmt.Errorf("backup s3 repository: garbage object metadata is incomplete")
		}
		fullKey := aws.ToString(object.Key)
		relative := strings.TrimPrefix(fullKey, prefix)
		if relative == fullKey || !safeRepositoryKey(relative) || fullKey <= previousFullKey {
			return GarbageObjectPage{}, fmt.Errorf("backup s3 repository: garbage page is not strictly ordered")
		}
		previousFullKey = fullKey
		page.AfterKey = relative
		if object.LastModified.UTC().Before(before.UTC()) {
			page.Objects = append(page.Objects, backupartifact.RepositoryObject{
				Key: relative, Size: aws.ToInt64(object.Size),
			})
		}
	}
	if !page.Complete && len(output.Contents) == 0 {
		return GarbageObjectPage{}, fmt.Errorf("backup s3 repository: truncated garbage page made no progress")
	}
	return page, nil
}

// ListErasureLedgerCommitKeys returns bounded lexically ordered commit-marker
// keys for one source-generation namespace.
func (r *S3Repository) ListErasureLedgerCommitKeys(ctx context.Context, namespace string) ([]string, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("backup s3 repository: repository is required")
	}
	if _, _, _, err := backupartifact.ParseErasureLedgerCommitKey(backupartifact.ErasureLedgerCommitKey(namespace, 0, 1)); err != nil {
		return nil, fmt.Errorf("backup s3 repository: erasure-ledger namespace is invalid")
	}
	prefix := r.prefix + "/erasure-ledger/streams/" + namespace + "/"
	keys := make([]string, 0)
	var continuationToken *string
	for {
		output, err := r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(r.bucket), Prefix: aws.String(prefix), ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, mapS3Error(err)
		}
		if output == nil {
			return nil, fmt.Errorf("backup s3 repository: erasure-ledger listing is missing")
		}
		for _, object := range output.Contents {
			fullKey := aws.ToString(object.Key)
			relative := strings.TrimPrefix(fullKey, r.prefix+"/")
			if relative == fullKey || !validErasureLedgerCommitKey(relative, namespace) {
				return nil, fmt.Errorf("%w: invalid listed erasure-ledger commit key", backupartifact.ErrObjectCorrupt)
			}
			keys = append(keys, relative)
			if len(keys) > backupartifact.MaxErasureLedgerEvents {
				return nil, fmt.Errorf("backup s3 repository: erasure-ledger commit listing exceeds limit")
			}
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		if output.NextContinuationToken == nil || aws.ToString(output.NextContinuationToken) == "" || (continuationToken != nil && aws.ToString(output.NextContinuationToken) == aws.ToString(continuationToken)) {
			return nil, fmt.Errorf("backup s3 repository: invalid erasure-ledger listing continuation token")
		}
		continuationToken = output.NextContinuationToken
	}
	sort.Strings(keys)
	return keys, nil
}

// Check verifies bucket reachability, versioning, and Object Lock before backup
// scheduling is allowed to start.
func (r *S3Repository) Check(ctx context.Context) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("backup s3 repository: client is required")
	}
	if _, err := r.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(r.bucket)}); err != nil {
		return fmt.Errorf("backup s3 repository %s: head bucket: %w", r.name, mapS3Error(err))
	}
	versioning, err := r.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(r.bucket)})
	if err != nil {
		return fmt.Errorf("backup s3 repository %s: get versioning: %w", r.name, mapS3Error(err))
	}
	if versioning == nil || versioning.Status != types.BucketVersioningStatusEnabled {
		return fmt.Errorf("backup s3 repository %s: bucket versioning must be enabled", r.name)
	}
	lock, err := r.client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: aws.String(r.bucket)})
	if err != nil {
		return fmt.Errorf("backup s3 repository %s: get Object Lock: %w", r.name, mapS3Error(err))
	}
	if lock == nil || lock.ObjectLockConfiguration == nil || lock.ObjectLockConfiguration.ObjectLockEnabled != types.ObjectLockEnabledEnabled {
		return fmt.Errorf("backup s3 repository %s: Object Lock must be enabled", r.name)
	}
	return nil
}

func (r *S3Repository) fullKey(key string) (string, error) {
	if r == nil || !safeRepositoryKey(key) {
		return "", fmt.Errorf("%w: unsafe S3 repository key", backupartifact.ErrInvalidObject)
	}
	return r.prefix + "/" + key, nil
}

func safeRepositoryKey(key string) bool {
	return key != "" && !strings.Contains(key, "\\") && !strings.HasPrefix(key, "/") && path.Clean(key) == key && key != "." && key != ".." && !strings.HasPrefix(key, "../")
}

func s3RepositoryObject(key string, size *int64, metadata map[string]string, providerChecksum *string) (backupartifact.RepositoryObject, error) {
	if size == nil || *size < 0 {
		return backupartifact.RepositoryObject{}, fmt.Errorf("%w: S3 object size is missing", backupartifact.ErrObjectCorrupt)
	}
	checksum := strings.ToLower(strings.TrimSpace(metadata[s3ChecksumMetadataKey]))
	if !validFileChecksum(checksum) {
		return backupartifact.RepositoryObject{}, fmt.Errorf("%w: S3 object checksum metadata is invalid", backupartifact.ErrObjectCorrupt)
	}
	decoded, _ := hex.DecodeString(checksum)
	if aws.ToString(providerChecksum) != base64.StdEncoding.EncodeToString(decoded) {
		return backupartifact.RepositoryObject{}, fmt.Errorf("%w: S3 provider checksum mismatch", backupartifact.ErrObjectCorrupt)
	}
	return backupartifact.RepositoryObject{Key: key, Size: *size, SHA256: checksum}, nil
}

func mapS3Error(err error) error {
	if err == nil {
		return nil
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "PreconditionFailed":
			return backupartifact.ErrObjectExists
		case "NoSuchKey", "NotFound", "NoSuchBucket":
			return backupartifact.ErrObjectNotFound
		}
	}
	return err
}

func s3GarbageDeleteMayBeLocked(err error) bool {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "AccessDenied", "InvalidRequest":
			return true
		}
	}
	return false
}

func (r *S3Repository) classifyGarbageDeleteError(
	ctx context.Context,
	fullKey string,
	versionID string,
	err error,
) error {
	if s3GarbageDeleteMayBeLocked(err) &&
		r.objectVersionLocked(ctx, fullKey, versionID) {
		return backupartifact.ErrObjectLocked
	}
	return mapS3Error(err)
}

func (r *S3Repository) objectVersionLocked(
	ctx context.Context,
	fullKey string,
	versionID string,
) bool {
	output, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket), Key: aws.String(fullKey), VersionId: aws.String(versionID),
	})
	if err != nil || output == nil {
		return false
	}
	return output.ObjectLockLegalHoldStatus == types.ObjectLockLegalHoldStatusOn ||
		(output.ObjectLockMode != "" && output.ObjectLockRetainUntilDate != nil &&
			output.ObjectLockRetainUntilDate.After(r.now().UTC()))
}

var _ backupartifact.Repository = (*S3Repository)(nil)
var _ backupartifact.RepairRepository = (*S3RepairRepository)(nil)
var _ GenerationGarbageRepository = (*S3Repository)(nil)
