//go:build integration

package backup

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestS3ArchiveStoreRoundTripAgainstCompatibleService(t *testing.T) {
	endpoint := os.Getenv("WK_TEST_S3_ENDPOINT")
	bucket := os.Getenv("WK_TEST_S3_BUCKET")
	accessKey := os.Getenv("WK_TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("WK_TEST_S3_SECRET_KEY")
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("WK_TEST_S3_* environment is not configured")
	}

	store, err := NewS3ArchiveStore(S3ArchiveStoreOptions{
		Endpoint: endpoint,
		Region:   os.Getenv("WK_TEST_S3_REGION"),
		Bucket:   bucket,
		Prefix: fmt.Sprintf(
			"wukongim-integration/%d",
			time.Now().UTC().UnixNano(),
		),
		AccessKey: accessKey,
		SecretKey: secretKey,
		PathStyle: true,
	})
	if err != nil {
		t.Fatalf("create S3 archive store: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), 15*time.Second,
		)
		defer cleanupCancel()
		if err := store.DeletePrefix(cleanupCtx, "probes"); err != nil {
			t.Errorf("clean S3 probe prefix: %v", err)
		}
	})

	const key = "probes/round-trip"
	body := []byte("wukongim S3 integration probe")
	if err := store.Put(ctx, backupartifact.PutObject{
		Key: key, Body: bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)), IfAbsent: true,
	}); err != nil {
		t.Fatalf("put S3 probe: %v", err)
	}

	reader, object, err := store.Open(ctx, key)
	if err != nil {
		t.Fatalf("open S3 probe: %v", err)
	}
	loaded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		t.Fatalf("read S3 probe: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close S3 probe: %v", closeErr)
	}
	if object.Bytes != uint64(len(body)) || !bytes.Equal(loaded, body) {
		t.Fatalf(
			"S3 probe mismatch: object_bytes=%d body=%q",
			object.Bytes, loaded,
		)
	}

	objects, err := store.List(ctx, "probes")
	if err != nil {
		t.Fatalf("list S3 probe prefix: %v", err)
	}
	if len(objects) != 1 || objects[0].Key != key {
		t.Fatalf("S3 probe listing = %#v, want one %q", objects, key)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("delete S3 probe: %v", err)
	}
	if _, _, err := store.Open(ctx, key); err == nil {
		t.Fatal("open deleted S3 probe: error = nil")
	}
}
