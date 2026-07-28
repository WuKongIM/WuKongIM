package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/stretchr/testify/require"
)

func TestOSSRepositoryWritesChecksummedObjectWithoutReplacingExistingKey(t *testing.T) {
	client := &fakeOSSClient{}
	repository, err := NewOSSRepository(OSSRepositoryOptions{
		Name:           "primary",
		Bucket:         "backup-primary",
		Prefix:         "prod/cluster-a",
		ObjectLockDays: 7,
		Client:         client,
		Now:            func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewOSSRepository() error = %v", err)
	}
	body := []byte("encrypted-backup-object")
	digest := sha256.Sum256(body)
	checksum := hex.EncodeToString(digest[:])
	key := "objects/job/00001/metadata-000001.bin"

	if err := repository.PutImmutable(
		context.Background(), key, int64(len(body)), checksum, bytes.NewReader(body),
	); err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	if client.putCalls != 1 {
		t.Fatalf("PutObject calls = %d, want 1", client.putCalls)
	}
	if client.lastPut == nil ||
		client.lastPut.Metadata[ossChecksumMetadataKey] != checksum {
		t.Fatalf("PutObject input = %#v", client.lastPut)
	}
	if err := repository.PutImmutable(
		context.Background(), key, int64(len(body)), checksum, bytes.NewReader(body),
	); err != backupartifact.ErrObjectExists {
		t.Fatalf("second PutImmutable() error = %v, want ErrObjectExists", err)
	}
	if client.putCalls != 1 {
		t.Fatalf("PutObject calls after duplicate = %d, want 1", client.putCalls)
	}
}

func TestOSSRepositoryRepairPublishesNewVersion(t *testing.T) {
	client := &fakeOSSClient{}
	repository, err := NewOSSRepository(OSSRepositoryOptions{
		Name: "primary", Bucket: "backup-primary", Prefix: "prod/cluster-a",
		ObjectLockDays: 7, Client: client,
	})
	if err != nil {
		t.Fatalf("NewOSSRepository() error = %v", err)
	}
	repair, err := NewOSSRepairRepository(OSSRepairRepositoryOptions{
		Repository: repository,
		Client:     client,
	})
	if err != nil {
		t.Fatalf("NewOSSRepairRepository() error = %v", err)
	}
	first := []byte("first")
	firstDigest := sha256.Sum256(first)
	key := "objects/job/00001/payload.bin"
	if err := repository.PutImmutable(
		context.Background(), key, int64(len(first)),
		hex.EncodeToString(firstDigest[:]), bytes.NewReader(first),
	); err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	repaired := []byte("repaired")
	repairedDigest := sha256.Sum256(repaired)
	if err := repair.RepairImmutable(
		context.Background(), key, int64(len(repaired)),
		hex.EncodeToString(repairedDigest[:]), bytes.NewReader(repaired),
	); err != nil {
		t.Fatalf("RepairImmutable() error = %v", err)
	}
	if client.putCalls != 2 || len(client.versions["prod/cluster-a/"+key]) != 2 {
		t.Fatalf("repair put/version counts = %d/%d, want 2/2",
			client.putCalls, len(client.versions["prod/cluster-a/"+key]))
	}
}

func TestOSSRepositoryDoctorRequiresVersioningAndComplianceObjectWorm(t *testing.T) {
	client := &fakeOSSClient{}
	repository, err := NewOSSRepository(OSSRepositoryOptions{
		Name: "secondary", Bucket: "backup-secondary", Prefix: "prod/cluster-a",
		ObjectLockDays: 7, Client: client,
	})
	if err != nil {
		t.Fatalf("NewOSSRepository() error = %v", err)
	}
	if err := repository.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	client.wormMode = "GOVERNANCE"
	if err := repository.Check(context.Background()); err == nil {
		t.Fatal("Check() error = nil, want non-compliance ObjectWorm rejection")
	}
	client.wormMode = "COMPLIANCE"
	client.versioning = "Suspended"
	if err := repository.Check(context.Background()); err == nil {
		t.Fatal("Check() error = nil, want suspended versioning rejection")
	}
}

func TestOSSRepositoryOpenDetectsBodyCorruption(t *testing.T) {
	client := &fakeOSSClient{}
	repository, err := NewOSSRepository(OSSRepositoryOptions{
		Name: "primary", Bucket: "backup-primary", Prefix: "prod/cluster-a",
		ObjectLockDays: 7, Client: client,
	})
	if err != nil {
		t.Fatalf("NewOSSRepository() error = %v", err)
	}
	body := []byte("healthy")
	digest := sha256.Sum256(body)
	key := "objects/job/00001/payload.bin"
	if err := repository.PutImmutable(
		context.Background(), key, int64(len(body)),
		hex.EncodeToString(digest[:]), bytes.NewReader(body),
	); err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	object := client.objects["prod/cluster-a/"+key]
	object.body = []byte("damaged")
	client.objects["prod/cluster-a/"+key] = object
	client.versions["prod/cluster-a/"+key][0] = object
	reader, _, err := repository.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	_, err = io.ReadAll(reader)
	_ = reader.Close()
	if !errors.Is(err, backupartifact.ErrObjectCorrupt) {
		t.Fatalf("ReadAll() error = %v, want ErrObjectCorrupt", err)
	}
}

func TestOSSRepositoryOpenUsesHeadMetadataAndPinsCurrentVersion(t *testing.T) {
	client := &fakeOSSClient{omitGetContentLength: true}
	repository, err := NewOSSRepository(OSSRepositoryOptions{
		Name: "primary", Bucket: "backup-primary", Prefix: "prod/cluster-a",
		ObjectLockDays: 7, Client: client,
	})
	if err != nil {
		t.Fatalf("NewOSSRepository() error = %v", err)
	}
	body := []byte("healthy without GET Content-Length")
	digest := sha256.Sum256(body)
	key := "objects/job/00001/head-metadata.bin"
	if err := repository.PutImmutable(
		context.Background(), key, int64(len(body)),
		hex.EncodeToString(digest[:]), bytes.NewReader(body),
	); err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	reader, object, err := repository.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	loaded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close error = %v/%v", readErr, closeErr)
	}
	if !bytes.Equal(loaded, body) || object.Size != int64(len(body)) {
		t.Fatalf("loaded/object = %q/%+v", loaded, object)
	}
	if client.lastGet == nil ||
		derefOSSTest(client.lastGet.VersionId) != "version-1" {
		t.Fatalf("GetObject version = %+v, want version-1", client.lastGet)
	}
}

