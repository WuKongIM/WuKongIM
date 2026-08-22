package alibaba

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	quotas "github.com/alibabacloud-go/quotas-20200510/v2/client"
	ram "github.com/alibabacloud-go/ram-20150501/v2/client"
	sts "github.com/alibabacloud-go/sts-20150401/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
)

const (
	discoveryPageSize              = 100
	maxDiscoveryPages              = 20
	defaultConnectTimeoutMillis    = 10_000
	defaultReadTimeoutMillis       = 30_000
	maxPostPaidVCPUAttribute       = "max-postpaid-instance-vcpu-count"
	usedPostPaidVCPUAttribute      = "used-postpaid-instance-vcpu-count"
	credentialAccessKeyIDEnv       = "ALIBABA_CLOUD_ACCESS_KEY_ID"
	credentialAccessKeySecretEnv   = "ALIBABA_CLOUD_ACCESS_KEY_SECRET"
	credentialSecurityTokenEnv     = "ALIBABA_CLOUD_SECURITY_TOKEN"
	cloudShellAuthorizationEnv     = "WK_ALIBABA_CLOUD_SHELL_EPHEMERAL_AUTHORIZATION"
	cloudShellAuthorizationValue   = "unregistered-one-hour-cloud-shell"
	lifecycleAuthorizationEnv      = "WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION"
	lifecycleAuthorizationValue    = "create-and-delete-paid-cloud-lease"
	officialUbuntuImageNamePattern = "ubuntu_24_04_x64_20G_alibase*"
	eipQuotaProductCode            = "eip"
	eipQuotaName                   = "eip_quota_instances_num"
	eipQuotaCategory               = "CommonQuota"
	mutationPermissionProbeID      = "i-wukongim-readonly-permission-probe"
	availabilityWithStock          = "with_stock"
	availabilityStatusOnly         = "available_status_only"
	availabilityCategoryOnly       = "with_stock_category_only"
	availabilityEmptyZones         = "empty_zones"
	availabilityZoneNotReturned    = "zone_not_returned"
	availabilityZoneStatusMissing  = "zone_status_missing"
	availabilityZoneWithoutStock   = "zone_not_with_stock"
	availabilityResourceMissing    = "resource_not_returned"
	availabilityValueMissing       = "supported_value_not_returned"
	availabilityStatusMissing      = "supported_status_missing"
	availabilityCategoryMissing    = "supported_category_missing"
	availabilityBothStatusMissing  = "supported_status_and_category_missing"
	availabilityWithoutStock       = "supported_not_with_stock"
	availabilityRangeMissing       = "disk_range_missing"
	availabilityRangeNotCovered    = "disk_range_not_covered"
)

// OpenAPI is the production Alibaba SDK boundary. Lifecycle methods remain
// unreachable unless constructed through the explicit paid-mutation path.
type OpenAPI struct {
	// region is the single reviewed provider discovery boundary.
	region string
	// ecs serves inventory, price, capacity, quota, and permission-probe calls.
	ecs *ecs.Client
	// sts verifies the exact temporary caller identity.
	sts *sts.Client
	// quotas proves EIP headroom and the retention-fee waiver precondition.
	quotas *quotas.Client
	// ram is used only by tagged integration checks to prove the exact attached policy.
	ram *ram.Client
	// vpc serves lifecycle VPC, vSwitch, and EIP operations; Quote never uses it.
	vpc *vpc.Client
	// lifecycleAuthorized is true only through the explicit paid-mutation constructor.
	lifecycleAuthorized bool
	// cloudShellAuthorized records the separately verified one-hour account identity shape.
	cloudShellAuthorized bool
}

var _ ReadAPI = (*OpenAPI)(nil)

// NewOpenAPIFromOIDCEnvironment creates read-only SDK clients from temporary
// role credentials or an explicitly verified one-hour Cloud Shell credential.
// An ordinary tokenless AccessKey remains rejected.
func NewOpenAPIFromOIDCEnvironment(region string) (*OpenAPI, error) {
	return newOpenAPIFromOIDCEnvironment(region, false)
}

// NewLifecycleOpenAPIFromOIDCEnvironment creates the live mutation boundary
// only when the job carries the exact explicit paid-cloud authorization value.
func NewLifecycleOpenAPIFromOIDCEnvironment(region string) (*OpenAPI, error) {
	if os.Getenv(lifecycleAuthorizationEnv) != lifecycleAuthorizationValue {
		return nil, fmt.Errorf("%w: explicit Alibaba lifecycle mutation authorization is required", ErrInvalidConfig)
	}
	return newOpenAPIFromOIDCEnvironment(region, true)
}

// NewInventoryOpenAPIFromOIDCEnvironment creates the provider inventory
// boundary without accepting the explicit paid-mutation authorization value.
// It must be paired with the read-only Observer RAM role and a read-only CLI.
func NewInventoryOpenAPIFromOIDCEnvironment(region string) (*OpenAPI, error) {
	return newOpenAPIFromOIDCEnvironment(region, false)
}

