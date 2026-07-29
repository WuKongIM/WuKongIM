//go:build integration

package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestRepositoryProviderRoundTripAgainstOSS(t *testing.T) {
	region := strings.TrimSpace(os.Getenv("WK_TEST_OSS_REGION"))
	bucket := strings.TrimSpace(os.Getenv("WK_TEST_OSS_BUCKET"))
	accessKey := strings.TrimSpace(os.Getenv("WK_TEST_OSS_ACCESS_KEY_ID"))
	secretKey := os.Getenv("WK_TEST_OSS_ACCESS_KEY_SECRET")
	if region == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("WK_TEST_OSS_REGION, WK_TEST_OSS_BUCKET, WK_TEST_OSS_ACCESS_KEY_ID, and WK_TEST_OSS_ACCESS_KEY_SECRET are required")
	}
	provider, config := integrationRepositoryProvider(
		t,
		backupcontract.StoreKindOSS,
		strings.TrimSpace(os.Getenv("WK_TEST_OSS_ENDPOINT")),
		region,
		bucket,
		accessKey,
		secretKey,
		false,
	)
	exerciseRepositoryProviderRoundTrip(
		t,
		provider,
		config,
		"Alibaba Cloud OSS",
	)
}

func TestRepositoryProviderRoundTripAgainstCOS(t *testing.T) {
	region := strings.TrimSpace(os.Getenv("WK_TEST_COS_REGION"))
	bucket := strings.TrimSpace(os.Getenv("WK_TEST_COS_BUCKET"))
	accessKey := strings.TrimSpace(os.Getenv("WK_TEST_COS_SECRET_ID"))
	secretKey := os.Getenv("WK_TEST_COS_SECRET_KEY")
	if region == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("WK_TEST_COS_REGION, WK_TEST_COS_BUCKET, WK_TEST_COS_SECRET_ID, and WK_TEST_COS_SECRET_KEY are required")
	}
	provider, config := integrationRepositoryProvider(
		t,
		backupcontract.StoreKindCOS,
		strings.TrimSpace(os.Getenv("WK_TEST_COS_ENDPOINT")),
		region,
		bucket,
		accessKey,
		secretKey,
		false,
	)
	exerciseRepositoryProviderRoundTrip(
		t,
		provider,
		config,
		"Tencent Cloud COS",
	)
}

func integrationRepositoryProvider(
	t *testing.T,
	kind backupcontract.StoreKind,
	endpoint string,
	region string,
	bucket string,
	accessKey string,
	secretKey string,
	pathStyle bool,
) (*RepositoryProvider, backupcontract.StoreConfig) {
	t.Helper()
	cipher, err := NewCredentialCipher(
		"integration-manager-secret",
		"integration-cluster",
	)
	if err != nil {
		t.Fatal("create integration credential cipher")
	}
	ciphertext, err := cipher.Seal(ObjectStoreCredentials{
		AccessKey: accessKey,
		SecretKey: secretKey,
	})
	if err != nil {
		t.Fatal("seal integration repository credentials")
	}
	provider, err := NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatal("create integration repository provider")
	}
	return provider, backupcontract.StoreConfig{
		Kind:                 kind,
		Endpoint:             endpoint,
		Region:               region,
		Bucket:               bucket,
		Prefix:               integrationRepositoryPrefix(string(kind)),
		PathStyle:            pathStyle,
		CredentialCiphertext: ciphertext,
		CredentialRevision:   1,
	}
}

func exerciseRepositoryProviderRoundTrip(
	t *testing.T,
	provider *RepositoryProvider,
	config backupcontract.StoreConfig,
	providerName string,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := provider.Open(ctx, config)
	if err != nil {
		t.Fatalf(
			"%s open failed: %v",
			providerName,
			classifyRepositoryError(
				config.Kind,
				backupcontract.RepositoryAccessOpen,
				err,
			),
		)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(),
			30*time.Second,
		)
		defer cleanupCancel()
		if err := store.DeletePrefix(cleanupCtx, "round-trip"); err != nil {
			t.Errorf(
				"%s cleanup failed: %v",
				providerName,
				classifyRepositoryError(
					config.Kind,
					backupcontract.RepositoryAccessDelete,
					err,
				),
			)
		}
	})

	const key = "round-trip/object"
	body := []byte("wukongim object storage integration probe")
	if err := store.Put(ctx, backupartifact.PutObject{
		Key:           key,
		Body:          bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)),
		IfAbsent:      true,
	}); err != nil {
		t.Fatalf(
			"%s create-only write failed: %v (provider response: %v)",
			providerName,
			classifyRepositoryError(
				config.Kind,
				backupcontract.RepositoryAccessWriteMarker,
				err,
			),
			err,
		)
	}
	err = store.Put(ctx, backupartifact.PutObject{
		Key:           key,
		Body:          bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)),
		IfAbsent:      true,
	})
	if !errors.Is(err, backupartifact.ErrObjectExists) {
		t.Fatalf(
			"%s create-only write did not reject an existing object: %v (provider response: %v)",
			providerName,
			classifyRepositoryError(
				config.Kind,
				backupcontract.RepositoryAccessWriteMarker,
				err,
			),
			err,
		)
	}

	reader, object, err := store.Open(ctx, key)
	if err != nil {
		t.Fatalf(
			"%s read failed: %v",
			providerName,
			classifyRepositoryError(
				config.Kind,
				backupcontract.RepositoryAccessReadMarker,
				err,
			),
		)
	}
	loaded, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("%s read stream failed", providerName)
	}
	if object.Key != key ||
		object.Bytes != uint64(len(body)) ||
		!bytes.Equal(loaded, body) {
		t.Fatalf("%s read metadata or content mismatch", providerName)
	}

	objects, err := store.List(ctx, "round-trip")
	if err != nil {
		t.Fatalf(
			"%s list failed: %v",
			providerName,
			classifyRepositoryError(
				config.Kind,
				backupcontract.RepositoryAccessList,
				err,
			),
		)
	}
	if len(objects) != 1 || objects[0].Key != key {
		t.Fatalf("%s list did not return the exact integration object", providerName)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf(
			"%s delete failed: %v",
			providerName,
			classifyRepositoryError(
				config.Kind,
				backupcontract.RepositoryAccessDelete,
				err,
			),
		)
	}
	objects, err = store.List(ctx, "round-trip")
	if err != nil {
		t.Fatalf(
			"%s post-delete list failed: %v",
			providerName,
			classifyRepositoryError(
				config.Kind,
				backupcontract.RepositoryAccessList,
				err,
			),
		)
	}
	if len(objects) != 0 {
		t.Fatalf("%s deleted object is still listable", providerName)
	}
	if _, _, err := store.Open(ctx, key); !errors.Is(
		err,
		backupartifact.ErrObjectNotFound,
	) {
		t.Fatalf("%s deleted object is still readable", providerName)
	}
}

func integrationRepositoryPrefix(provider string) string {
	random := make([]byte, 8)
	if _, err := io.ReadFull(rand.Reader, random); err != nil {
		panic("backup integration random source unavailable")
	}
	return "wukongim-integration/" + provider + "/" +
		time.Now().UTC().Format("20060102T150405.000000000") + "-" +
		hex.EncodeToString(random)
}