func TestOSSRepositoryBoundsExactVersionDeletion(t *testing.T) {
	client := &fakeOSSClient{}
	repository, err := NewOSSRepository(OSSRepositoryOptions{
		Name: "primary", Bucket: "backup-primary", Prefix: "prod/cluster-a",
		ObjectLockDays: 7, Client: client,
	})
	if err != nil {
		t.Fatalf("NewOSSRepository() error = %v", err)
	}
	body := []byte("expired")
	digest := sha256.Sum256(body)
	key := "objects/rebase-00000-00000000000000000001/attempt/00000/object.bin"
	if err := repository.PutImmutable(
		context.Background(), key, int64(len(body)),
		hex.EncodeToString(digest[:]), bytes.NewReader(body),
	); err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	used, err := repository.DeleteGenerationGarbageObject(
		context.Background(), key, 1,
	)
	if used != 1 || !errors.Is(err, errGenerationGCRequestBudget) {
		t.Fatalf("bounded delete used/error = %d/%v", used, err)
	}
	client.deleteErr = &oss.ServiceError{
		Code: "FileImmutable", StatusCode: 409,
	}
	used, err = repository.DeleteGenerationGarbageObject(
		context.Background(), key, 2,
	)
	if used != 2 || !errors.Is(err, backupartifact.ErrObjectLocked) {
		t.Fatalf("locked delete used/error = %d/%v", used, err)
	}
	client.deleteErr = nil
	used, err = repository.DeleteGenerationGarbageObject(
		context.Background(), key, 3,
	)
	if used != 2 || err != nil {
		t.Fatalf("exact delete used/error = %d/%v", used, err)
	}
	if client.lastDelete == nil ||
		derefOSSTest(client.lastDelete.VersionId) == "" {
		t.Fatalf("DeleteObject input = %#v", client.lastDelete)
	}
}

