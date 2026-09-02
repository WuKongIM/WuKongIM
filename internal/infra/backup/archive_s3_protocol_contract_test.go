package backup

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestS3ArchiveStoreStreamsCRUDThroughTheS3Protocol(t *testing.T) {
	transport := &memoryS3ProtocolTransport{
		bucket:  "contract-bucket",
		objects: make(map[string][]byte),
	}
	store := newS3ArchiveStore(
		&minioArchiveAPI{
			client: newProtocolContractMinioClient(t, transport),
			bucket: transport.bucket,
		},
		"cluster-root",
	)
	ctx := context.Background()
	first := []byte("manifest-body")
	if err := store.Put(ctx, backupartifact.PutObject{
		Key: "backups/backup-1/manifest.json", Body: bytes.NewReader(first),
		ExpectedBytes: uint64(len(first)), IfAbsent: true,
	}); err != nil {
		t.Fatalf("Put(manifest): %v", err)
	}
	if transport.lastPutIfNoneMatch != "*" ||
		transport.lastPutContentType != "application/octet-stream" {
		t.Fatalf(
			"PUT conditions = %q/%q",
			transport.lastPutIfNoneMatch, transport.lastPutContentType,
		)
	}
	second := []byte("complete-body")
	if err := store.Put(ctx, backupartifact.PutObject{
		Key: "backups/backup-1/COMPLETE", Body: bytes.NewReader(second),
		ExpectedBytes: uint64(len(second)),
	}); err != nil {
		t.Fatalf("Put(COMPLETE): %v", err)
	}

	reader, object, err := store.Open(ctx, "backups/backup-1/manifest.json")
	if err != nil {
		t.Fatalf("Open(manifest): %v", err)
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close manifest = %v/%v", readErr, closeErr)
	}
	if !bytes.Equal(body, first) || object.Key != "backups/backup-1/manifest.json" ||
		object.Bytes != uint64(len(first)) || object.Modified.IsZero() {
		t.Fatalf("opened object = %q / %+v", body, object)
	}

	items, err := store.List(ctx, "backups/backup-1")
	if err != nil {
		t.Fatalf("List(backup): %v", err)
	}
	if len(items) != 2 || items[0].Key != "backups/backup-1/COMPLETE" ||
		items[1].Key != "backups/backup-1/manifest.json" {
		t.Fatalf("listed objects = %+v", items)
	}

	if err := store.Delete(ctx, "backups/backup-1/manifest.json"); err != nil {
		t.Fatalf("Delete(manifest): %v", err)
	}
	if _, _, err := store.Open(
		ctx, "backups/backup-1/manifest.json",
	); !errors.Is(err, backupartifact.ErrObjectNotFound) {
		t.Fatalf("Open(deleted) error = %v", err)
	}
	if err := store.DeletePrefix(ctx, "backups/backup-1"); err != nil {
		t.Fatalf("DeletePrefix(backup): %v", err)
	}
	items, err = store.List(ctx, "backups/backup-1")
	if err != nil || len(items) != 0 {
		t.Fatalf("List(after delete) = %+v, %v", items, err)
	}
}

func TestS3ArchiveStoreMapsAtomicWriteAndRepositoryBoundaryFailures(t *testing.T) {
	transport := &memoryS3ProtocolTransport{
		bucket:  "contract-bucket",
		objects: map[string][]byte{"cluster-root/existing": []byte("old")},
	}
	store := newS3ArchiveStore(
		&minioArchiveAPI{
			client: newProtocolContractMinioClient(t, transport),
			bucket: transport.bucket,
		},
		"cluster-root",
	)
	ctx := context.Background()
	transport.rejectConditionalPut = true
	err := store.Put(ctx, backupartifact.PutObject{
		Key: "existing", Body: strings.NewReader("new"),
		ExpectedBytes: 3, IfAbsent: true,
	})
	if !errors.Is(err, backupartifact.ErrObjectExists) {
		t.Fatalf("Put(existing) error = %v", err)
	}

	for _, operation := range []func() error{
		func() error {
			return store.Put(ctx, backupartifact.PutObject{
				Key: "../escape", Body: strings.NewReader("x"), ExpectedBytes: 1,
			})
		},
		func() error {
			_, _, err := store.Open(ctx, "../escape")
			return err
		},
		func() error {
			_, err := store.List(ctx, "../escape")
			return err
		},
		func() error { return store.Delete(ctx, "../escape") },
	} {
		if err := operation(); !errors.Is(err, backupartifact.ErrInvalidObject) {
			t.Fatalf("unsafe key error = %v", err)
		}
	}

	escaped := newS3ArchiveStore(&escapedListS3API{}, "cluster-root")
	if _, err := escaped.List(ctx, "backups"); !errors.Is(
		err, backupartifact.ErrObjectCorrupt,
	) {
		t.Fatalf("List(escaped object) error = %v", err)
	}
	if got := mapS3NotFound(nil); got != nil {
		t.Fatalf("mapS3NotFound(nil) = %v", got)
	}
	if got := mapS3NotFound(errors.New("transport")); got == nil ||
		errors.Is(got, backupartifact.ErrObjectNotFound) {
		t.Fatalf("mapS3NotFound(transport) = %v", got)
	}
}

