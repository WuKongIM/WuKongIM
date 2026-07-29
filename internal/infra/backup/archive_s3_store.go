package backup

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// S3ArchiveStoreOptions configures one S3-compatible repository prefix.
type S3ArchiveStoreOptions struct {
	// Endpoint is the HTTP(S) S3-compatible service address.
	Endpoint string
	// Region is optional only when the target service does not require one.
	Region string
	// Bucket is the existing repository bucket; this adapter never creates it.
	Bucket string
	// Prefix isolates one cluster-bound repository within Bucket.
	Prefix string
	// AccessKey is the decrypted runtime credential and must never be logged.
	AccessKey string
	// SecretKey is the decrypted runtime credential and must never be returned.
	SecretKey string
	// PathStyle selects compatibility addressing for services such as MinIO.
	PathStyle bool
	// VirtualHost forces DNS bucket addressing required by OSS and COS.
	VirtualHost bool
}

// S3ArchiveStore stores archive objects in one S3-compatible bucket prefix.
type S3ArchiveStore struct {
	api    s3ArchiveAPI
	prefix string
}

// NewS3ArchiveStore creates a bounded S3-compatible archive store.
func NewS3ArchiveStore(options S3ArchiveStoreOptions) (*S3ArchiveStore, error) {
	if options.PathStyle && options.VirtualHost {
		return nil, fmt.Errorf("backup S3 store: addressing styles conflict")
	}
	endpoint, secure, err := parseS3Endpoint(options.Endpoint)
	if err != nil {
		return nil, err
	}
	bucket := strings.TrimSpace(options.Bucket)
	accessKey := strings.TrimSpace(options.AccessKey)
	if bucket == "" || accessKey == "" || options.SecretKey == "" {
		return nil, fmt.Errorf("backup S3 store: bucket and credentials are required")
	}
	prefix, err := normalizeS3Prefix(options.Prefix)
	if err != nil {
		return nil, err
	}
	lookup := minio.BucketLookupAuto
	if options.PathStyle {
		lookup = minio.BucketLookupPath
	} else if options.VirtualHost {
		lookup = minio.BucketLookupDNS
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKey, options.SecretKey, ""),
		Secure:       secure,
		Region:       strings.TrimSpace(options.Region),
		BucketLookup: lookup,
		MaxRetries:   3,
	})
	if err != nil {
		return nil, fmt.Errorf("backup S3 store: create client: %w", err)
	}
	return newS3ArchiveStore(&minioArchiveAPI{
		client: client, bucket: bucket,
	}, prefix), nil
}

func newS3ArchiveStore(api s3ArchiveAPI, prefix string) *S3ArchiveStore {
	return &S3ArchiveStore{api: api, prefix: prefix}
}

// Put uploads one exact-size object.
func (s *S3ArchiveStore) Put(
	ctx context.Context,
	object backupartifact.PutObject,
) error {
	if s == nil || s.api == nil || object.Body == nil {
		return backupartifact.ErrInvalidObject
	}
	if err := backupartifact.ValidateRepositoryKey(object.Key); err != nil {
		return fmt.Errorf("%w: %v", backupartifact.ErrInvalidObject, err)
	}
	return s.api.put(
		ctx, s.objectKey(object.Key), object.Body,
		object.ExpectedBytes, object.IfAbsent,
	)
}

// Open returns one exact repository object.
func (s *S3ArchiveStore) Open(
	ctx context.Context,
	key string,
) (io.ReadCloser, backupartifact.ArchiveObject, error) {
	if err := backupartifact.ValidateRepositoryKey(key); err != nil {
		return nil, backupartifact.ArchiveObject{},
			fmt.Errorf("%w: %v", backupartifact.ErrInvalidObject, err)
	}
	reader, info, err := s.api.open(ctx, s.objectKey(key))
	if err != nil {
		return nil, backupartifact.ArchiveObject{}, err
	}
	return reader, backupartifact.ArchiveObject{
		Key: key, Bytes: info.bytes, Modified: info.modified,
	}, nil
}