func newOpenAPIFromOIDCEnvironment(region string, lifecycleAuthorized bool) (*OpenAPI, error) {
	accessKeyID := strings.TrimSpace(os.Getenv(credentialAccessKeyIDEnv))
	accessKeySecret := os.Getenv(credentialAccessKeySecretEnv)
	securityToken := strings.TrimSpace(os.Getenv(credentialSecurityTokenEnv))
	cloudShellAuthorized := os.Getenv(cloudShellAuthorizationEnv) == cloudShellAuthorizationValue
	if region != RegionHangzhou || accessKeyID == "" || accessKeySecret == "" || (securityToken == "" && !cloudShellAuthorized) {
		return nil, fmt.Errorf("%w: temporary Alibaba role credentials for %s are required", ErrInvalidConfig, RegionHangzhou)
	}
	config := (&openapiutil.Config{}).
		SetAccessKeyId(accessKeyID).
		SetAccessKeySecret(accessKeySecret).
		SetRegionId(region).
		SetConnectTimeout(defaultConnectTimeoutMillis).
		SetReadTimeout(defaultReadTimeoutMillis)
	if securityToken != "" {
		config.SetSecurityToken(securityToken)
	}
	ecsClient, err := ecs.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("%w: create ECS client: %v", ErrInvalidConfig, err)
	}
	stsClient, err := sts.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("%w: create STS client: %v", ErrInvalidConfig, err)
	}
	quotaClient, err := quotas.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("%w: create Quota Center client: %v", ErrInvalidConfig, err)
	}
	ramClient, err := ram.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("%w: create RAM client: %v", ErrInvalidConfig, err)
	}
	vpcClient, err := vpc.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("%w: create VPC client: %v", ErrInvalidConfig, err)
	}
	return &OpenAPI{
		region: region, ecs: ecsClient, sts: stsClient, quotas: quotaClient,
		ram: ramClient, vpc: vpcClient, lifecycleAuthorized: lifecycleAuthorized,
		cloudShellAuthorized: cloudShellAuthorized,
	}, nil
}

// RequiredQuoteActions is the exact read-only RAM action set used by OpenAPI.
func RequiredQuoteActions() []string {
	return []string{
		"ecs:DescribeAccountAttributes",
		"ecs:DescribeAvailableResource",
		"ecs:DescribeImages",
		"ecs:DescribeInstanceTypes",
		"ecs:DescribePrice",
		"ecs:DescribeZones",
		"quotas:ListProductQuotas",
		"ram:GetPolicyVersion",
		"ram:ListPoliciesForRole",
		"sts:GetCallerIdentity",
	}
}

// RequiredLifecycleObserveActions is the exact non-mutating inventory set used
// by Inspect and the discovery phase of Release and Sweep.
func RequiredLifecycleObserveActions() []string {
	return []string{
		"ecs:DescribeDisks",
		"ecs:DescribeInstances",
		"ecs:DescribeNetworkInterfaces",
		"ecs:DescribeSecurityGroupAttribute",
		"ecs:DescribeSecurityGroups",
		"sts:GetCallerIdentity",
		"vpc:DescribeEipAddresses",
		"vpc:DescribeNatGateways",
		"vpc:DescribeRouteEntryList",
		"vpc:DescribeRouteTableList",
		"vpc:DescribeVSwitches",
		"vpc:DescribeVpcs",
	}
}

// RequiredBillingObserveActions is the exact delayed-billing read set reserved
// for the Observer role. Billing APIs support only account-wide resources.
func RequiredBillingObserveActions() []string {
	return []string{
		"bssapi:QueryBill",
		"bssapi:QueryBillOverview",
		"bssapi:QueryInstanceBill",
	}
}

// RequiredLifecycleProvisionActions adds only the creation and state-tagging
// calls consumed by Acquire to the observe set.
func RequiredLifecycleProvisionActions() []string {
	return append(RequiredLifecycleObserveActions(),
		"ecs:AuthorizeSecurityGroup",
		"ecs:CreateSecurityGroup",
		"ecs:RunInstances",
		"ecs:TagResources",
		"vpc:AllocateEipAddress",
		"vpc:AssociateEipAddress",
		"vpc:CreateVSwitch",
		"vpc:CreateVpc",
		"vpc:DescribeVSwitchAttributes",
		"vpc:DescribeVpcAttribute",
		"vpc:TagResources",
	)
}

// RequiredLifecycleReleaseActions adds only the revocation and deletion calls
// consumed by Release and Sweep to the observe set.
func RequiredLifecycleReleaseActions() []string {
	return append(RequiredLifecycleObserveActions(),
		"ecs:DeleteDisk",
		"ecs:DeleteInstance",
		"ecs:DeleteNetworkInterface",
		"ecs:DeleteSecurityGroup",
		"ecs:DetachDisk",
		"ecs:RevokeSecurityGroup",
		"ecs:TagResources",
		"vpc:DeleteNatGateway",
		"vpc:DeleteRouteEntry",
		"vpc:DeleteVSwitch",
		"vpc:DeleteVpc",
		"vpc:ReleaseEipAddress",
		"vpc:TagResources",
		"vpc:UnassociateEipAddress",
	)
}

// QuoteRolePolicyDocument renders the only policy document accepted by the
// read-only OIDC integration check and later bootstrap wiring.
func QuoteRolePolicyDocument() string {
	document := map[string]any{
		"Version": "1",
		"Statement": []any{map[string]any{
			"Effect": "Allow", "Action": RequiredQuoteActions(), "Resource": []string{"*"},
		}},
	}
	data, _ := json.Marshal(document)
	return string(data)
}

// AccountIDHash verifies the temporary caller without exposing its account ID.
func (a *OpenAPI) AccountIDHash(ctx context.Context) (string, error) {
	accountID, _, err := a.callerIdentity(ctx)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(accountID))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// CallerPrincipalARN returns the temporary principal for integration-time
// role binding checks. Quote never exposes this value.
func (a *OpenAPI) CallerPrincipalARN(ctx context.Context) (string, error) {
	_, arn, err := a.callerIdentity(ctx)
	return arn, err
}