func TestOSSRepositoryQualifiesGarbageRoleWithDeleteMarkerOnly(t *testing.T) {
	client := &fakeOSSClient{}
	repository, err := NewOSSRepository(OSSRepositoryOptions{
		Name: "primary", Bucket: "backup-primary", Prefix: "prod/cluster-a",
		ObjectLockDays: 7, Client: client,
	})
	if err != nil {
		t.Fatalf("NewOSSRepository() error = %v", err)
	}

	if err := repository.QualifyGarbageAccess(
		context.Background(), "0011223344556677",
	); err != nil {
		t.Fatalf("QualifyGarbageAccess() error = %v", err)
	}
	if client.deleteCalls != 2 {
		t.Fatalf("DeleteObject calls = %d, want 2", client.deleteCalls)
	}
	if client.listVersionCalls != 2 {
		t.Fatalf("ListObjectVersions calls = %d, want 2", client.listVersionCalls)
	}
	if len(client.deleteMarkers) != 0 {
		t.Fatalf("delete markers = %#v, want none", client.deleteMarkers)
	}
	if len(client.objects) != 0 || len(client.versions) != 0 {
		t.Fatal("garbage qualification created or deleted a data object")
	}
}

func TestOSSRepositoryGarbageQualificationRejectsMissingDeleteVersionPermission(t *testing.T) {
	client := &fakeOSSClient{deleteVersionErr: &oss.ServiceError{
		Code: "AccessDenied", StatusCode: 403,
	}}
	repository, err := NewOSSRepository(OSSRepositoryOptions{
		Name: "primary", Bucket: "backup-primary", Prefix: "prod/cluster-a",
		ObjectLockDays: 7, Client: client,
	})
	if err != nil {
		t.Fatalf("NewOSSRepository() error = %v", err)
	}

	err = repository.QualifyGarbageAccess(
		context.Background(), "0011223344556677",
	)
	if err == nil || !strings.Contains(err.Error(), "delete garbage-role marker") {
		t.Fatalf("QualifyGarbageAccess() error = %v, want delete-marker failure", err)
	}
	markersAfterFirstFailure := len(client.deleteMarkers)
	err = repository.QualifyGarbageAccess(
		context.Background(), "0011223344556677",
	)
	if err == nil || len(client.deleteMarkers) != markersAfterFirstFailure ||
		markersAfterFirstFailure != 1 {
		t.Fatalf(
			"retry error/markers = %v/%d, want failure with one bounded marker",
			err, len(client.deleteMarkers),
		)
	}
}

func TestOSSRepositoryLeastPrivilegeProbesRejectBroadRoles(t *testing.T) {
	accessDenied := &oss.ServiceError{
		Code: "AccessDenied", StatusCode: 403,
	}
	newRepository := func(t *testing.T, client *fakeOSSClient) *OSSRepository {
		t.Helper()
		repository, err := NewOSSRepository(OSSRepositoryOptions{
			Name: "primary", Bucket: "backup-primary",
			Prefix: "prod/cluster-a", ObjectLockDays: 7, Client: client,
		})
		require.NoError(t, err)
		return repository
	}

	t.Run("ordinary role", func(t *testing.T) {
		restrictedClient := &fakeOSSClient{deleteErr: accessDenied}
		restricted := newRepository(t, restrictedClient)
		require.NoError(t,
			restricted.QualifyOrdinaryRoleLeastPrivilege(context.Background()))
		require.NotNil(t, restrictedClient.lastDelete)
		require.Empty(t, derefOSSTest(restrictedClient.lastDelete.VersionId))

		broad := newRepository(t, &fakeOSSClient{})
		require.Error(t,
			broad.QualifyOrdinaryRoleLeastPrivilege(context.Background()))
	})

	t.Run("repair role", func(t *testing.T) {
		ordinary := newRepository(t, &fakeOSSClient{})
		restrictedClient := &fakeOSSClient{deleteErr: accessDenied}
		restricted, err := NewOSSRepairRepository(OSSRepairRepositoryOptions{
			Repository: ordinary,
			Client:     restrictedClient,
		})
		require.NoError(t, err)
		require.NoError(t,
			restricted.QualifyRepairRoleLeastPrivilege(context.Background()))
		require.NotNil(t, restrictedClient.lastDelete)
		require.Empty(t, derefOSSTest(restrictedClient.lastDelete.VersionId))

		broad, err := NewOSSRepairRepository(OSSRepairRepositoryOptions{
			Repository: ordinary, Client: &fakeOSSClient{},
		})
		require.NoError(t, err)
		require.Error(t,
			broad.QualifyRepairRoleLeastPrivilege(context.Background()))
	})

	t.Run("garbage role", func(t *testing.T) {
		restricted := newRepository(t, &fakeOSSClient{
			getErr: accessDenied,
		})
		require.NoError(t,
			restricted.QualifyGarbageRoleLeastPrivilege(context.Background()))

		broad := newRepository(t, &fakeOSSClient{})
		require.Error(t,
			broad.QualifyGarbageRoleLeastPrivilege(context.Background()))
	})
}

