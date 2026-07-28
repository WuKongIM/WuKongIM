package backup

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	alicredentials "github.com/aliyun/credentials-go/credentials"
	aliproviders "github.com/aliyun/credentials-go/credentials/providers"
)

const (
	alibabaBackupConnectTimeout = 10 * time.Second
	alibabaBackupReadTimeout    = 30 * time.Second
	alibabaBackupRoleDuration   = 3600
)

type alibabaCredentialSource interface {
	GetCredential() (*alicredentials.CredentialModel, error)
}

type alibabaOSSCredentialsProvider struct {
	source alibabaCredentialSource
}

func (p *alibabaOSSCredentialsProvider) GetCredentials(
	_ context.Context,
) (osscredentials.Credentials, error) {
	if p == nil || p.source == nil {
		return osscredentials.Credentials{}, fmt.Errorf(
			"backup Alibaba credentials: source is required",
		)
	}
	model, err := p.source.GetCredential()
	if err != nil {
		return osscredentials.Credentials{}, fmt.Errorf(
			"backup Alibaba credentials: refresh: %w", err,
		)
	}
	if model == nil || strings.TrimSpace(alibabaString(model.AccessKeyId)) == "" ||
		alibabaString(model.AccessKeySecret) == "" {
		return osscredentials.Credentials{}, fmt.Errorf(
			"backup Alibaba credentials: access key pair is incomplete",
		)
	}
	return osscredentials.Credentials{
		AccessKeyID:     alibabaString(model.AccessKeyId),
		AccessKeySecret: alibabaString(model.AccessKeySecret),
		SecurityToken:   alibabaString(model.SecurityToken),
	}, nil
}

// LoadOSSRepository assumes the ordinary repository role and creates an OSS adapter.
func LoadOSSRepository(
	ctx context.Context,
	name string,
	endpoint string,
	region string,
	bucket string,
	prefix string,
	objectLockDays int,
	roleARN string,
) (*OSSRepository, error) {
	repository, err := loadOSSRepository(
		ctx, name, endpoint, region, bucket, prefix, objectLockDays,
		roleARN, "wukongim-backup-repository",
	)
	if err != nil {
		return nil, err
	}
	if err := repository.QualifyOrdinaryRoleLeastPrivilege(ctx); err != nil {
		return nil, err
	}
	return repository, nil
}

// LoadOSSGarbageRepository assumes the delete-capable garbage role.
func LoadOSSGarbageRepository(
	ctx context.Context,
	name string,
	endpoint string,
	region string,
	bucket string,
	prefix string,
	objectLockDays int,
	roleARN string,
	probeSlot uint64,
) (*OSSRepository, error) {
	if strings.TrimSpace(roleARN) == "" {
		return nil, fmt.Errorf(
			"backup OSS repository: garbage collector role ARN is required",
		)
	}
	if probeSlot == 0 {
		return nil, fmt.Errorf(
			"backup OSS repository: garbage-role probe slot is required",
		)
	}
	repository, err := loadOSSRepository(
		ctx, name, endpoint, region, bucket, prefix, objectLockDays,
		roleARN, "wukongim-backup-garbage-collector",
	)
	if err != nil {
		return nil, err
	}
	if err := repository.QualifyGarbageAccess(
		ctx, fmt.Sprintf("%016x", probeSlot),
	); err != nil {
		return nil, err
	}
	if err := repository.QualifyGarbageRoleLeastPrivilege(ctx); err != nil {
		return nil, err
	}
	return repository, nil
}

// LoadOSSRepairRepository assumes the separately authorized auditor role.
func LoadOSSRepairRepository(
	ctx context.Context,
	repository *OSSRepository,
	endpoint string,
	region string,
	roleARN string,
) (*OSSRepairRepository, error) {
	if repository == nil {
		return nil, fmt.Errorf("backup OSS repair repository: repository is required")
	}
	client, err := loadAlibabaOSSClient(
		ctx, endpoint, region, roleARN,
		"wukongim-backup-integrity-auditor",
	)
	if err != nil {
		return nil, err
	}
	repair, err := NewOSSRepairRepository(OSSRepairRepositoryOptions{
		Repository: repository,
		Client:     client,
	})
	if err != nil {
		return nil, err
	}
	if err := repair.QualifyRepairRoleLeastPrivilege(ctx); err != nil {
		return nil, err
	}
	return repair, nil
}