func (a *OpenAPI) callerIdentity(ctx context.Context) (string, string, error) {
	if a == nil || a.sts == nil {
		return "", "", ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	response, err := a.sts.GetCallerIdentity()
	if err != nil || response == nil || response.Body == nil {
		return "", "", discoveryError("GetCallerIdentity", err)
	}
	accountID := strings.TrimSpace(stringValue(response.Body.AccountId))
	arn := strings.TrimSpace(stringValue(response.Body.Arn))
	if !validCallerIdentity(accountID, arn, stringValue(response.Body.IdentityType),
		stringValue(response.Body.RoleId), a.cloudShellAuthorized) {
		return "", "", discoveryError("GetCallerIdentity incomplete", nil)
	}
	return accountID, arn, nil
}

func validCallerIdentity(accountID, arn, identityType, roleID string, cloudShellAuthorized bool) bool {
	accountID = strings.TrimSpace(accountID)
	arn = strings.TrimSpace(arn)
	identityType = strings.TrimSpace(identityType)
	roleID = strings.TrimSpace(roleID)
	if accountID == "" || arn == "" {
		return false
	}
	if cloudShellAuthorized {
		return identityType == "Account" && roleID == "" && arn == "acs:ram::"+accountID+":root"
	}
	return identityType == "AssumedRoleUser" && roleID != ""
}

func principalHasRole(arn, expectedRole string) bool {
	arn = strings.TrimSpace(arn)
	expectedRole = strings.TrimSpace(expectedRole)
	if arn == "" || expectedRole == "" || strings.Contains(expectedRole, "/") {
		return false
	}
	for _, marker := range []string{":assumed-role/", ":role/"} {
		index := strings.Index(arn, marker)
		if index < 0 {
			continue
		}
		roleAndSession := arn[index+len(marker):]
		role, _, _ := strings.Cut(roleAndSession, "/")
		// Alibaba canonicalizes RAM role names to lowercase in ARNs even when
		// GetRole returns the original display casing.
		return strings.EqualFold(role, expectedRole)
	}
	return false
}

// AssertCallerRole proves that the current temporary credentials belong to one
// exact assumed role rather than accepting a role-name substring.
func (a *OpenAPI) AssertCallerRole(ctx context.Context, expectedRole string) error {
	arn, err := a.CallerPrincipalARN(ctx)
	if err != nil {
		return err
	}
	if !principalHasRole(arn, expectedRole) {
		return discoveryError("GetCallerIdentity unexpected role", nil)
	}
	return nil
}

// AssertExactRoleTrust proves the current role retains the complete expected
// workflow trust and one-hour session bound, not merely the setup subject.
func (a *OpenAPI) AssertExactRoleTrust(ctx context.Context, roleName, expectedTrust string) error {
	roleName = strings.TrimSpace(roleName)
	expectedTrust = normalizeRAMPolicyDocument(expectedTrust)
	if a == nil || a.ram == nil || roleName == "" || expectedTrust == "" {
		return ErrInvalidConfig
	}
	response, err := a.ram.GetRoleWithContext(ctx, (&ram.GetRoleRequest{}).SetRoleName(roleName), &dara.RuntimeOptions{})
	if err != nil || response == nil || response.Body == nil || response.Body.Role == nil {
		return discoveryError("GetRole failed", err)
	}
	role := response.Body.Role
	if stringValue(role.RoleName) != roleName || dara.Int64Value(role.MaxSessionDuration) != 3600 ||
		normalizeRAMPolicyDocument(stringValue(role.AssumeRolePolicyDocument)) != expectedTrust {
		return discoveryError("GetRole unexpected trust", nil)
	}
	return nil
}

// Zones lists current PostPaid availability zones that advertise ESSD support.
func (a *OpenAPI) Zones(ctx context.Context, region string) ([]Zone, error) {
	if !a.validRegion(region) {
		return nil, ErrInvalidConfig
	}
	response, err := a.ecs.DescribeZonesWithContext(ctx, (&ecs.DescribeZonesRequest{}).
		SetRegionId(region).
		SetInstanceChargeType(providerBillingPostPaid).
		SetVerbose(true), &dara.RuntimeOptions{})
	if err != nil || response == nil || response.Body == nil || response.Body.Zones == nil {
		return nil, discoveryError("DescribeZones", err)
	}
	zones := make([]Zone, 0, len(response.Body.Zones.Zone))
	for _, item := range response.Body.Zones.Zone {
		if item == nil || item.AvailableDiskCategories == nil ||
			(item.ZoneType != nil && stringValue(item.ZoneType) != "AvailabilityZone") {
			continue
		}
		supportsESSD := false
		for _, category := range item.AvailableDiskCategories.DiskCategories {
			if stringValue(category) == providerDiskESSD {
				supportsESSD = true
				break
			}
		}
		zones = append(zones, Zone{ID: stringValue(item.ZoneId), SupportsESSD: supportsESSD})
	}
	return zones, nil
}

// InstanceTypes returns every page of exact CPU and memory candidates. The
// Provider independently enforces architecture, GPU, and burstable filtering.
func (a *OpenAPI) InstanceTypes(ctx context.Context, region string, vcpus int, memoryBytes int64) ([]InstanceType, error) {
	if !a.validRegion(region) || vcpus <= 0 || memoryBytes <= 0 || memoryBytes%bytesPerGiB != 0 ||
		vcpus > math.MaxInt32 || memoryBytes/bytesPerGiB > math.MaxInt32 {
		return nil, ErrInvalidConfig
	}
	memoryGiB := float32(memoryBytes / bytesPerGiB)
	return discoverInstanceTypes(ctx, func(ctx context.Context, token string) ([]InstanceType, string, error) {
		request := (&ecs.DescribeInstanceTypesRequest{}).
			SetCpuArchitecture("X86").
			SetMinimumCpuCoreCount(int32(vcpus)).
			SetMaximumCpuCoreCount(int32(vcpus)).
			SetMinimumMemorySize(memoryGiB).
			SetMaximumMemorySize(memoryGiB).
			SetMaxResults(discoveryPageSize)
		if token != "" {
			request.SetNextToken(token)
		}
		response, err := a.ecs.DescribeInstanceTypesWithContext(ctx, request, &dara.RuntimeOptions{})
		if err != nil || response == nil || response.Body == nil || response.Body.InstanceTypes == nil {
			return nil, "", discoveryError("DescribeInstanceTypes", err)
		}
		items := make([]InstanceType, 0, len(response.Body.InstanceTypes.InstanceType))
		for _, item := range response.Body.InstanceTypes.InstanceType {
			if item == nil {
				continue
			}
			memory := int64(0)
			if item.MemorySize != nil && *item.MemorySize > 0 && float64(*item.MemorySize) <= float64(math.MaxInt64)/float64(bytesPerGiB) {
				memory = int64(math.Round(float64(*item.MemorySize) * float64(bytesPerGiB)))
			}
			items = append(items, InstanceType{
				ID: stringValue(item.InstanceTypeId), Architecture: stringValue(item.CpuArchitecture),
				VCPUs: int(int32Value(item.CpuCoreCount)), MemoryBytes: memory,
				GPUCount: int(int32Value(item.GPUAmount)), FamilyLevel: stringValue(item.InstanceFamilyLevel),
			})
		}
		return items, strings.TrimSpace(stringValue(response.Body.NextToken)), nil
	})
}

type instanceTypePageFetcher func(context.Context, string) ([]InstanceType, string, error)

func discoverInstanceTypes(ctx context.Context, fetch instanceTypePageFetcher) ([]InstanceType, error) {
	if fetch == nil {
		return nil, ErrDiscoveryUnavailable
	}
	result := make([]InstanceType, 0, discoveryPageSize)
	seenTokens := make(map[string]struct{}, maxDiscoveryPages)
	token := ""
	for page := 0; page < maxDiscoveryPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, nextToken, err := fetch(ctx, token)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		nextToken = strings.TrimSpace(nextToken)
		if nextToken == "" {
			return result, nil
		}
		if nextToken == token {
			return nil, discoveryError("DescribeInstanceTypes repeated token", nil)
		}
		if _, exists := seenTokens[nextToken]; exists {
			return nil, discoveryError("DescribeInstanceTypes token cycle", nil)
		}
		seenTokens[nextToken] = struct{}{}
		token = nextToken
	}
	return nil, discoveryError("DescribeInstanceTypes page limit", nil)
}