func TestS3ArchiveStoreValidatesAddressingAndProviderOptionsBeforeUse(t *testing.T) {
	base := S3ArchiveStoreOptions{
		Endpoint: "https://s3.example.com", Region: "us-east-1",
		Bucket: "backups", Prefix: "cluster", AccessKey: "access",
		SecretKey: "secret",
	}
	for _, mutate := range []func(*S3ArchiveStoreOptions){
		func(options *S3ArchiveStoreOptions) { options.Endpoint = "ftp://s3.example.com" },
		func(options *S3ArchiveStoreOptions) { options.Bucket = "" },
		func(options *S3ArchiveStoreOptions) { options.Prefix = "a/../b" },
		func(options *S3ArchiveStoreOptions) { options.Prefix = `a\b` },
	} {
		options := base
		mutate(&options)
		if _, err := NewS3ArchiveStore(options); err == nil {
			t.Fatalf("NewS3ArchiveStore(%+v) error = nil", options)
		}
	}
	for _, mutate := range []func(*S3ArchiveStoreOptions){
		func(options *S3ArchiveStoreOptions) { options.PathStyle = true },
		func(options *S3ArchiveStoreOptions) { options.VirtualHost = true },
		func(options *S3ArchiveStoreOptions) { options.COS = true },
		func(options *S3ArchiveStoreOptions) {
			options.OSS = true
			options.OSSNativeEndpoint = "https://oss.example.com"
		},
		func(options *S3ArchiveStoreOptions) { options.Prefix = "///" },
	} {
		options := base
		mutate(&options)
		if _, err := NewS3ArchiveStore(options); err != nil {
			t.Fatalf("NewS3ArchiveStore(%+v): %v", options, err)
		}
	}
}

func TestOSSArchiveAPISelectsAtomicAndOrdinaryWriteProtocols(t *testing.T) {
	transport := &memoryS3ProtocolTransport{
		bucket: "contract-bucket", objects: make(map[string][]byte),
	}
	ordinary := &minioArchiveAPI{
		client: newProtocolContractMinioClient(t, transport),
		bucket: transport.bucket,
	}
	client := &recordingOSSPutObjectClient{}
	api := &ossArchiveAPI{
		minioArchiveAPI: ordinary,
		createOnly:      &ossCreateOnlyWriter{client: client, bucket: transport.bucket},
	}
	if err := api.put(
		context.Background(), "ordinary", strings.NewReader("body"), 4, false,
	); err != nil {
		t.Fatalf("put(ordinary): %v", err)
	}
	if _, ok := transport.objects["ordinary"]; !ok {
		t.Fatal("ordinary object was not sent through S3")
	}
	if err := api.put(
		context.Background(), "atomic", strings.NewReader("body"), 4, true,
	); err != nil {
		t.Fatalf("put(atomic): %v", err)
	}
	if client.request == nil || aliyunoss.ToString(client.request.ForbidOverwrite) != "true" {
		t.Fatalf("atomic OSS request = %+v", client.request)
	}

	var unavailable *ossCreateOnlyWriter
	if err := unavailable.put(
		context.Background(), "atomic", strings.NewReader("x"), 1,
	); err == nil {
		t.Fatal("nil create-only writer error = nil")
	}
	if err := (&ossCreateOnlyWriter{client: client}).put(
		context.Background(), "atomic", strings.NewReader("x"),
		uint64(^uint64(0)>>1)+1,
	); !errors.Is(err, backupartifact.ErrInvalidObject) {
		t.Fatalf("oversized OSS write error = %v", err)
	}
	transportErr := errors.New("transport")
	if err := (&ossCreateOnlyWriter{
		client: &recordingOSSPutObjectClient{err: transportErr},
	}).put(context.Background(), "atomic", strings.NewReader("x"), 1); !errors.Is(
		err, transportErr,
	) {
		t.Fatalf("OSS transport error = %v", err)
	}
	serviceErr := &aliyunoss.ServiceError{
		StatusCode: 403, Code: "AccessDenied", Message: "denied",
		RequestID: "request-2",
	}
	err := (&ossCreateOnlyWriter{
		client: &recordingOSSPutObjectClient{err: serviceErr}, bucket: "backups",
	}).put(context.Background(), "atomic", strings.NewReader("x"), 1)
	var response minio.ErrorResponse
	if !errors.As(err, &response) || response.Code != "AccessDenied" {
		t.Fatalf("OSS service error = %#v", err)
	}
}