func loadOSSRepository(
	ctx context.Context,
	name string,
	endpoint string,
	region string,
	bucket string,
	prefix string,
	objectLockDays int,
	roleARN string,
	sessionName string,
) (*OSSRepository, error) {
	if strings.TrimSpace(roleARN) == "" {
		return nil, fmt.Errorf(
			"backup OSS repository: ordinary access role ARN is required",
		)
	}
	client, err := loadAlibabaOSSClient(
		ctx, endpoint, region, roleARN, sessionName,
	)
	if err != nil {
		return nil, err
	}
	return NewOSSRepository(OSSRepositoryOptions{
		Name:           name,
		Bucket:         bucket,
		Prefix:         prefix,
		ObjectLockDays: objectLockDays,
		Client:         client,
	})
}

func loadAlibabaOSSClient(
	ctx context.Context,
	endpoint string,
	region string,
	roleARN string,
	sessionName string,
) (*oss.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint = strings.TrimSpace(endpoint)
	region = strings.TrimSpace(region)
	if _, err := parseAlibabaBackupEndpoint(endpoint); err != nil || region == "" {
		return nil, fmt.Errorf(
			"backup OSS repository: HTTPS endpoint and region are required",
		)
	}
	credential, err := loadAlibabaRoleCredential(roleARN, sessionName)
	if err != nil {
		return nil, fmt.Errorf("backup OSS repository: %w", err)
	}
	config := oss.LoadDefaultConfig().
		WithRegion(region).
		WithEndpoint(endpoint).
		WithCredentialsProvider(&alibabaOSSCredentialsProvider{source: credential}).
		WithConnectTimeout(alibabaBackupConnectTimeout).
		WithReadWriteTimeout(alibabaBackupReadTimeout)
	return oss.NewClient(config), nil
}

func loadAlibabaRoleCredential(
	roleARN string,
	sessionName string,
) (alicredentials.Credential, error) {
	roleARN = strings.TrimSpace(roleARN)
	sessionName = strings.TrimSpace(sessionName)
	if roleARN == "" || sessionName == "" {
		return nil, fmt.Errorf("Alibaba RAM role ARN and session name are required")
	}
	base := aliproviders.NewDefaultCredentialsProvider()
	roleProvider, err := aliproviders.NewRAMRoleARNCredentialsProviderBuilder().
		WithCredentialsProvider(base).
		WithRoleArn(roleARN).
		WithRoleSessionName(sessionName).
		WithDurationSeconds(alibabaBackupRoleDuration).
		WithHttpOptions(&aliproviders.HttpOptions{
			ConnectTimeout: int(alibabaBackupConnectTimeout.Milliseconds()),
			ReadTimeout:    int(alibabaBackupReadTimeout.Milliseconds()),
		}).
		Build()
	if err != nil {
		return nil, fmt.Errorf("create Alibaba RAM role credentials: %w", err)
	}
	// Resolve once so startup fails before any backup runtime is scheduled. The
	// provider remains chained to the refreshable default source for long-lived
	// ECS, RRSA, URI, profile, or environment credentials.
	resolved, err := roleProvider.GetCredentials()
	if err != nil {
		return nil, fmt.Errorf("assume Alibaba RAM role: %w", err)
	}
	if resolved == nil || strings.TrimSpace(resolved.AccessKeyId) == "" ||
		resolved.AccessKeySecret == "" {
		return nil, fmt.Errorf("assumed Alibaba RAM role credentials are incomplete")
	}
	return alicredentials.FromCredentialsProvider("ram_role_arn", roleProvider), nil
}

func parseAlibabaBackupEndpoint(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf(
			"endpoint must be an HTTPS origin without credentials, query, or fragment",
		)
	}
	return parsed, nil
}

func alibabaString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