// Images returns all current system-owned cloud-init x86 Ubuntu 24.04 image
// candidates compatible with one exact instance type.
func (a *OpenAPI) Images(ctx context.Context, region, instanceType string) ([]Image, error) {
	if !a.validRegion(region) || strings.TrimSpace(instanceType) == "" {
		return nil, ErrInvalidConfig
	}
	return discoverImages(ctx, func(ctx context.Context, page int32) ([]Image, int32, error) {
		response, err := a.ecs.DescribeImagesWithContext(ctx, (&ecs.DescribeImagesRequest{}).
			SetRegionId(region).
			SetInstanceType(instanceType).
			SetImageOwnerAlias("system").
			SetImageName(officialUbuntuImageNamePattern).
			SetArchitecture(providerArchitectureX86).
			SetOSType("linux").
			SetStatus("Available").
			SetIsSupportCloudinit(true).
			SetPageNumber(page).
			SetPageSize(discoveryPageSize), &dara.RuntimeOptions{})
		if err != nil || response == nil || response.Body == nil || response.Body.Images == nil || response.Body.TotalCount == nil {
			return nil, 0, discoveryError("DescribeImages", err)
		}
		images := make([]Image, 0, len(response.Body.Images.Image))
		for _, item := range response.Body.Images.Image {
			if item == nil {
				continue
			}
			createdAt, parseErr := time.Parse(time.RFC3339, stringValue(item.CreationTime))
			if parseErr != nil {
				return nil, 0, discoveryError("DescribeImages creation time", parseErr)
			}
			images = append(images, Image{
				ID: stringValue(item.ImageId), CreationTime: createdAt.UTC(),
				Official:  stringValue(item.ImageOwnerAlias) == "system",
				CloudInit: boolValue(item.IsSupportCloudinit), Architecture: stringValue(item.Architecture),
			})
		}
		return images, int32Value(response.Body.TotalCount), nil
	})
}

type imagePageFetcher func(context.Context, int32) ([]Image, int32, error)

func discoverImages(ctx context.Context, fetch imagePageFetcher) ([]Image, error) {
	if fetch == nil {
		return nil, ErrDiscoveryUnavailable
	}
	result := make([]Image, 0, discoveryPageSize)
	seenIDs := make(map[string]struct{}, discoveryPageSize)
	expectedTotal := int32(-1)
	for page := int32(1); page <= maxDiscoveryPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, total, err := fetch(ctx, page)
		if err != nil {
			return nil, err
		}
		if total < 0 || expectedTotal >= 0 && total != expectedTotal || total == 0 && len(items) != 0 {
			return nil, discoveryError("DescribeImages inconsistent total", nil)
		}
		if expectedTotal < 0 {
			expectedTotal = total
		}
		for _, item := range items {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				return nil, discoveryError("DescribeImages missing image ID", nil)
			}
			if _, exists := seenIDs[id]; exists {
				return nil, discoveryError("DescribeImages duplicate image ID", nil)
			}
			seenIDs[id] = struct{}{}
			result = append(result, item)
		}
		if len(result) == int(expectedTotal) {
			return result, nil
		}
		if len(result) > int(expectedTotal) || len(items) == 0 {
			return nil, discoveryError("DescribeImages incomplete pagination", nil)
		}
	}
	return nil, discoveryError("DescribeImages page limit", nil)
}

// Availability checks the instance type and both ESSD attachment categories
// with regular PostPaid (NoSpot) inventory requests.
func (a *OpenAPI) Availability(ctx context.Context, request AvailabilityRequest) (Availability, error) {
	if !a.validRegion(request.Region) || strings.TrimSpace(request.Zone) == "" ||
		strings.TrimSpace(request.InstanceType) == "" || request.HostCount <= 0 ||
		!validDiskSizes(request.SystemDiskSizesGiB, maxSystemDiskGiB) ||
		!validDiskSizes(request.DataDiskSizesGiB, maxDataDiskGiB) {
		return Availability{}, ErrInvalidConfig
	}
	instance, instanceReason, err := a.resourceAvailable(ctx, request, "InstanceType", request.InstanceType, nil)
	if err != nil {
		return Availability{}, err
	}
	systemDisk, systemDiskReason, err := a.resourceAvailable(ctx, request, "SystemDisk", providerDiskESSD, request.SystemDiskSizesGiB)
	if err != nil {
		return Availability{}, err
	}
	dataDisk, dataDiskReason, err := a.resourceAvailable(ctx, request, "DataDisk", providerDiskESSD, request.DataDiskSizesGiB)
	if err != nil {
		return Availability{}, err
	}
	return Availability{
		Instance: instance, InstanceReason: instanceReason,
		SystemESSDPL0: systemDisk, SystemESSDPL0Reason: systemDiskReason,
		DataESSDPL0: dataDisk, DataESSDPL0Reason: dataDiskReason,
	}, nil
}

