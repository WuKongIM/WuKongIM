package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"testing"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/minio/minio-go/v7"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestOSSCreateOnlyWriterUsesForbidOverwriteHeader(t *testing.T) {
	client := &recordingOSSPutObjectClient{}
	writer := &ossCreateOnlyWriter{
		client: client,
		bucket: "wukongim-backups",
	}

	body := []byte("marker")
	if err := writer.put(
		context.Background(),
		"cluster-a/probes/marker",
		bytes.NewReader(body),
		uint64(len(body)),
	); err != nil {
		t.Fatalf("put(): %v", err)
	}
	if client.request == nil {
		t.Fatal("request was not sent")
	}
	if got := aliyunoss.ToString(client.request.ForbidOverwrite); got != "true" {
		t.Fatalf("ForbidOverwrite = %q", got)
	}
	if got := aliyunoss.ToString(client.request.Bucket); got !=
		"wukongim-backups" {
		t.Fatalf("Bucket = %q", got)
	}
	if got := aliyunoss.ToString(client.request.Key); got !=
		"cluster-a/probes/marker" {
		t.Fatalf("Key = %q", got)
	}
}

func TestOSSCreateOnlyWriterMapsExistingObject(t *testing.T) {
	writer := &ossCreateOnlyWriter{
		client: &recordingOSSPutObjectClient{
			err: &aliyunoss.ServiceError{
				StatusCode: 409,
				Code:       "FileAlreadyExists",
				Message:    "object exists",
				RequestID:  "request-1",
			},
		},
		bucket: "wukongim-backups",
	}

	body := []byte("marker")
	err := writer.put(
		context.Background(),
		"cluster-a/probes/marker",
		bytes.NewReader(body),
		uint64(len(body)),
	)
	if !errors.Is(err, backupartifact.ErrObjectExists) {
		t.Fatalf("put() error = %v", err)
	}
}

func TestCOSForbidOverwriteTransportAddsNativeHeader(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := &cosForbidOverwriteTransport{next: recorder}
	request, err := http.NewRequest(
		http.MethodPut,
		"https://bucket.cos.ap-guangzhou.myqcloud.com/object",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("If-None-Match", "*")

	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatalf("RoundTrip(): %v", err)
	}
	if recorder.request == nil {
		t.Fatal("request was not forwarded")
	}
	if got := recorder.request.Header.Get("x-cos-forbid-overwrite"); got != "true" {
		t.Fatalf("x-cos-forbid-overwrite = %q", got)
	}
	if got := recorder.request.Header.Get("If-None-Match"); got != "*" {
		t.Fatalf("If-None-Match = %q", got)
	}
	if request.Header.Get("x-cos-forbid-overwrite") != "" {
		t.Fatal("caller request was mutated")
	}
}

func TestCOSForbidOverwriteTransportLeavesOrdinaryWritesUnchanged(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := &cosForbidOverwriteTransport{next: recorder}
	request, err := http.NewRequest(
		http.MethodPut,
		"https://bucket.cos.ap-guangzhou.myqcloud.com/object",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatalf("RoundTrip(): %v", err)
	}
	if got := recorder.request.Header.Get("x-cos-forbid-overwrite"); got != "" {
		t.Fatalf("x-cos-forbid-overwrite = %q", got)
	}
}

func TestMapCreateOnlyS3ErrorMapsCOSNotModified(t *testing.T) {
	err := mapCreateOnlyS3Error(minio.ErrorResponse{
		StatusCode: http.StatusNotModified,
		Code:       "Not Modified",
	})
	if !errors.Is(err, backupartifact.ErrObjectExists) {
		t.Fatalf("mapCreateOnlyS3Error() = %v", err)
	}
}

func TestMapCreateOnlyS3ErrorMapsS3PreconditionFailed(t *testing.T) {
	err := mapCreateOnlyS3Error(minio.ErrorResponse{
		StatusCode: http.StatusPreconditionFailed,
		Code:       "PreconditionFailed",
	})
	if !errors.Is(err, backupartifact.ErrObjectExists) {
		t.Fatalf("mapCreateOnlyS3Error() = %v", err)
	}
}

