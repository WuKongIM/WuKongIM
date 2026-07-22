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
	_, err = r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:                    aws.String(r.bucket),
		Key:                       aws.String(fullKey),
		Body:                      body,
		ContentLength:             aws.Int64(size),
		ChecksumAlgorithm:         types.ChecksumAlgorithmSha256,
		ChecksumSHA256:            aws.String(checksumBase64),
		IfNoneMatch:               aws.String("*"),
		Metadata:                  map[string]string{s3ChecksumMetadataKey: checksum},
		ObjectLockMode:            types.ObjectLockModeCompliance,
		ObjectLockRetainUntilDate: &retainUntil,
	})
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
			return mapS3Error(err)
		}
	}
	return nil
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

// ListRestorePointIDs lists publication markers under the repository namespace.
// Every returned marker and manifest is still authenticated by the restore inspector.
func (r *S3Repository) ListRestorePointIDs(ctx context.Context) ([]string, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("backup s3 repository: repository is required")
	}
	prefix := r.prefix + "/restore-points/"
	ids := make(map[string]struct{})
	var continuationToken *string
	for {
		output, err := r.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket: aws.String(r.bucket), Prefix: aws.String(prefix), ContinuationToken: continuationToken,
		})
		if err != nil {
			return nil, mapS3Error(err)
		}
		if output == nil {
			return nil, fmt.Errorf("backup s3 repository: object listing is missing")
		}
		for _, object := range output.Contents {
			key := aws.ToString(object.Key)
			relative := strings.TrimPrefix(key, prefix)
			if relative == key || !strings.HasSuffix(relative, "/published.json") {
				continue
			}
			id := strings.TrimSuffix(relative, "/published.json")
			if !strings.Contains(id, "/") && safeRestorePointID(id) {
				ids[id] = struct{}{}
			}
		}
		if len(ids) > maxListedRestorePoints {
			return nil, fmt.Errorf("backup s3 repository: restore-point listing exceeds limit")
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		if output.NextContinuationToken == nil || aws.ToString(output.NextContinuationToken) == "" || (continuationToken != nil && aws.ToString(output.NextContinuationToken) == aws.ToString(continuationToken)) {
			return nil, fmt.Errorf("backup s3 repository: invalid listing continuation token")
		}
		continuationToken = output.NextContinuationToken
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}

// ListErasureLedgerCommitKeys returns bounded lexically ordered commit-marker keys.
func (r *S3Repository) ListErasureLedgerCommitKeys(ctx context.Context) ([]string, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("backup s3 repository: repository is required")
	}
	prefix := r.prefix + "/erasure-ledger/commits/"
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
			if relative == fullKey || !validErasureLedgerCommitKey(relative) {
				return nil, fmt.Errorf("%w: invalid listed erasure-ledger commit key", backupartifact.ErrObjectCorrupt)
			}
			keys = append(keys, relative)
			if len(keys) > maxErasureLedgerCommits {
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

var _ backupartifact.Repository = (*S3Repository)(nil)
var _ RestorePointLister = (*S3Repository)(nil)