// List returns sorted objects below one repository-relative prefix.
func (s *S3ArchiveStore) List(
	ctx context.Context,
	prefix string,
) ([]backupartifact.ArchiveObject, error) {
	if err := backupartifact.ValidateRepositoryKey(prefix); err != nil {
		return nil, fmt.Errorf("%w: %v", backupartifact.ErrInvalidObject, err)
	}
	subtreePrefix := strings.TrimSuffix(prefix, "/") + "/"
	items, err := s.api.list(ctx, s.objectKey(subtreePrefix))
	if err != nil {
		return nil, err
	}
	result := make([]backupartifact.ArchiveObject, 0, len(items))
	for _, item := range items {
		key, ok := s.relativeKey(item.key)
		if !ok {
			return nil, fmt.Errorf("%w: object escaped repository prefix", backupartifact.ErrObjectCorrupt)
		}
		result = append(result, backupartifact.ArchiveObject{
			Key: key, Bytes: item.bytes, Modified: item.modified,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

// Delete removes one exact repository object.
func (s *S3ArchiveStore) Delete(ctx context.Context, key string) error {
	if err := backupartifact.ValidateRepositoryKey(key); err != nil {
		return fmt.Errorf("%w: %v", backupartifact.ErrInvalidObject, err)
	}
	return s.api.remove(ctx, s.objectKey(key))
}

// DeletePrefix removes exactly the objects returned below the prefix.
func (s *S3ArchiveStore) DeletePrefix(ctx context.Context, prefix string) error {
	items, err := s.List(ctx, prefix)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := s.Delete(ctx, item.Key); err != nil {
			return err
		}
	}
	return nil
}

func (s *S3ArchiveStore) objectKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

func (s *S3ArchiveStore) relativeKey(key string) (string, bool) {
	if s.prefix == "" {
		return key, true
	}
	prefix := s.prefix + "/"
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	return strings.TrimPrefix(key, prefix), true
}

func parseS3Endpoint(value string) (string, bool, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", false, fmt.Errorf("backup S3 store: endpoint must be an HTTP(S) origin")
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

func normalizeS3Prefix(value string) (string, error) {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" {
		return "", nil
	}
	if path.Clean(value) != value || strings.HasPrefix(value, "../") {
		return "", fmt.Errorf("backup S3 store: prefix is unsafe")
	}
	if err := backupartifact.ValidateRepositoryKey(value); err != nil {
		return "", fmt.Errorf("backup S3 store: prefix is unsafe: %w", err)
	}
	return value, nil
}

type s3ArchiveObject struct {
	key      string
	bytes    uint64
	modified time.Time
}

type s3ArchiveAPI interface {
	put(context.Context, string, io.Reader, uint64, bool) error
	open(context.Context, string) (io.ReadCloser, s3ArchiveObject, error)
	list(context.Context, string) ([]s3ArchiveObject, error)
	remove(context.Context, string) error
}

type minioArchiveAPI struct {
	client *minio.Client
	bucket string
}

func (a *minioArchiveAPI) put(
	ctx context.Context,
	key string,
	body io.Reader,
	bytes uint64,
	ifAbsent bool,
) error {
	options := minio.PutObjectOptions{
		ContentType:      "application/octet-stream",
		DisableMultipart: bytes <= 64<<20,
	}
	if ifAbsent {
		options.SetMatchETagExcept("*")
	}
	info, err := a.client.PutObject(
		ctx, a.bucket, key, body, int64(bytes), options,
	)
	if err != nil {
		response := minio.ToErrorResponse(err)
		if response.Code == "PreconditionFailed" {
			return backupartifact.ErrObjectExists
		}
		return err
	}
	if uint64(info.Size) != bytes {
		return fmt.Errorf("%w: S3 upload size mismatch", backupartifact.ErrObjectCorrupt)
	}
	return nil
}

func (a *minioArchiveAPI) open(
	ctx context.Context,
	key string,
) (io.ReadCloser, s3ArchiveObject, error) {
	object, err := a.client.GetObject(
		ctx, a.bucket, key, minio.GetObjectOptions{},
	)
	if err != nil {
		return nil, s3ArchiveObject{}, mapS3NotFound(err)
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return nil, s3ArchiveObject{}, mapS3NotFound(err)
	}
	if info.Size < 0 {
		_ = object.Close()
		return nil, s3ArchiveObject{}, backupartifact.ErrObjectCorrupt
	}
	return object, s3ArchiveObject{
		key: key, bytes: uint64(info.Size), modified: info.LastModified.UTC(),
	}, nil
}

func (a *minioArchiveAPI) list(
	ctx context.Context,
	prefix string,
) ([]s3ArchiveObject, error) {
	result := make([]s3ArchiveObject, 0)
	for object := range a.client.ListObjects(ctx, a.bucket, minio.ListObjectsOptions{
		Prefix: prefix, Recursive: true,
	}) {
		if object.Err != nil {
			return nil, object.Err
		}
		if object.Size < 0 {
			return nil, backupartifact.ErrObjectCorrupt
		}
		result = append(result, s3ArchiveObject{
			key: object.Key, bytes: uint64(object.Size),
			modified: object.LastModified.UTC(),
		})
	}
	return result, nil
}

func (a *minioArchiveAPI) remove(ctx context.Context, key string) error {
	return a.client.RemoveObject(ctx, a.bucket, key, minio.RemoveObjectOptions{})
}

func mapS3NotFound(err error) error {
	if err == nil {
		return nil
	}
	response := minio.ToErrorResponse(err)
	if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" ||
		response.StatusCode == 404 {
		return backupartifact.ErrObjectNotFound
	}
	return err
}

var _ backupartifact.ArchiveStore = (*S3ArchiveStore)(nil)