func validDiskSizes(sizesGiB []int, maximumGiB int) bool {
	if len(sizesGiB) == 0 {
		return false
	}
	for _, sizeGiB := range sizesGiB {
		if sizeGiB <= 0 || sizeGiB > maximumGiB {
			return false
		}
	}
	return true
}

func (a *OpenAPI) resourceAvailable(ctx context.Context, request AvailabilityRequest, destination, expected string, sizesGiB []int) (bool, string, error) {
	providerRequest := availabilityProviderRequest(request, destination)
	response, err := a.ecs.DescribeAvailableResourceWithContext(ctx, providerRequest, &dara.RuntimeOptions{})
	if err != nil || response == nil {
		return false, "", discoveryError("DescribeAvailableResource "+destination, err)
	}
	return resourceAvailableFromBody(response.Body, request, destination, expected, sizesGiB)
}

func availabilityProviderRequest(request AvailabilityRequest, destination string) *ecs.DescribeAvailableResourceRequest {
	providerRequest := (&ecs.DescribeAvailableResourceRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.Zone).
		SetDestinationResource(destination).
		SetResourceType("instance").
		SetInstanceChargeType(providerBillingPostPaid).
		SetSpotStrategy(providerSpotNoSpot).
		SetInstanceType(request.InstanceType).
		SetIoOptimized("optimized").
		SetNetworkCategory("vpc")
	switch destination {
	case "SystemDisk":
		providerRequest.SetSystemDiskCategory(providerDiskESSD)
	case "DataDisk":
		providerRequest.SetSystemDiskCategory(providerDiskESSD)
		providerRequest.SetDataDiskCategory(providerDiskESSD)
	}
	return providerRequest
}

func resourceAvailableFromBody(body *ecs.DescribeAvailableResourceResponseBody, request AvailabilityRequest, destination, expected string, sizesGiB []int) (bool, string, error) {
	if body == nil {
		return false, "", discoveryError("DescribeAvailableResource "+destination+" body", nil)
	}
	// Alibaba omits the AvailableZones wrapper when a successful exact query
	// has no matching inventory. That is authoritative unavailability for this
	// offer, not loss of discovery evidence for the remaining candidates.
	if body.AvailableZones == nil {
		return false, availabilityEmptyZones, nil
	}
	zoneFound := false
	zoneStatusMissing := false
	zoneWithoutStock := false
	resourceFound := false
	valueFound := false
	statusMissing := false
	categoryMissing := false
	bothStatusMissing := false
	withoutStock := false
	rangeMissing := false
	rangeNotCovered := false
	for _, zone := range body.AvailableZones.AvailableZone {
		if zone == nil || stringValue(zone.ZoneId) != request.Zone {
			continue
		}
		zoneFound = true
		if stringValue(zone.Status) == "" || stringValue(zone.StatusCategory) == "" {
			zoneStatusMissing = true
			continue
		}
		if !stockStatusAvailable(stringValue(zone.Status), stringValue(zone.StatusCategory)) {
			zoneWithoutStock = true
			continue
		}
		if zone.AvailableResources == nil {
			continue
		}
		for _, resource := range zone.AvailableResources.AvailableResource {
			if resource == nil || stringValue(resource.Type) != destination {
				continue
			}
			resourceFound = true
			if resource.SupportedResources == nil {
				continue
			}
			for _, supported := range resource.SupportedResources.SupportedResource {
				if supported == nil || stringValue(supported.Value) != expected {
					continue
				}
				valueFound = true
				reason := supportedResourceAvailabilityReason(supported, sizesGiB)
				switch reason {
				case availabilityWithStock, availabilityStatusOnly, availabilityCategoryOnly:
					return true, reason, nil
				case availabilityStatusMissing:
					statusMissing = true
				case availabilityCategoryMissing:
					categoryMissing = true
				case availabilityBothStatusMissing:
					bothStatusMissing = true
				case availabilityWithoutStock:
					withoutStock = true
				case availabilityRangeMissing:
					rangeMissing = true
				case availabilityRangeNotCovered:
					rangeNotCovered = true
				}
			}
		}
	}
	switch {
	case !zoneFound:
		return false, availabilityZoneNotReturned, nil
	case zoneStatusMissing:
		return false, availabilityZoneStatusMissing, nil
	case zoneWithoutStock:
		return false, availabilityZoneWithoutStock, nil
	case !resourceFound:
		return false, availabilityResourceMissing, nil
	case !valueFound:
		return false, availabilityValueMissing, nil
	case rangeNotCovered:
		return false, availabilityRangeNotCovered, nil
	case rangeMissing:
		return false, availabilityRangeMissing, nil
	case bothStatusMissing:
		return false, availabilityBothStatusMissing, nil
	case categoryMissing:
		return false, availabilityCategoryMissing, nil
	case statusMissing:
		return false, availabilityStatusMissing, nil
	case withoutStock:
		return false, availabilityWithoutStock, nil
	default:
		return false, availabilityValueMissing, nil
	}
}

func supportedResourceAvailable(supported *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource, expected string, sizesGiB []int) bool {
	if supported == nil || stringValue(supported.Value) != expected {
		return false
	}
	switch supportedResourceAvailabilityReason(supported, sizesGiB) {
	case availabilityWithStock, availabilityStatusOnly, availabilityCategoryOnly:
		return true
	default:
		return false
	}
}

