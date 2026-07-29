package backup

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

func TestRepositoryProviderForcesCloudVirtualHostAddressing(t *testing.T) {
	cipher, err := NewCredentialCipher(
		"manager-installation-secret", "cluster-a",
	)
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	provider, err := NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	credential, err := provider.SealObjectStoreCredentials(
		"access-key", "secret-key",
	)
	if err != nil {
		t.Fatalf("SealObjectStoreCredentials(): %v", err)
	}
	testCases := []struct {
		name     string
		kind     backupcontract.StoreKind
		endpoint string
		region   string
		bucket   string
		wantHost string
	}{
		{
			name:     "Alibaba OSS",
			kind:     backupcontract.StoreKindOSS,
			region:   "cn-hangzhou",
			bucket:   "wukongim-backups",
			wantHost: "wukongim-backups.s3.oss-cn-hangzhou.aliyuncs.com",
		},
		{
			name:     "Tencent COS",
			kind:     backupcontract.StoreKindCOS,
			region:   "ap-shanghai",
			bucket:   "wukongim-backups-1250000000",
			wantHost: "wukongim-backups-1250000000.cos.ap-shanghai.myqcloud.com",
		},
		{
			name:     "Alibaba OSS internal endpoint override",
			kind:     backupcontract.StoreKindOSS,
			endpoint: "https://oss-cn-hangzhou-internal.aliyuncs.com",
			region:   "cn-hangzhou",
			bucket:   "wukongim-backups",
			wantHost: "wukongim-backups.oss-cn-hangzhou-internal.aliyuncs.com",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			store, err := provider.Open(
				context.Background(),
				backupcontract.StoreConfig{
					Kind: testCase.kind, Endpoint: testCase.endpoint,
					Region:               testCase.region,
					Bucket:               testCase.bucket,
					Prefix:               "cluster-a",
					CredentialCiphertext: credential,
				},
			)
			if err != nil {
				t.Fatalf("Open(): %v", err)
			}
			s3Store, ok := store.(*S3ArchiveStore)
			if !ok {
				t.Fatalf("store type = %T", store)
			}
			var api *minioArchiveAPI
			switch value := s3Store.api.(type) {
			case *minioArchiveAPI:
				api = value
			case *ossArchiveAPI:
				api = value.minioArchiveAPI
			default:
				t.Fatalf("api type = %T", s3Store.api)
			}
			objectURL, err := api.client.PresignedGetObject(
				context.Background(),
				testCase.bucket,
				"probes/provider",
				time.Minute,
				url.Values{},
			)
			if err != nil {
				t.Fatalf("PresignedGetObject(): %v", err)
			}
			if !strings.EqualFold(objectURL.Host, testCase.wantHost) {
				t.Fatalf("host = %q, want %q", objectURL.Host, testCase.wantHost)
			}
		})
	}
}