func TestS3ArchiveStoreKeepsKeysInsideConfiguredPrefix(t *testing.T) {
	api := &memoryS3ArchiveAPI{objects: map[string][]byte{}}
	store := newS3ArchiveStore(api, "tenant/cluster-a")
	body := []byte("archive")

	if err := store.Put(context.Background(), backupartifact.PutObject{
		Key: "backups/backup-1/COMPLETE", Body: bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)), IfAbsent: true,
	}); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	if _, exists := api.objects["tenant/cluster-a/backups/backup-1/COMPLETE"]; !exists {
		t.Fatalf("objects = %#v", api.objects)
	}
	items, err := store.List(context.Background(), "backups")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(items) != 1 || items[0].Key != "backups/backup-1/COMPLETE" {
		t.Fatalf("items = %#v", items)
	}
}

func TestParseS3EndpointRejectsEmbeddedPathOrCredentials(t *testing.T) {
	for _, value := range []string{
		"https://user:secret@s3.example.com",
		"https://s3.example.com/path",
		"ftp://s3.example.com",
	} {
		if _, _, err := parseS3Endpoint(value); err == nil {
			t.Fatalf("parseS3Endpoint(%q) error = nil", value)
		}
	}
}

func TestS3ArchiveStoreRejectsConflictingAddressingStyles(t *testing.T) {
	_, err := NewS3ArchiveStore(S3ArchiveStoreOptions{
		Endpoint:    "https://s3.example.com",
		Bucket:      "backups",
		Prefix:      "cluster-a",
		AccessKey:   "access-key",
		SecretKey:   "secret-key",
		PathStyle:   true,
		VirtualHost: true,
	})
	if err == nil {
		t.Fatal("NewS3ArchiveStore() error = nil")
	}
}

func TestS3ArchiveStoreDeletePrefixDoesNotMatchSiblingPrefix(t *testing.T) {
	api := &memoryS3ArchiveAPI{objects: map[string][]byte{
		"root/backups/abc/manifest.json":  []byte("abc"),
		"root/backups/abc2/manifest.json": []byte("abc2"),
	}}
	store := newS3ArchiveStore(api, "root")
	if err := store.DeletePrefix(context.Background(), "backups/abc"); err != nil {
		t.Fatalf("DeletePrefix(): %v", err)
	}
	if _, exists := api.objects["root/backups/abc/manifest.json"]; exists {
		t.Fatal("target subtree remains")
	}
	if _, exists := api.objects["root/backups/abc2/manifest.json"]; !exists {
		t.Fatal("sibling prefix was deleted")
	}
}

type memoryS3ArchiveAPI struct {
	objects map[string][]byte
}

type recordingOSSPutObjectClient struct {
	request *aliyunoss.PutObjectRequest
	err     error
}

type recordingRoundTripper struct {
	request *http.Request
}

func (r *recordingRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	r.request = request
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Request:    request,
	}, nil
}

func (c *recordingOSSPutObjectClient) PutObject(
	_ context.Context,
	request *aliyunoss.PutObjectRequest,
	_ ...func(*aliyunoss.Options),
) (*aliyunoss.PutObjectResult, error) {
	c.request = request
	if c.err != nil {
		return nil, c.err
	}
	return &aliyunoss.PutObjectResult{}, nil
}

func (m *memoryS3ArchiveAPI) put(
	_ context.Context,
	key string,
	body io.Reader,
	expected uint64,
	ifAbsent bool,
) error {
	if ifAbsent {
		if _, exists := m.objects[key]; exists {
			return backupartifact.ErrObjectExists
		}
	}
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if uint64(len(value)) != expected {
		return backupartifact.ErrInvalidObject
	}
	m.objects[key] = value
	return nil
}

func (m *memoryS3ArchiveAPI) open(
	_ context.Context,
	key string,
) (io.ReadCloser, s3ArchiveObject, error) {
	value, exists := m.objects[key]
	if !exists {
		return nil, s3ArchiveObject{}, backupartifact.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(value)), s3ArchiveObject{
		key: key, bytes: uint64(len(value)),
	}, nil
}

func (m *memoryS3ArchiveAPI) list(
	_ context.Context,
	prefix string,
) ([]s3ArchiveObject, error) {
	result := make([]s3ArchiveObject, 0)
	for key, value := range m.objects {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			result = append(result, s3ArchiveObject{
				key: key, bytes: uint64(len(value)),
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result, nil
}

func (m *memoryS3ArchiveAPI) remove(_ context.Context, key string) error {
	delete(m.objects, key)
	return nil
}