func supportedResourceAvailabilityReason(supported *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource, sizesGiB []int) string {
	if supported == nil {
		return availabilityValueMissing
	}
	status := stringValue(supported.Status)
	category := stringValue(supported.StatusCategory)
	switch {
	case status == "" && category == "":
		return availabilityBothStatusMissing
	case status == "":
		if category == "WithStock" {
			return supportedResourceRangeReason(supported, sizesGiB, availabilityCategoryOnly)
		}
		return availabilityStatusMissing
	case category == "":
		if status == "Available" {
			return supportedResourceRangeReason(supported, sizesGiB, availabilityStatusOnly)
		}
		return availabilityCategoryMissing
	}
	if !stockStatusAvailable(status, category) {
		return availabilityWithoutStock
	}
	return supportedResourceRangeReason(supported, sizesGiB, availabilityWithStock)
}

func supportedResourceRangeReason(supported *ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource, sizesGiB []int, availableReason string) string {
	if len(sizesGiB) == 0 {
		return availableReason
	}
	if stringValue(supported.Unit) != "GiB" || supported.Min == nil || supported.Max == nil || *supported.Min <= 0 {
		return availabilityRangeMissing
	}
	for _, sizeGiB := range sizesGiB {
		if *supported.Min > int32(sizeGiB) || int32(sizeGiB) > *supported.Max {
			return availabilityRangeNotCovered
		}
	}
	return availableReason
}

func stockStatusAvailable(status, category string) bool {
	return status == "Available" && category == "WithStock"
}

