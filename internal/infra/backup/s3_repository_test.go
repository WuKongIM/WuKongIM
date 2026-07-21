package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

func TestS3RepositoryWritesImmutableChecksummedLockedObject(t *testing.T) {
	client := newFakeS3Client()
	repository, err := NewS3Repository(S3RepositoryOptions{
		Name:           "primary",
		Bucket:         "backup-primary",
		Prefix:         "prod/cluster-a",
		ObjectLockDays: 7,
		Client:         client,
		Now:            func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewS3Repository() error = %v", err)
	}
	body := []byte("encrypted-backup-object")
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	if err := repository.PutImmutable(context.Background(), "objects/job/00001/metadata-000001.bin", int64(len(body)), checksum, bytes.NewReader(body)); err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	put := client.lastPut
	if aws.ToString(put.IfNoneMatch) != "*" || put.ObjectLockMode != types.ObjectLockModeCompliance {
		t.Fatalf("immutable write preconditions = %#v", put)
	}
	if got := put.ObjectLockRetainUntilDate.Sub(time.Unix(1_700_000_000, 0).UTC()); got != 7*24*time.Hour {
		t.Fatalf("ObjectLock retention = %v, want 7d", got)
	}
	if aws.ToString(put.ChecksumSHA256) != base64.StdEncoding.EncodeToString(hash[:]) {
		t.Fatalf("ChecksumSHA256 = %q", aws.ToString(put.ChecksumSHA256))
	}
	stored, err := repository.Stat(context.Background(), "objects/job/00001/metadata-000001.bin")
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if stored.Size != int64(len(body)) || stored.SHA256 != checksum {
		t.Fatalf("Stat() = %#v", stored)
	}
	reader, opened, err := repository.Open(context.Background(), stored.Key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	got, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(got, body) || opened != stored {
		t.Fatalf("Open() body/meta = %q %#v errors=%v/%v", got, opened, readErr, closeErr)
	}
	if err := repository.PutImmutable(context.Background(), stored.Key, int64(len(body)), checksum, bytes.NewReader(body)); err != backupartifact.ErrObjectExists {
		t.Fatalf("second PutImmutable() error = %v, want ErrObjectExists", err)
	}
}

func TestS3RepositoryDoctorRequiresVersioningAndObjectLock(t *testing.T) {
	client := newFakeS3Client()
	repository, err := NewS3Repository(S3RepositoryOptions{Name: "secondary", Bucket: "backup-secondary", Prefix: "prod/cluster-a", ObjectLockDays: 7, Client: client})
	if err != nil {
		t.Fatalf("NewS3Repository() error = %v", err)
	}
	if err := repository.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	client.versioning = types.BucketVersioningStatusSuspended
	if err := repository.Check(context.Background()); err == nil {
		t.Fatal("Check() error = nil, want suspended versioning rejection")
	}
}

func TestS3RepositoryDeletesExactGarbageObjectVersions(t *testing.T) {
	client := newFakeS3Client()
	repository, err := NewS3Repository(S3RepositoryOptions{Name: "primary", Bucket: "backup-primary", Prefix: "prod/cluster-a", ObjectLockDays: 7, Client: client})
	if err != nil {
		t.Fatalf("NewS3Repository() error = %v", err)
	}
	body := []byte("expired-object")
	hash := sha256.Sum256(body)
	key := "objects/expired/00001.bin"
	if err := repository.PutImmutable(context.Background(), key, int64(len(body)), hex.EncodeToString(hash[:]), bytes.NewReader(body)); err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	if err := repository.DeleteGarbageObject(context.Background(), key); err != nil {
		t.Fatalf("DeleteGarbageObject() error = %v", err)
	}
	if _, err := repository.Stat(context.Background(), key); err != backupartifact.ErrObjectNotFound {
		t.Fatalf("Stat() error = %v, want %v", err, backupartifact.ErrObjectNotFound)
	}
	if client.lastDelete == nil || aws.ToString(client.lastDelete.VersionId) == "" {
		t.Fatalf("DeleteObject input = %#v, want exact version", client.lastDelete)
	}
}

type fakeS3Object struct {
	body     []byte
	metadata map[string]string
	checksum string
}

type fakeS3Client struct {
	objects    map[string]fakeS3Object
	lastPut    *s3.PutObjectInput
	lastDelete *s3.DeleteObjectInput
	versioning types.BucketVersioningStatus
	lock       types.ObjectLockEnabled
}

func newFakeS3Client() *fakeS3Client {
	return &fakeS3Client{objects: map[string]fakeS3Object{}, versioning: types.BucketVersioningStatusEnabled, lock: types.ObjectLockEnabledEnabled}
}

func (f *fakeS3Client) PutObject(_ context.Context, input *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	key := aws.ToString(input.Key)
	if _, exists := f.objects[key]; exists {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "already exists"}
	}
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	f.lastPut = input
	f.objects[key] = fakeS3Object{body: body, metadata: input.Metadata, checksum: aws.ToString(input.ChecksumSHA256)}
	return &s3.PutObjectOutput{ChecksumSHA256: input.ChecksumSHA256}, nil
}

func (f *fakeS3Client) HeadObject(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	object, ok := f.objects[aws.ToString(input.Key)]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NotFound", Message: "missing"}
	}
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(int64(len(object.body))), Metadata: object.metadata, ChecksumSHA256: aws.String(object.checksum)}, nil
}

func (f *fakeS3Client) GetObject(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	object, ok := f.objects[aws.ToString(input.Key)]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing"}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(object.body)), ContentLength: aws.Int64(int64(len(object.body))), Metadata: object.metadata, ChecksumSHA256: aws.String(object.checksum)}, nil
}

func (f *fakeS3Client) HeadBucket(context.Context, *s3.HeadBucketInput, ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	return &s3.HeadBucketOutput{}, nil
}

func (f *fakeS3Client) GetBucketVersioning(context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	return &s3.GetBucketVersioningOutput{Status: f.versioning}, nil
}

func (f *fakeS3Client) GetObjectLockConfiguration(context.Context, *s3.GetObjectLockConfigurationInput, ...func(*s3.Options)) (*s3.GetObjectLockConfigurationOutput, error) {
	return &s3.GetObjectLockConfigurationOutput{ObjectLockConfiguration: &types.ObjectLockConfiguration{ObjectLockEnabled: f.lock}}, nil
}

func (f *fakeS3Client) ListObjectsV2(_ context.Context, input *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	output := &s3.ListObjectsV2Output{}
	for key := range f.objects {
		if strings.HasPrefix(key, aws.ToString(input.Prefix)) {
			keyCopy := key
			output.Contents = append(output.Contents, types.Object{Key: &keyCopy})
		}
	}
	return output, nil
}

func (f *fakeS3Client) ListObjectVersions(_ context.Context, input *s3.ListObjectVersionsInput, _ ...func(*s3.Options)) (*s3.ListObjectVersionsOutput, error) {
	output := &s3.ListObjectVersionsOutput{}
	for key := range f.objects {
		if strings.HasPrefix(key, aws.ToString(input.Prefix)) {
			keyCopy, versionID := key, "version-1"
			output.Versions = append(output.Versions, types.ObjectVersion{Key: &keyCopy, VersionId: &versionID})
		}
	}
	return output, nil
}

func (f *fakeS3Client) DeleteObject(_ context.Context, input *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	f.lastDelete = input
	delete(f.objects, aws.ToString(input.Key))
	return &s3.DeleteObjectOutput{}, nil
}
