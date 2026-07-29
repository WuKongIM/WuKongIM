//go:build integration

package backup

import (
	"os"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

func TestS3ArchiveStoreRoundTripAgainstCompatibleService(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("WK_TEST_S3_ENDPOINT"))
	bucket := strings.TrimSpace(os.Getenv("WK_TEST_S3_BUCKET"))
	accessKey := strings.TrimSpace(os.Getenv("WK_TEST_S3_ACCESS_KEY"))
	secretKey := os.Getenv("WK_TEST_S3_SECRET_KEY")
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		t.Skip("WK_TEST_S3_ENDPOINT, WK_TEST_S3_BUCKET, WK_TEST_S3_ACCESS_KEY, and WK_TEST_S3_SECRET_KEY are required")
	}
	provider, config := integrationRepositoryProvider(
		t,
		backupcontract.StoreKindS3,
		endpoint,
		strings.TrimSpace(os.Getenv("WK_TEST_S3_REGION")),
		bucket,
		accessKey,
		secretKey,
		true,
	)
	exerciseRepositoryProviderRoundTrip(
		t,
		provider,
		config,
		"S3-compatible storage",
	)
}