type fakeOSSVersion struct {
	body         []byte
	checksum     string
	versionID    string
	lastModified time.Time
	retainUntil  string
}

type fakeOSSClient struct {
	objects              map[string]fakeOSSVersion
	versions             map[string][]fakeOSSVersion
	putCalls             int
	lastPut              *oss.PutObjectRequest
	versioning           string
	wormMode             string
	wormDays             int32
	deleteErr            error
	deleteVersionErr     error
	deleteCalls          int
	listVersionCalls     int
	deleteMarkers        map[string][]string
	lastDelete           *oss.DeleteObjectRequest
	lastGet              *oss.GetObjectRequest
	getErr               error
	omitGetContentLength bool
}

func (f *fakeOSSClient) ensureDefaults() {
	if f.objects == nil {
		f.objects = make(map[string]fakeOSSVersion)
	}
	if f.versions == nil {
		f.versions = make(map[string][]fakeOSSVersion)
	}
	if f.deleteMarkers == nil {
		f.deleteMarkers = make(map[string][]string)
	}
	if f.versioning == "" {
		f.versioning = "Enabled"
	}
	if f.wormMode == "" {
		f.wormMode = "COMPLIANCE"
	}
	if f.wormDays == 0 {
		f.wormDays = 7
	}
}

func (f *fakeOSSClient) PutObject(
	_ context.Context,
	input *oss.PutObjectRequest,
	_ ...func(*oss.Options),
) (*oss.PutObjectResult, error) {
	f.ensureDefaults()
	f.putCalls++
	f.lastPut = input
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	versionID := "version-" + string(rune('0'+f.putCalls))
	version := fakeOSSVersion{
		body:         append([]byte(nil), body...),
		checksum:     input.Metadata[ossChecksumMetadataKey],
		versionID:    versionID,
		lastModified: time.Unix(1_700_000_000+int64(f.putCalls), 0).UTC(),
	}
	key := derefOSSTest(input.Key)
	f.objects[key] = version
	f.versions[key] = append(f.versions[key], version)
	return &oss.PutObjectResult{VersionId: ptrOSSTest(versionID)}, nil
}

func (f *fakeOSSClient) HeadObject(
	_ context.Context,
	input *oss.HeadObjectRequest,
	_ ...func(*oss.Options),
) (*oss.HeadObjectResult, error) {
	f.ensureDefaults()
	version, ok := f.objects[derefOSSTest(input.Key)]
	if !ok {
		return nil, &oss.ServiceError{Code: "NoSuchKey", StatusCode: 404}
	}
	return &oss.HeadObjectResult{
		ContentLength: int64(len(version.body)),
		Metadata: map[string]string{
			ossChecksumMetadataKey: version.checksum,
		},
		VersionId:    ptrOSSTest(version.versionID),
		LastModified: &version.lastModified,
	}, nil
}

func (f *fakeOSSClient) GetObject(
	_ context.Context,
	input *oss.GetObjectRequest,
	_ ...func(*oss.Options),
) (*oss.GetObjectResult, error) {
	f.ensureDefaults()
	f.lastGet = input
	if f.getErr != nil {
		return nil, f.getErr
	}
	version, ok := f.objects[derefOSSTest(input.Key)]
	if !ok {
		return nil, &oss.ServiceError{Code: "NoSuchKey", StatusCode: 404}
	}
	if versionID := derefOSSTest(input.VersionId); versionID != "" {
		ok = false
		for _, candidate := range f.versions[derefOSSTest(input.Key)] {
			if candidate.versionID == versionID {
				version = candidate
				ok = true
				break
			}
		}
		if !ok {
			return nil, &oss.ServiceError{
				Code: "NoSuchVersion", StatusCode: 404,
			}
		}
	}
	contentLength := int64(len(version.body))
	if f.omitGetContentLength {
		contentLength = 0
	}
	return &oss.GetObjectResult{
		ContentLength: contentLength,
		Metadata: map[string]string{
			ossChecksumMetadataKey: version.checksum,
		},
		VersionId: ptrOSSTest(version.versionID),
		Body:      io.NopCloser(bytes.NewReader(version.body)),
	}, nil
}