func newProtocolContractMinioClient(
	t *testing.T,
	transport http.RoundTripper,
) *minio.Client {
	t.Helper()
	client, err := minio.New("s3.contract.invalid", &minio.Options{
		Creds:  credentials.NewStaticV4("access", "secret", ""),
		Secure: true, Region: "us-east-1", Transport: transport,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		t.Fatalf("minio.New(): %v", err)
	}
	return client
}

type memoryS3ProtocolTransport struct {
	bucket               string
	objects              map[string][]byte
	rejectConditionalPut bool
	lastPutIfNoneMatch   string
	lastPutContentType   string
}

func (t *memoryS3ProtocolTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	objectKey := strings.TrimPrefix(request.URL.EscapedPath(), "/"+t.bucket+"/")
	decodedKey, err := url.PathUnescape(objectKey)
	if err != nil {
		return nil, err
	}
	if request.Method == http.MethodGet &&
		request.URL.Query().Get("list-type") == "2" {
		return t.listResponse(request)
	}
	switch request.Method {
	case http.MethodPut:
		t.lastPutIfNoneMatch = request.Header.Get("If-None-Match")
		t.lastPutContentType = request.Header.Get("Content-Type")
		if t.rejectConditionalPut && t.lastPutIfNoneMatch == "*" {
			return s3ProtocolErrorResponse(
				request, http.StatusPreconditionFailed, "PreconditionFailed",
			), nil
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		t.objects[decodedKey] = append([]byte(nil), body...)
		response := s3ProtocolResponse(request, http.StatusOK, nil)
		response.Header.Set("ETag", `"contract-etag"`)
		return response, nil
	case http.MethodHead:
		body, ok := t.objects[decodedKey]
		if !ok {
			return s3ProtocolErrorResponse(
				request, http.StatusNotFound, "NoSuchKey",
			), nil
		}
		response := s3ProtocolResponse(request, http.StatusOK, nil)
		setS3ProtocolObjectHeaders(response.Header, len(body))
		return response, nil
	case http.MethodGet:
		body, ok := t.objects[decodedKey]
		if !ok {
			return s3ProtocolErrorResponse(
				request, http.StatusNotFound, "NoSuchKey",
			), nil
		}
		response := s3ProtocolResponse(request, http.StatusOK, body)
		setS3ProtocolObjectHeaders(response.Header, len(body))
		return response, nil
	case http.MethodDelete:
		delete(t.objects, decodedKey)
		return s3ProtocolResponse(request, http.StatusNoContent, nil), nil
	default:
		return s3ProtocolErrorResponse(
			request, http.StatusMethodNotAllowed, "MethodNotAllowed",
		), nil
	}
}

func (t *memoryS3ProtocolTransport) listResponse(
	request *http.Request,
) (*http.Response, error) {
	prefix := request.URL.Query().Get("prefix")
	keys := make([]string, 0)
	for key := range t.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	result := s3ProtocolListResult{
		Name: t.bucket, Prefix: prefix, KeyCount: len(keys),
		MaxKeys: 1000, IsTruncated: false,
	}
	for _, key := range keys {
		result.Contents = append(result.Contents, s3ProtocolListObject{
			Key: key, LastModified: "2026-09-02T01:02:03.000Z",
			ETag: `"contract-etag"`, Size: int64(len(t.objects[key])),
			StorageClass: "STANDARD",
		})
	}
	body, err := xml.Marshal(result)
	if err != nil {
		return nil, err
	}
	return s3ProtocolResponse(request, http.StatusOK, body), nil
}

type s3ProtocolListResult struct {
	XMLName     xml.Name               `xml:"ListBucketResult"`
	Name        string                 `xml:"Name"`
	Prefix      string                 `xml:"Prefix"`
	KeyCount    int                    `xml:"KeyCount"`
	MaxKeys     int                    `xml:"MaxKeys"`
	IsTruncated bool                   `xml:"IsTruncated"`
	Contents    []s3ProtocolListObject `xml:"Contents"`
}

type s3ProtocolListObject struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

func s3ProtocolResponse(
	request *http.Request,
	status int,
	body []byte,
) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    request,
	}
}

func s3ProtocolErrorResponse(
	request *http.Request,
	status int,
	code string,
) *http.Response {
	body := []byte("<Error><Code>" + code +
		"</Code><Message>contract error</Message><RequestId>request-1</RequestId></Error>")
	response := s3ProtocolResponse(request, status, body)
	response.Header.Set("Content-Type", "application/xml")
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return response
}

func setS3ProtocolObjectHeaders(header http.Header, size int) {
	header.Set("Content-Length", strconv.Itoa(size))
	header.Set("Last-Modified", time.Date(
		2026, 9, 2, 1, 2, 3, 0, time.UTC,
	).Format(http.TimeFormat))
	header.Set("ETag", `"contract-etag"`)
}

type escapedListS3API struct{}

func (*escapedListS3API) put(
	context.Context, string, io.Reader, uint64, bool,
) error {
	return nil
}

func (*escapedListS3API) open(
	context.Context, string,
) (io.ReadCloser, s3ArchiveObject, error) {
	return nil, s3ArchiveObject{}, backupartifact.ErrObjectNotFound
}

func (*escapedListS3API) list(
	context.Context, string,
) ([]s3ArchiveObject, error) {
	return []s3ArchiveObject{{key: "outside/object", bytes: 1}}, nil
}

func (*escapedListS3API) remove(context.Context, string) error { return nil }