// PostPaidVCPUQuota returns exact regional or zonal PostPaid vCPU values.
func (a *OpenAPI) PostPaidVCPUQuota(ctx context.Context, region, zone string) (VCPUQuota, error) {
	if !a.validRegion(region) || strings.TrimSpace(zone) == "" {
		return VCPUQuota{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return VCPUQuota{}, err
	}
	response, err := a.ecs.DescribeAccountAttributes((&ecs.DescribeAccountAttributesRequest{}).
		SetRegionId(region).
		SetZoneId(zone).
		SetAttributeName([]*string{dara.String(maxPostPaidVCPUAttribute), dara.String(usedPostPaidVCPUAttribute)}))
	if err != nil || response == nil || response.Body == nil || response.Body.AccountAttributeItems == nil {
		return VCPUQuota{}, discoveryError("DescribeAccountAttributes", err)
	}
	values := make(map[string]int64, 2)
	for _, attribute := range response.Body.AccountAttributeItems.AccountAttributeItem {
		if attribute == nil || attribute.AttributeValues == nil || len(attribute.AttributeValues.ValueItem) == 0 || attribute.AttributeValues.ValueItem[0] == nil {
			continue
		}
		value, parseErr := strconv.ParseInt(stringValue(attribute.AttributeValues.ValueItem[0].Value), 10, 64)
		if parseErr != nil || value < 0 {
			return VCPUQuota{}, discoveryError("DescribeAccountAttributes value", parseErr)
		}
		values[stringValue(attribute.AttributeName)] = value
	}
	limit, limitOK := values[maxPostPaidVCPUAttribute]
	used, usedOK := values[usedPostPaidVCPUAttribute]
	if !limitOK || !usedOK || used > limit {
		return VCPUQuota{}, discoveryError("DescribeAccountAttributes incomplete", nil)
	}
	return VCPUQuota{Limit: limit, Used: used}, nil
}

// EIPQuota returns the account-wide pay-as-you-go EIP ceiling and current use.
func (a *OpenAPI) EIPQuota(ctx context.Context, region string) (EIPQuota, error) {
	if !a.validRegion(region) || a.quotas == nil {
		return EIPQuota{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return EIPQuota{}, err
	}
	return discoverEIPQuota(ctx, func(ctx context.Context, token string) ([]eipQuotaRecord, string, int32, error) {
		request := (&quotas.ListProductQuotasRequest{}).
			SetProductCode(eipQuotaProductCode).
			SetQuotaCategory(eipQuotaCategory).
			SetMaxResults(discoveryPageSize)
		if token != "" {
			request.SetNextToken(token)
		}
		response, err := a.quotas.ListProductQuotasWithContext(ctx, request, &dara.RuntimeOptions{})
		if err != nil || response == nil || response.Body == nil || response.Body.TotalCount == nil {
			return nil, "", 0, discoveryError("ListProductQuotas", err)
		}
		records := make([]eipQuotaRecord, 0, len(response.Body.Quotas))
		for _, quota := range response.Body.Quotas {
			if quota == nil {
				return nil, "", 0, discoveryError("ListProductQuotas nil quota", nil)
			}
			records = append(records, eipQuotaRecord{
				ProductCode: stringValue(quota.ProductCode),
				ActionCode:  stringValue(quota.QuotaActionCode),
				Category:    stringValue(quota.QuotaCategory),
				Name:        stringValue(quota.QuotaName),
				Limit:       quota.TotalQuota,
				Used:        quota.TotalUsage,
			})
		}
		return records, stringValue(response.Body.NextToken), int32Value(response.Body.TotalCount), nil
	})
}

type eipQuotaRecord struct {
	ProductCode string
	ActionCode  string
	Category    string
	Name        string
	Limit       *float32
	Used        *float32
}

type eipQuotaPageFetcher func(context.Context, string) ([]eipQuotaRecord, string, int32, error)

func discoverEIPQuota(ctx context.Context, fetch eipQuotaPageFetcher) (EIPQuota, error) {
	if fetch == nil {
		return EIPQuota{}, ErrDiscoveryUnavailable
	}
	records := make([]eipQuotaRecord, 0, 1)
	seenTokens := make(map[string]struct{}, maxDiscoveryPages)
	expectedTotal := int32(-1)
	token := ""
	for page := 0; page < maxDiscoveryPages; page++ {
		if err := ctx.Err(); err != nil {
			return EIPQuota{}, err
		}
		items, nextToken, total, err := fetch(ctx, token)
		if err != nil {
			return EIPQuota{}, err
		}
		if total < 0 || (expectedTotal >= 0 && total != expectedTotal) {
			return EIPQuota{}, discoveryError("ListProductQuotas inconsistent total", nil)
		}
		if expectedTotal < 0 {
			expectedTotal = total
		}
		records = append(records, items...)
		if int64(len(records)) > int64(expectedTotal) {
			return EIPQuota{}, discoveryError("ListProductQuotas excess records", nil)
		}
		nextToken = strings.TrimSpace(nextToken)
		if nextToken == "" {
			if int64(len(records)) != int64(expectedTotal) {
				return EIPQuota{}, discoveryError("ListProductQuotas incomplete inventory", nil)
			}
			matches := make([]eipQuotaRecord, 0, 1)
			nameMatches := 0
			for _, record := range records {
				if record.ProductCode != eipQuotaProductCode || record.Name != eipQuotaName {
					continue
				}
				nameMatches++
				// QuotaCategory is optional in the response. The request already
				// constrains the inventory to CommonQuota, so only an explicit
				// contradictory category invalidates the record.
				if record.Category == "" || record.Category == eipQuotaCategory {
					matches = append(matches, record)
				}
			}
			if len(matches) != 1 {
				return EIPQuota{}, discoveryError(fmt.Sprintf(
					"ListProductQuotas exact quota (records=%d name_matches=%d exact_matches=%d observed=%s)",
					len(records), nameMatches, len(matches), eipQuotaRecordSummary(records)), nil)
			}
			record := matches[0]
			limit, limitOK := wholeQuotaValue(record.Limit)
			used, usedOK := wholeQuotaValue(record.Used)
			if !limitOK || !usedOK || limit <= 0 || used > limit {
				return EIPQuota{}, discoveryError("ListProductQuotas value", nil)
			}
			return EIPQuota{Limit: limit, Used: used}, nil
		}
		if nextToken == token {
			return EIPQuota{}, discoveryError("ListProductQuotas repeated token", nil)
		}
		if _, exists := seenTokens[nextToken]; exists {
			return EIPQuota{}, discoveryError("ListProductQuotas token cycle", nil)
		}
		seenTokens[nextToken] = struct{}{}
		token = nextToken
	}
	return EIPQuota{}, discoveryError("ListProductQuotas page limit", nil)
}

func eipQuotaRecordSummary(records []eipQuotaRecord) string {
	const (
		maxRecords    = 8
		maxFieldBytes = 64
	)
	count := len(records)
	if count > maxRecords {
		count = maxRecords
	}
	identities := make([]string, 0, count+1)
	for _, record := range records[:count] {
		action := boundedQuotaEvidenceField(record.ActionCode, maxFieldBytes)
		category := boundedQuotaEvidenceField(record.Category, maxFieldBytes)
		if action == "" {
			action = "(omitted)"
		}
		if category == "" {
			category = "(omitted)"
		}
		name := boundedQuotaEvidenceField(record.Name, maxFieldBytes)
		if name == "" {
			name = "(omitted)"
		}
		identities = append(identities, action+"/"+category+"/"+name)
	}
	if len(records) > maxRecords {
		identities = append(identities, fmt.Sprintf("+%d_more", len(records)-maxRecords))
	}
	return strings.Join(identities, ",")
}

func boundedQuotaEvidenceField(value string, limit int) string {
	value = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, strings.TrimSpace(value))
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func wholeQuotaValue(value *float32) (int64, bool) {
	if value == nil || *value < 0 || math.IsNaN(float64(*value)) || math.IsInf(float64(*value), 0) ||
		float64(*value) > float64(math.MaxInt64) || math.Trunc(float64(*value)) != float64(*value) {
		return 0, false
	}
	return int64(*value), true
}

// AssertExactQuoteRolePolicy proves that the live role has exactly one custom
// attached policy and that its active document equals QuoteRolePolicyDocument.
func (a *OpenAPI) AssertExactQuoteRolePolicy(ctx context.Context, roleName, policyName string) error {
	return a.AssertExactRolePolicy(ctx, roleName, policyName, QuoteRolePolicyDocument())
}

// AssertExactRolePolicy proves that one role has exactly one custom attached
// policy and that its active document equals the canonical expected document.
func (a *OpenAPI) AssertExactRolePolicy(ctx context.Context, roleName, policyName, expectedDocument string) error {
	roleName = strings.TrimSpace(roleName)
	policyName = strings.TrimSpace(policyName)
	expectedDocument = normalizeRAMPolicyDocument(expectedDocument)
	if a == nil || a.ram == nil || roleName == "" || policyName == "" || expectedDocument == "" {
		return ErrInvalidConfig
	}
	list, err := a.ram.ListPoliciesForRoleWithContext(ctx,
		(&ram.ListPoliciesForRoleRequest{}).SetRoleName(roleName), &dara.RuntimeOptions{})
	if err != nil || list == nil || list.Body == nil || list.Body.Policies == nil || len(list.Body.Policies.Policy) != 1 {
		return discoveryError("ListPoliciesForRole exact allowlist", err)
	}
	attached := list.Body.Policies.Policy[0]
	if attached == nil || stringValue(attached.PolicyName) != policyName || stringValue(attached.PolicyType) != "Custom" ||
		strings.TrimSpace(stringValue(attached.DefaultVersion)) == "" {
		return discoveryError("ListPoliciesForRole unexpected policy", nil)
	}
	versionID := strings.TrimSpace(stringValue(attached.DefaultVersion))
	version, err := a.ram.GetPolicyVersionWithContext(ctx, (&ram.GetPolicyVersionRequest{}).
		SetPolicyName(policyName).
		SetPolicyType("Custom").
		SetVersionId(versionID), &dara.RuntimeOptions{})
	if err != nil || version == nil || version.Body == nil || version.Body.PolicyVersion == nil {
		return discoveryError("GetPolicyVersion", err)
	}
	active := version.Body.PolicyVersion
	if !boolValue(active.IsDefaultVersion) || stringValue(active.VersionId) != versionID ||
		normalizeRAMPolicyDocument(stringValue(active.PolicyDocument)) != expectedDocument {
		return discoveryError("GetPolicyVersion unexpected document", nil)
	}
	return nil
}

func normalizeRAMPolicyDocument(document string) string {
	if decoded, err := url.QueryUnescape(document); err == nil {
		document = decoded
	}
	var value any
	if json.Unmarshal([]byte(document), &value) != nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// AssertMutationDenied issues an ECS dry-run delete against a sentinel ID and
// succeeds only when RAM denies the action before any resource lookup. The
// DryRun flag guarantees that the probe cannot release an instance.
func (a *OpenAPI) AssertMutationDenied(ctx context.Context) error {
	if a == nil || a.ecs == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := a.ecs.DeleteInstanceWithContext(ctx, (&ecs.DeleteInstanceRequest{}).
		SetInstanceId(mutationPermissionProbeID).
		SetDryRun(true), &dara.RuntimeOptions{})
	if err == nil {
		return discoveryError("DeleteInstance permission probe unexpectedly succeeded", nil)
	}
	if ramPermissionDenied(err) {
		return nil
	}
	return discoveryError("DeleteInstance permission probe was not RAM-denied", err)
}

func ramPermissionDenied(err error) bool {
	var sdkErr *dara.SDKError
	if !errors.As(err, &sdkErr) || int32(dara.IntValue(sdkErr.StatusCode)) != 403 {
		return false
	}
	code := strings.TrimSpace(dara.StringValue(sdkErr.Code))
	return code == "Forbidden.RAM" || code == "Forbbiden.SubUser"
}

// Price returns a conservative provider unit price for one host hour or one
// GiB of pay-by-traffic public egress.
func (a *OpenAPI) Price(ctx context.Context, request PriceRequest) (Price, error) {
	if !a.validRegion(request.Region) {
		return Price{}, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return Price{}, err
	}
	providerRequest := (&ecs.DescribePriceRequest{}).
		SetRegionId(request.Region).
		SetPriceUnit("Hour").
		SetPeriod(1).
		SetAmount(1)
	switch request.Kind {
	case PriceKindHost:
		if request.BillingModel != providerBillingPostPaid || request.Zone == "" || request.InstanceType == "" || request.ImageID == "" ||
			request.SystemDiskGiB <= 0 || request.SystemDiskGiB > maxSystemDiskGiB ||
			request.SystemDiskClass != providerDiskESSD || request.SystemDiskLevel != providerDiskLevelPL0 ||
			request.DataDiskGiB <= 0 || request.DataDiskGiB > maxDataDiskGiB ||
			request.DataDiskClass != providerDiskESSD || request.DataDiskLevel != providerDiskLevelPL0 {
			return Price{}, ErrInvalidConfig
		}
		providerRequest.
			SetZoneId(request.Zone).
			SetResourceType("instance").
			SetInstanceType(request.InstanceType).
			SetImageId(request.ImageID).
			SetInstanceNetworkType("vpc").
			SetIoOptimized("optimized").
			SetSpotStrategy(providerSpotNoSpot).
			SetSystemDisk((&ecs.DescribePriceRequestSystemDisk{}).
				SetCategory(request.SystemDiskClass).
				SetPerformanceLevel(request.SystemDiskLevel).
				SetSize(int32(request.SystemDiskGiB))).
			SetDataDisk([]*ecs.DescribePriceRequestDataDisk{(&ecs.DescribePriceRequestDataDisk{}).
				SetCategory(request.DataDiskClass).
				SetPerformanceLevel(request.DataDiskLevel).
				SetSize(int64(request.DataDiskGiB))})
	case PriceKindEIPTraffic:
		if request.InternetCharge != providerInternetPayTraffic || request.PeakBandwidthMbps <= 0 || request.PeakBandwidthMbps > math.MaxInt32 {
			return Price{}, ErrInvalidConfig
		}
		providerRequest.
			SetResourceType("bandwidth").
			SetInternetChargeType(providerInternetPayTraffic).
			SetInternetMaxBandwidthOut(int32(request.PeakBandwidthMbps))
	default:
		return Price{}, ErrInvalidConfig
	}
	response, err := a.ecs.DescribePrice(providerRequest)
	if err != nil || response == nil || response.Body == nil || response.Body.PriceInfo == nil || response.Body.PriceInfo.Price == nil {
		return Price{}, discoveryError("DescribePrice", err)
	}
	return parsePriceMicros(stringValue(response.Body.PriceInfo.Price.Currency), response.Body.PriceInfo.Price.TradePrice)
}

func parsePriceMicros(currency string, value *float32) (Price, error) {
	currency = strings.TrimSpace(currency)
	if currency == "" || value == nil || *value <= 0 || math.IsNaN(float64(*value)) || math.IsInf(float64(*value), 0) {
		return Price{}, ErrDiscoveryUnavailable
	}
	decimal := strconv.FormatFloat(float64(*value), 'f', -1, 32)
	parts := strings.SplitN(decimal, ".", 2)
	if len(parts[0]) > 12 {
		return Price{}, ErrDiscoveryUnavailable
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole <= 0 && (len(parts) == 1 || strings.Trim(parts[1], "0") == "") || whole > math.MaxInt64/1_000_000 {
		return Price{}, ErrDiscoveryUnavailable
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	roundUp := false
	if len(fraction) > 6 {
		roundUp = strings.Trim(fraction[6:], "0") != ""
		fraction = fraction[:6]
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	fractionMicros, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return Price{}, ErrDiscoveryUnavailable
	}
	micros, ok := checkedAdd(whole*1_000_000, fractionMicros)
	if !ok || roundUp && micros == math.MaxInt64 {
		return Price{}, ErrDiscoveryUnavailable
	}
	if roundUp {
		micros++
	}
	if micros <= 0 {
		return Price{}, ErrDiscoveryUnavailable
	}
	return Price{Currency: currency, UnitCostMicros: micros}, nil
}

func (a *OpenAPI) validRegion(region string) bool {
	return a != nil && a.ecs != nil && a.region == RegionHangzhou && region == a.region
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int32Value(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

func boolValue(value *bool) bool { return value != nil && *value }