func (f *fakeOSSClient) GetBucketVersioning(
	_ context.Context,
	_ *oss.GetBucketVersioningRequest,
	_ ...func(*oss.Options),
) (*oss.GetBucketVersioningResult, error) {
	f.ensureDefaults()
	return &oss.GetBucketVersioningResult{VersionStatus: ptrOSSTest(f.versioning)}, nil
}

func (f *fakeOSSClient) GetBucketObjectWormConfiguration(
	_ context.Context,
	_ *oss.GetBucketObjectWormConfigurationRequest,
	_ ...func(*oss.Options),
) (*oss.GetBucketObjectWormConfigurationResult, error) {
	f.ensureDefaults()
	return &oss.GetBucketObjectWormConfigurationResult{
		ObjectWormConfiguration: &oss.ObjectWormConfiguration{
			ObjectWormEnabled: ptrOSSTest("Enabled"),
			Rule: &oss.ObjectWormRule{
				DefaultRetention: &oss.ObjectWormDefaultRetention{
					Mode: ptrOSSTest(f.wormMode),
					Days: &f.wormDays,
				},
			},
		},
	}, nil
}

func (f *fakeOSSClient) ListObjectsV2(
	context.Context,
	*oss.ListObjectsV2Request,
	...func(*oss.Options),
) (*oss.ListObjectsV2Result, error) {
	return &oss.ListObjectsV2Result{}, nil
}

func (f *fakeOSSClient) ListObjectVersions(
	_ context.Context,
	input *oss.ListObjectVersionsRequest,
	_ ...func(*oss.Options),
) (*oss.ListObjectVersionsResult, error) {
	f.ensureDefaults()
	f.listVersionCalls++
	result := &oss.ListObjectVersionsResult{}
	prefix := derefOSSTest(input.Prefix)
	for key, versions := range f.versions {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		for _, version := range versions {
			version := version
			result.ObjectVersions = append(
				result.ObjectVersions,
				oss.ObjectVersionProperties{
					Key:          ptrOSSTest(key),
					VersionId:    ptrOSSTest(version.versionID),
					LastModified: &version.lastModified,
					Size:         int64(len(version.body)),
				},
			)
		}
	}
	for key, versions := range f.deleteMarkers {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix {
			continue
		}
		for _, versionID := range versions {
			result.ObjectDeleteMarkers = append(
				result.ObjectDeleteMarkers,
				oss.ObjectDeleteMarkerProperties{
					Key: ptrOSSTest(key), VersionId: ptrOSSTest(versionID),
					IsLatest: true,
				},
			)
		}
	}
	return result, nil
}

func (f *fakeOSSClient) DeleteObject(
	_ context.Context,
	input *oss.DeleteObjectRequest,
	_ ...func(*oss.Options),
) (*oss.DeleteObjectResult, error) {
	f.ensureDefaults()
	f.deleteCalls++
	f.lastDelete = input
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	key := derefOSSTest(input.Key)
	versionID := derefOSSTest(input.VersionId)
	if versionID == "" {
		versionID = fmt.Sprintf("delete-marker-%d", f.deleteCalls)
		f.deleteMarkers[key] = append(f.deleteMarkers[key], versionID)
		return &oss.DeleteObjectResult{
			VersionId: ptrOSSTest(versionID), DeleteMarker: true,
		}, nil
	}
	if f.deleteVersionErr != nil {
		return nil, f.deleteVersionErr
	}
	markers := f.deleteMarkers[key]
	keptMarkers := markers[:0]
	for _, marker := range markers {
		if marker != versionID {
			keptMarkers = append(keptMarkers, marker)
		}
	}
	if len(keptMarkers) == 0 {
		delete(f.deleteMarkers, key)
	} else {
		f.deleteMarkers[key] = keptMarkers
	}
	versions := f.versions[key]
	kept := versions[:0]
	for _, version := range versions {
		if version.versionID != versionID {
			kept = append(kept, version)
		}
	}
	if len(kept) == 0 {
		delete(f.versions, key)
		delete(f.objects, key)
	} else {
		f.versions[key] = kept
		f.objects[key] = kept[len(kept)-1]
	}
	return &oss.DeleteObjectResult{VersionId: ptrOSSTest(versionID)}, nil
}

func (f *fakeOSSClient) GetObjectRetention(
	context.Context,
	*oss.GetObjectRetentionRequest,
	...func(*oss.Options),
) (*oss.GetObjectRetentionResult, error) {
	return &oss.GetObjectRetentionResult{}, nil
}

func ptrOSSTest[T any](value T) *T {
	return &value
}

func derefOSSTest(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
