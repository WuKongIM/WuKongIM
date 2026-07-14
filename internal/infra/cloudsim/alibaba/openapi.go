package alibaba

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	sts "github.com/alibabacloud-go/sts-20150401/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
	"github.com/aliyun/credentials-go/credentials"
)

const (
	defaultSDKPollInterval = 2 * time.Second
	defaultSDKWaitTimeout  = 3 * time.Minute
)

// OpenAPI is the production Alibaba API boundary backed by official Go SDK clients.
type OpenAPI struct {
	ecs          *ecs.Client
	vpc          *vpc.Client
	sts          *sts.Client
	pollInterval time.Duration
	waitTimeout  time.Duration
}

// NewOpenAPIFromEnvironment creates SDK clients from short-lived Alibaba
// credentials exported by the official GitHub OIDC credential action.
func NewOpenAPIFromEnvironment(region string) (*OpenAPI, error) {
	accessKeyID := strings.TrimSpace(os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID"))
	accessKeySecret := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")
	securityToken := os.Getenv("ALIBABA_CLOUD_SECURITY_TOKEN")
	if accessKeyID == "" || accessKeySecret == "" || securityToken == "" || strings.TrimSpace(region) == "" {
		return nil, fmt.Errorf("%w: short-lived Alibaba credentials and region are required", ErrInvalidConfig)
	}
	config := &openapiutil.Config{
		AccessKeyId: dara.String(accessKeyID), AccessKeySecret: dara.String(accessKeySecret),
		SecurityToken: dara.String(securityToken), RegionId: dara.String(region),
	}
	return newOpenAPI(config)
}

// NewOpenAPIFromDefaultCredential creates clients from Alibaba's default
// credential chain. It is reserved for the one-time CloudShell bootstrap CLI.
func NewOpenAPIFromDefaultCredential(region string) (*OpenAPI, error) {
	if strings.TrimSpace(region) == "" {
		return nil, ErrInvalidConfig
	}
	credential, err := credentials.NewCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("load Alibaba default credential: %w", err)
	}
	return newOpenAPI(&openapiutil.Config{Credential: credential, RegionId: dara.String(region)})
}

func newOpenAPI(config *openapiutil.Config) (*OpenAPI, error) {
	ecsClient, err := ecs.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create ECS client: %w", err)
	}
	vpcClient, err := vpc.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create VPC client: %w", err)
	}
	stsClient, err := sts.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("create STS client: %w", err)
	}
	return &OpenAPI{ecs: ecsClient, vpc: vpcClient, sts: stsClient, pollInterval: defaultSDKPollInterval, waitTimeout: defaultSDKWaitTimeout}, nil
}

// AccountIDHash verifies the current short-lived caller and returns only its
// non-secret stable account binding.
func (a *OpenAPI) AccountIDHash(ctx context.Context) (string, error) {
	if a == nil || a.sts == nil {
		return "", ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	response, err := a.sts.GetCallerIdentity()
	if err != nil || response.Body == nil || strings.TrimSpace(deref(response.Body.AccountId)) == "" {
		return "", errors.Join(ErrInvalidConfig, err)
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(deref(response.Body.AccountId))))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// Offers queries live spot availability and a one-hour price for each allowlisted SKU.
func (a *OpenAPI) Offers(ctx context.Context, request OfferRequest) ([]Offer, error) {
	if a == nil || a.ecs == nil {
		return nil, ErrInvalidConfig
	}
	offers := make([]Offer, 0, len(request.InstanceTypes))
	for _, instanceType := range request.InstanceTypes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		available, err := a.instanceAvailable(request, instanceType)
		if err != nil {
			offers = append(offers, Offer{InstanceType: instanceType, ZoneID: request.ZoneID})
			continue
		}
		price, currency, err := a.hourlyPrice(request, instanceType)
		if err != nil || currency != "CNY" || price <= 0 {
			offers = append(offers, Offer{InstanceType: instanceType, ZoneID: request.ZoneID, Available: available})
			continue
		}
		quotaAvailable, quotaErr := a.spotQuotaAvailable(request, instanceType)
		if quotaErr != nil {
			offers = append(offers, Offer{InstanceType: instanceType, ZoneID: request.ZoneID, HourlyCostMicros: price, Available: available})
			continue
		}
		offers = append(offers, Offer{
			InstanceType: instanceType, ZoneID: request.ZoneID, HourlyCostMicros: price,
			Available: available, QuotaAvailable: quotaAvailable,
		})
	}
	return offers, nil
}

func (a *OpenAPI) spotQuotaAvailable(request OfferRequest, instanceType string) (bool, error) {
	typesResponse, err := a.ecs.DescribeInstanceTypes((&ecs.DescribeInstanceTypesRequest{}).
		SetInstanceTypes([]*string{dara.String(instanceType)}))
	if err != nil {
		return false, err
	}
	var typesBody struct {
		InstanceTypes struct {
			InstanceType []struct {
				InstanceTypeID           string `json:"InstanceTypeId"`
				CPUCoreCount             int64  `json:"CpuCoreCount"`
				ENIPrivateIPAddrQuantity int64  `json:"EniPrivateIpAddressQuantity"`
			} `json:"InstanceType"`
		} `json:"InstanceTypes"`
	}
	if err := decodeSDKBody(typesResponse.Body, &typesBody); err != nil {
		return false, err
	}
	var cores int64
	var privateIPv4Capacity int64
	for _, item := range typesBody.InstanceTypes.InstanceType {
		if item.InstanceTypeID == instanceType {
			cores = item.CPUCoreCount
			privateIPv4Capacity = item.ENIPrivateIPAddrQuantity
			break
		}
	}
	if cores <= 0 || request.SimulatorPrivateIPv4Count <= 0 || privateIPv4Capacity < int64(request.SimulatorPrivateIPv4Count) || int64(request.HostCount) > math.MaxInt64/cores {
		return false, ErrInvalidConfig
	}
	attributesResponse, err := a.ecs.DescribeAccountAttributes((&ecs.DescribeAccountAttributesRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.ZoneID).
		SetAttributeName([]*string{dara.String("max-spot-instance-vcpu-count"), dara.String("used-spot-instance-vcpu-count")}))
	if err != nil {
		return false, err
	}
	var attributesBody struct {
		AccountAttributeItems struct {
			AccountAttributeItem []struct {
				AttributeName   string `json:"AttributeName"`
				AttributeValues struct {
					ValueItem []struct {
						Value string `json:"Value"`
					} `json:"ValueItem"`
				} `json:"AttributeValues"`
			} `json:"AccountAttributeItem"`
		} `json:"AccountAttributeItems"`
	}
	if err := decodeSDKBody(attributesResponse.Body, &attributesBody); err != nil {
		return false, err
	}
	values := make(map[string]int64, 2)
	for _, attribute := range attributesBody.AccountAttributeItems.AccountAttributeItem {
		if len(attribute.AttributeValues.ValueItem) == 0 {
			continue
		}
		value, parseErr := strconv.ParseInt(attribute.AttributeValues.ValueItem[0].Value, 10, 64)
		if parseErr != nil || value < 0 {
			return false, ErrInvalidConfig
		}
		values[attribute.AttributeName] = value
	}
	maximum, maxOK := values["max-spot-instance-vcpu-count"]
	used, usedOK := values["used-spot-instance-vcpu-count"]
	if !maxOK || !usedOK || used > maximum {
		return false, ErrInvalidConfig
	}
	return maximum-used >= cores*int64(request.HostCount), nil
}

func (a *OpenAPI) instanceAvailable(request OfferRequest, instanceType string) (bool, error) {
	response, err := a.ecs.DescribeAvailableResource((&ecs.DescribeAvailableResourceRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.ZoneID).
		SetDestinationResource("InstanceType").
		SetResourceType("instance").
		SetInstanceChargeType("PostPaid").
		SetSpotStrategy("SpotAsPriceGo").
		SetInstanceType(instanceType).
		SetIoOptimized("optimized").
		SetNetworkCategory("vpc"))
	if err != nil {
		return false, err
	}
	var body struct {
		AvailableZones struct {
			AvailableZone []struct {
				ZoneID             string `json:"ZoneId"`
				Status             string `json:"Status"`
				AvailableResources struct {
					AvailableResource []struct {
						SupportedResources struct {
							SupportedResource []struct {
								Value  string `json:"Value"`
								Status string `json:"Status"`
							} `json:"SupportedResource"`
						} `json:"SupportedResources"`
					} `json:"AvailableResource"`
				} `json:"AvailableResources"`
			} `json:"AvailableZone"`
		} `json:"AvailableZones"`
	}
	if err := decodeSDKBody(response.Body, &body); err != nil {
		return false, err
	}
	for _, zone := range body.AvailableZones.AvailableZone {
		if zone.ZoneID != request.ZoneID || zone.Status != "Available" {
			continue
		}
		for _, resource := range zone.AvailableResources.AvailableResource {
			for _, supported := range resource.SupportedResources.SupportedResource {
				if supported.Value == instanceType && supported.Status == "Available" {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (a *OpenAPI) hourlyPrice(request OfferRequest, instanceType string) (int64, string, error) {
	priceRequest := (&ecs.DescribePriceRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.ZoneID).
		SetResourceType("instance").
		SetInstanceType(instanceType).
		SetImageId(request.ImageID).
		SetSpotStrategy("SpotAsPriceGo").
		SetPriceUnit("Hour").
		SetPeriod(1).
		SetAmount(1).
		SetSystemDisk((&ecs.DescribePriceRequestSystemDisk{}).
			SetCategory(request.SystemDiskCategory).
			SetSize(request.SystemDiskSizeGiB)).
		SetDataDisk([]*ecs.DescribePriceRequestDataDisk{(&ecs.DescribePriceRequestDataDisk{}).
			SetCategory(request.DataDiskCategory).
			SetSize(int64(request.DataDiskSizeGiB))})
	if request.RequirePublicAddress {
		priceRequest.SetInternetChargeType("PayByBandwidth").SetInternetMaxBandwidthOut(request.PublicBandwidthMbps)
	}
	response, err := a.ecs.DescribePrice(priceRequest)
	if err != nil {
		return 0, "", err
	}
	var body struct {
		PriceInfo struct {
			Price struct {
				Currency   string  `json:"Currency"`
				TradePrice float64 `json:"TradePrice"`
			} `json:"Price"`
		} `json:"PriceInfo"`
	}
	if err := decodeSDKBody(response.Body, &body); err != nil {
		return 0, "", err
	}
	return int64(math.Ceil(body.PriceInfo.Price.TradePrice * 1_000_000)), body.PriceInfo.Price.Currency, nil
}

// ListAssets discovers every supported run resource type by mandatory tags.
func (a *OpenAPI) ListAssets(ctx context.Context, request ListAssetsRequest) ([]Asset, error) {
	if a == nil || a.ecs == nil || a.vpc == nil {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selector := map[string]string{cloudsim.TagManagedBy: cloudsim.ManagedByValue}
	if request.RunID != "" {
		selector[cloudsim.TagRunID] = request.RunID
	}
	assets := make([]Asset, 0, assetCount)

	instances, err := collectPages(ctx, func(page int32) ([]listedInstance, int, error) {
		response, listErr := a.ecs.DescribeInstances((&ecs.DescribeInstancesRequest{}).
			SetRegionId(request.Region).SetPageNumber(page).SetPageSize(100).SetTag(describeInstanceTags(selector)))
		if listErr != nil {
			return nil, 0, listErr
		}
		var body struct {
			TotalCount int `json:"TotalCount"`
			Instances  struct {
				Instance []listedInstance `json:"Instance"`
			} `json:"Instances"`
		}
		if decodeErr := decodeSDKBody(response.Body, &body); decodeErr != nil {
			return nil, 0, decodeErr
		}
		return body.Instances.Instance, body.TotalCount, nil
	})
	if err != nil {
		return nil, err
	}
	for _, instance := range instances {
		tags := ecsJSONTags(instance.Tags.Tag)
		privateAddress := ""
		if len(instance.VpcAttributes.PrivateIP.IPAddress) > 0 {
			privateAddress = instance.VpcAttributes.PrivateIP.IPAddress[0]
		}
		assets = append(assets, Asset{ID: instance.InstanceID, Kind: "compute", Role: tags[cloudsim.TagResourceRole], Billable: true, PrivateAddress: privateAddress, Tags: tags})
	}

	disks, err := collectPages(ctx, func(page int32) ([]listedDisk, int, error) {
		response, listErr := a.ecs.DescribeDisks((&ecs.DescribeDisksRequest{}).
			SetRegionId(request.Region).SetPageNumber(page).SetPageSize(100).SetDiskType("data").SetTag(describeDiskTags(selector)))
		if listErr != nil {
			return nil, 0, listErr
		}
		var body struct {
			TotalCount int `json:"TotalCount"`
			Disks      struct {
				Disk []listedDisk `json:"Disk"`
			} `json:"Disks"`
		}
		if decodeErr := decodeSDKBody(response.Body, &body); decodeErr != nil {
			return nil, 0, decodeErr
		}
		return body.Disks.Disk, body.TotalCount, nil
	})
	if err != nil {
		return nil, err
	}
	for _, disk := range disks {
		tags := ecsJSONTags(disk.Tags.Tag)
		assets = append(assets, Asset{ID: disk.DiskID, Kind: "disk", Role: tags[cloudsim.TagResourceRole], Billable: true, AttachedTo: disk.InstanceID, Tags: tags})
	}

	groups, err := collectPages(ctx, func(page int32) ([]listedSecurityGroup, int, error) {
		response, listErr := a.ecs.DescribeSecurityGroups((&ecs.DescribeSecurityGroupsRequest{}).
			SetRegionId(request.Region).SetPageNumber(page).SetPageSize(100).SetTag(describeSecurityGroupTags(selector)))
		if listErr != nil {
			return nil, 0, listErr
		}
		var body struct {
			TotalCount     int `json:"TotalCount"`
			SecurityGroups struct {
				SecurityGroup []listedSecurityGroup `json:"SecurityGroup"`
			} `json:"SecurityGroups"`
		}
		if decodeErr := decodeSDKBody(response.Body, &body); decodeErr != nil {
			return nil, 0, decodeErr
		}
		return body.SecurityGroups.SecurityGroup, body.TotalCount, nil
	})
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		tags := ecsJSONTags(group.Tags.Tag)
		assets = append(assets, Asset{ID: group.SecurityGroupID, Kind: "security-group", Role: tags[cloudsim.TagResourceRole], Tags: tags})
	}

	vpcs, err := collectPages(ctx, func(page int32) ([]listedVPC, int, error) {
		response, listErr := a.vpc.DescribeVpcs((&vpc.DescribeVpcsRequest{}).
			SetRegionId(request.Region).SetPageNumber(page).SetPageSize(100).SetTag(describeVPCTags(selector)))
		if listErr != nil {
			return nil, 0, listErr
		}
		var body struct {
			TotalCount int `json:"TotalCount"`
			Vpcs       struct {
				Vpc []listedVPC `json:"Vpc"`
			} `json:"Vpcs"`
		}
		if decodeErr := decodeSDKBody(response.Body, &body); decodeErr != nil {
			return nil, 0, decodeErr
		}
		return body.Vpcs.Vpc, body.TotalCount, nil
	})
	if err != nil {
		return nil, err
	}
	for _, item := range vpcs {
		tags := vpcJSONTags(item.Tags.Tag)
		assets = append(assets, Asset{ID: item.VpcID, Kind: "vpc", Role: tags[cloudsim.TagResourceRole], Tags: tags})
	}

	vswitches, err := collectPages(ctx, func(page int32) ([]listedVSwitch, int, error) {
		response, listErr := a.vpc.DescribeVSwitches((&vpc.DescribeVSwitchesRequest{}).
			SetRegionId(request.Region).SetPageNumber(page).SetPageSize(100).SetTag(describeVSwitchTags(selector)))
		if listErr != nil {
			return nil, 0, listErr
		}
		var body struct {
			TotalCount int `json:"TotalCount"`
			VSwitches  struct {
				VSwitch []listedVSwitch `json:"VSwitch"`
			} `json:"VSwitches"`
		}
		if decodeErr := decodeSDKBody(response.Body, &body); decodeErr != nil {
			return nil, 0, decodeErr
		}
		return body.VSwitches.VSwitch, body.TotalCount, nil
	})
	if err != nil {
		return nil, err
	}
	for _, item := range vswitches {
		tags := vpcJSONTags(item.Tags.Tag)
		assets = append(assets, Asset{ID: item.VSwitchID, Kind: "subnet", Role: tags[cloudsim.TagResourceRole], Tags: tags})
	}

	eips, err := collectPages(ctx, func(page int32) ([]listedEIP, int, error) {
		response, listErr := a.vpc.DescribeEipAddresses((&vpc.DescribeEipAddressesRequest{}).
			SetRegionId(request.Region).SetPageNumber(page).SetPageSize(100).SetTag(describeEIPTags(selector)))
		if listErr != nil {
			return nil, 0, listErr
		}
		var body struct {
			TotalCount   int `json:"TotalCount"`
			EipAddresses struct {
				EipAddress []listedEIP `json:"EipAddress"`
			} `json:"EipAddresses"`
		}
		if decodeErr := decodeSDKBody(response.Body, &body); decodeErr != nil {
			return nil, 0, decodeErr
		}
		return body.EipAddresses.EipAddress, body.TotalCount, nil
	})
	if err != nil {
		return nil, err
	}
	for _, item := range eips {
		tags := vpcJSONTags(item.Tags.Tag)
		assets = append(assets, Asset{ID: item.AllocationID, Kind: "public-address", Role: tags[cloudsim.TagResourceRole], Billable: true, PublicAddress: item.IPAddress, AttachedTo: item.InstanceID, Tags: tags})
	}
	return assets, nil
}

type listedInstance struct {
	InstanceID    string `json:"InstanceId"`
	VpcAttributes struct {
		PrivateIP struct {
			IPAddress []string `json:"IpAddress"`
		} `json:"PrivateIpAddress"`
	} `json:"VpcAttributes"`
	Tags struct {
		Tag []ecsTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type listedDisk struct {
	DiskID     string `json:"DiskId"`
	InstanceID string `json:"InstanceId"`
	Tags       struct {
		Tag []ecsTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type listedSecurityGroup struct {
	SecurityGroupID string `json:"SecurityGroupId"`
	Tags            struct {
		Tag []ecsTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type listedVPC struct {
	VpcID string `json:"VpcId"`
	Tags  struct {
		Tag []vpcTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type listedVSwitch struct {
	VSwitchID string `json:"VSwitchId"`
	Tags      struct {
		Tag []vpcTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type listedEIP struct {
	AllocationID string `json:"AllocationId"`
	IPAddress    string `json:"IpAddress"`
	InstanceID   string `json:"InstanceId"`
	Tags         struct {
		Tag []vpcTagJSON `json:"Tag"`
	} `json:"Tags"`
}

func collectPages[T any](ctx context.Context, fetch func(int32) ([]T, int, error)) ([]T, error) {
	result := make([]T, 0)
	for page := int32(1); page <= 10000; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, total, err := fetch(page)
		if err != nil {
			return nil, err
		}
		if total < 0 || total < len(result)+len(items) {
			return nil, ErrAmbiguousInventory
		}
		result = append(result, items...)
		if len(result) == total {
			return result, nil
		}
		if len(items) == 0 {
			return nil, ErrAmbiguousInventory
		}
	}
	return nil, ErrAmbiguousInventory
}

func collectTokenPages[T any](ctx context.Context, fetch func(string) ([]T, string, error)) ([]T, error) {
	result := make([]T, 0)
	nextToken := ""
	seen := make(map[string]struct{})
	for page := 0; page < 10000; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, followingToken, err := fetch(nextToken)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if followingToken == "" {
			return result, nil
		}
		if followingToken == nextToken {
			return nil, ErrAmbiguousInventory
		}
		if _, exists := seen[followingToken]; exists {
			return nil, ErrAmbiguousInventory
		}
		seen[followingToken] = struct{}{}
		nextToken = followingToken
	}
	return nil, ErrAmbiguousInventory
}

// CreateNetwork creates and tags the isolated VPC, vSwitch, and security group.
func (a *OpenAPI) CreateNetwork(ctx context.Context, request NetworkRequest) (_ []Asset, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vpcResponse, err := a.vpc.CreateVpc((&vpc.CreateVpcRequest{}).
		SetRegionId(request.Region).
		SetCidrBlock(request.VPCIPv4CIDR).
		SetVpcName(resourceName(request.Tags[cloudsim.TagRunID], "vpc")).
		SetClientToken(clientToken(request.Tags[cloudsim.TagRunID], "vpc")).
		SetTag(createVPCTags(request.Tags)))
	if err != nil {
		return nil, err
	}
	vpcID := deref(vpcResponse.Body.VpcId)
	if vpcID == "" {
		return nil, ErrAmbiguousInventory
	}
	defer func() {
		if err != nil {
			_, _ = a.vpc.DeleteVpc((&vpc.DeleteVpcRequest{}).SetRegionId(request.Region).SetVpcId(vpcID))
		}
	}()
	if err = a.waitVpc(ctx, request.Region, vpcID); err != nil {
		return nil, err
	}
	vswResponse, err := a.vpc.CreateVSwitch((&vpc.CreateVSwitchRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.ZoneID).
		SetVpcId(vpcID).
		SetCidrBlock(request.VSwitchIPv4CIDR).
		SetVSwitchName(resourceName(request.Tags[cloudsim.TagRunID], "subnet")).
		SetClientToken(clientToken(request.Tags[cloudsim.TagRunID], "subnet")).
		SetTag(createVSwitchTags(request.Tags)))
	if err != nil {
		return nil, err
	}
	vswitchID := deref(vswResponse.Body.VSwitchId)
	defer func() {
		if err != nil && vswitchID != "" {
			_, _ = a.vpc.DeleteVSwitch((&vpc.DeleteVSwitchRequest{}).SetRegionId(request.Region).SetVSwitchId(vswitchID))
		}
	}()
	securityResponse, err := a.ecs.CreateSecurityGroup((&ecs.CreateSecurityGroupRequest{}).
		SetRegionId(request.Region).
		SetVpcId(vpcID).
		SetSecurityGroupName(resourceName(request.Tags[cloudsim.TagRunID], "sg")).
		SetClientToken(clientToken(request.Tags[cloudsim.TagRunID], "sg")).
		SetTag(createSecurityGroupTags(request.Tags)))
	if err != nil {
		return nil, err
	}
	securityGroupID := deref(securityResponse.Body.SecurityGroupId)
	if securityGroupID == "" || vswitchID == "" {
		return nil, ErrAmbiguousInventory
	}
	defer func() {
		if err != nil {
			_, _ = a.ecs.DeleteSecurityGroup((&ecs.DeleteSecurityGroupRequest{}).SetRegionId(request.Region).SetSecurityGroupId(securityGroupID))
		}
	}()
	_, err = a.ecs.AuthorizeSecurityGroup((&ecs.AuthorizeSecurityGroupRequest{}).
		SetRegionId(request.Region).
		SetSecurityGroupId(securityGroupID).
		SetIpProtocol("tcp").
		SetPortRange("1/65535").
		SetSourceCidrIp(request.VSwitchIPv4CIDR).
		SetPolicy("accept").
		SetPriority("1").
		SetDescription("wukongim-cloud-sim internal run network"))
	if err != nil {
		return nil, err
	}
	return []Asset{
		{ID: vpcID, Kind: "vpc", Role: "run-network", Tags: cloneTags(request.Tags)},
		{ID: vswitchID, Kind: "subnet", Role: "run-network", Tags: cloneTags(request.Tags)},
		{ID: securityGroupID, Kind: "security-group", Role: "run-network", Tags: cloneTags(request.Tags)},
	}, nil
}

// CreateHost starts one tagged spot instance and waits until its tagged data disk is discoverable.
func (a *OpenAPI) CreateHost(ctx context.Context, request HostRequest) (_ []Asset, err error) {
	userData := cloudInit(request.SSHPublicKey, request.SecondaryPrivateIPv4, request.PrivateIPv4PrefixBits)
	diskID := ""
	diskAttached := false
	runRequest := (&ecs.RunInstancesRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.ZoneID).
		SetImageId(request.ImageID).
		SetInstanceType(request.InstanceType).
		SetInstanceChargeType("PostPaid").
		SetSpotStrategy("SpotAsPriceGo").
		SetSpotDuration(0).
		SetAmount(1).
		SetMinAmount(1).
		SetVSwitchId(request.VSwitchID).
		SetPrivateIpAddress(request.PrivateIPv4).
		SetSecurityGroupId(request.SecurityGroupID).
		SetInstanceName(resourceName(request.Tags[cloudsim.TagRunID], request.Role)).
		SetHostName(hostName(request.Tags[cloudsim.TagRunID], request.Role)).
		SetAutoReleaseTime(request.AutoReleaseAt.UTC().Format("2006-01-02T15:04:00Z")).
		SetClientToken(clientToken(request.Tags[cloudsim.TagRunID], request.Role)).
		SetUserData(base64.StdEncoding.EncodeToString([]byte(userData))).
		SetSystemDisk((&ecs.RunInstancesRequestSystemDisk{}).
			SetCategory(request.SystemDiskCategory).
			SetSize(strconvInt32(request.SystemDiskSizeGiB))).
		SetTag(runInstanceTags(request.Tags))
	response, err := a.ecs.RunInstances(runRequest)
	if err != nil {
		return nil, err
	}
	var instanceID string
	if response.Body != nil && response.Body.InstanceIdSets != nil && len(response.Body.InstanceIdSets.InstanceIdSet) == 1 {
		instanceID = deref(response.Body.InstanceIdSets.InstanceIdSet[0])
	}
	if instanceID == "" {
		return nil, ErrAmbiguousInventory
	}
	defer func() {
		if err != nil {
			_, _ = a.ecs.DeleteInstance((&ecs.DeleteInstanceRequest{}).SetInstanceId(instanceID).SetForce(true))
			if diskID != "" && !diskAttached {
				_, _ = a.ecs.DeleteDisk((&ecs.DeleteDiskRequest{}).SetDiskId(diskID))
			}
		}
	}()
	diskResponse, err := a.ecs.CreateDisk((&ecs.CreateDiskRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.ZoneID).
		SetDiskCategory(request.DataDiskCategory).
		SetSize(request.DataDiskSizeGiB).
		SetDiskName(resourceName(request.Tags[cloudsim.TagRunID], request.Role+"-data")).
		SetClientToken(clientToken(request.Tags[cloudsim.TagRunID], request.Role+"-data")).
		SetTag(createDiskTags(request.Tags)))
	if err != nil {
		return nil, err
	}
	if diskResponse.Body != nil {
		diskID = deref(diskResponse.Body.DiskId)
	}
	if diskID == "" {
		return nil, ErrAmbiguousInventory
	}
	if err = a.waitDiskAvailable(ctx, request.Region, diskID); err != nil {
		return nil, err
	}
	if err = a.waitInstanceAttachable(ctx, request.Region, instanceID); err != nil {
		return nil, err
	}
	_, err = a.ecs.AttachDisk((&ecs.AttachDiskRequest{}).
		SetInstanceId(instanceID).
		SetDiskId(diskID).
		SetDeleteWithInstance(true))
	if err != nil {
		return nil, err
	}
	diskAttached = true
	if len(request.SecondaryPrivateIPv4) != 0 {
		networkInterfaceID, interfaceErr := a.primaryNetworkInterfaceID(ctx, request.Region, instanceID)
		if interfaceErr != nil {
			return nil, interfaceErr
		}
		addresses := make([]*string, 0, len(request.SecondaryPrivateIPv4))
		for _, address := range request.SecondaryPrivateIPv4 {
			addresses = append(addresses, dara.String(address))
		}
		_, assignErr := a.ecs.AssignPrivateIpAddresses((&ecs.AssignPrivateIpAddressesRequest{}).
			SetRegionId(request.Region).
			SetNetworkInterfaceId(networkInterfaceID).
			SetPrivateIpAddress(addresses).
			SetClientToken(clientToken(request.Tags[cloudsim.TagRunID], request.Role+"-source-ips")))
		if assignErr != nil {
			return nil, assignErr
		}
	}
	deadline := time.Now().Add(a.waitTimeout)
	for {
		assets, listErr := a.ListAssets(ctx, ListAssetsRequest{Region: request.Region, RunID: request.Tags[cloudsim.TagRunID]})
		if listErr == nil {
			selected := make([]Asset, 0, 2)
			for _, asset := range assets {
				if asset.Role == request.Role && (asset.ID == instanceID || asset.Kind == "disk") {
					selected = append(selected, asset)
				}
			}
			if len(selected) == 2 {
				return selected, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("discover host %s assets: %w", request.Role, listErr)
		}
		if err := waitContext(ctx, a.pollInterval); err != nil {
			return nil, err
		}
	}
}

func (a *OpenAPI) waitInstanceAttachable(ctx context.Context, region, instanceID string) error {
	deadline := time.Now().Add(a.waitTimeout)
	instanceIDs, _ := json.Marshal([]string{instanceID})
	lastErr := error(ErrAmbiguousInventory)
	for {
		response, err := a.ecs.DescribeInstances((&ecs.DescribeInstancesRequest{}).
			SetRegionId(region).SetInstanceIds(string(instanceIDs)))
		if err == nil {
			var body struct {
				Instances struct {
					Instance []struct {
						InstanceID string `json:"InstanceId"`
						Status     string `json:"Status"`
					} `json:"Instance"`
				} `json:"Instances"`
			}
			if decodeErr := decodeSDKBody(response.Body, &body); decodeErr == nil {
				if len(body.Instances.Instance) == 1 && body.Instances.Instance[0].InstanceID == instanceID && attachableInstanceStatus(body.Instances.Instance[0].Status) {
					return nil
				}
				lastErr = ErrAmbiguousInventory
			} else {
				lastErr = decodeErr
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for attachable instance %s: %w", instanceID, lastErr)
		}
		if err := waitContext(ctx, a.pollInterval); err != nil {
			return err
		}
	}
}

func attachableInstanceStatus(status string) bool {
	return status == "Running" || status == "Stopped"
}

func (a *OpenAPI) waitDiskAvailable(ctx context.Context, region, diskID string) error {
	deadline := time.Now().Add(a.waitTimeout)
	diskIDs, _ := json.Marshal([]string{diskID})
	for {
		response, err := a.ecs.DescribeDisks((&ecs.DescribeDisksRequest{}).
			SetRegionId(region).SetDiskIds(string(diskIDs)))
		if err == nil {
			var body struct {
				Disks struct {
					Disk []struct {
						DiskID string `json:"DiskId"`
						Status string `json:"Status"`
					} `json:"Disk"`
				} `json:"Disks"`
			}
			if decodeErr := decodeSDKBody(response.Body, &body); decodeErr == nil && len(body.Disks.Disk) == 1 && body.Disks.Disk[0].DiskID == diskID && body.Disks.Disk[0].Status == "Available" {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for data disk %s: %w", diskID, err)
		}
		if err := waitContext(ctx, a.pollInterval); err != nil {
			return err
		}
	}
}

func (a *OpenAPI) primaryNetworkInterfaceID(ctx context.Context, region, instanceID string) (string, error) {
	deadline := time.Now().Add(a.waitTimeout)
	instanceIDs, _ := json.Marshal([]string{instanceID})
	for {
		response, err := a.ecs.DescribeInstances((&ecs.DescribeInstancesRequest{}).
			SetRegionId(region).SetInstanceIds(string(instanceIDs)))
		if err == nil {
			var body struct {
				Instances struct {
					Instance []struct {
						NetworkInterfaces struct {
							NetworkInterface []struct {
								NetworkInterfaceID string `json:"NetworkInterfaceId"`
							} `json:"NetworkInterface"`
						} `json:"NetworkInterfaces"`
					} `json:"Instance"`
				} `json:"Instances"`
			}
			if decodeErr := decodeSDKBody(response.Body, &body); decodeErr == nil && len(body.Instances.Instance) == 1 && len(body.Instances.Instance[0].NetworkInterfaces.NetworkInterface) > 0 {
				if id := body.Instances.Instance[0].NetworkInterfaces.NetworkInterface[0].NetworkInterfaceID; id != "" {
					return id, nil
				}
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("discover primary network interface for %s: %w", instanceID, err)
		}
		if err := waitContext(ctx, a.pollInterval); err != nil {
			return "", err
		}
	}
}

// CreatePublicAddress allocates and tags one pay-as-you-go EIP.
func (a *OpenAPI) CreatePublicAddress(ctx context.Context, request PublicAddressRequest) (_ Asset, err error) {
	if err := ctx.Err(); err != nil {
		return Asset{}, err
	}
	response, err := a.vpc.AllocateEipAddress((&vpc.AllocateEipAddressRequest{}).
		SetRegionId(request.Region).
		SetInstanceChargeType("PostPaid").
		SetInternetChargeType("PayByBandwidth").
		SetBandwidth(strconvInt32(request.BandwidthMbps)).
		SetName(resourceName(request.Tags[cloudsim.TagRunID], "sim-eip")).
		SetClientToken(clientToken(request.Tags[cloudsim.TagRunID], "eip")))
	if err != nil {
		return Asset{}, err
	}
	allocationID := deref(response.Body.AllocationId)
	defer func() {
		if err != nil && allocationID != "" {
			_, _ = a.vpc.ReleaseEipAddress((&vpc.ReleaseEipAddressRequest{}).SetRegionId(request.Region).SetAllocationId(allocationID))
		}
	}()
	if allocationID == "" {
		return Asset{}, ErrAmbiguousInventory
	}
	_, err = a.vpc.TagResources((&vpc.TagResourcesRequest{}).
		SetRegionId(request.Region).
		SetResourceType("EIP").
		SetResourceId([]*string{dara.String(allocationID)}).
		SetTag(tagResourceTags(request.Tags)))
	if err != nil {
		return Asset{}, err
	}
	return Asset{ID: allocationID, Kind: "public-address", Role: "sim", Billable: true, PublicAddress: deref(response.Body.EipAddress), Tags: cloneTags(request.Tags)}, nil
}

// AssociatePublicAddress binds the run EIP to the simulator instance.
func (a *OpenAPI) AssociatePublicAddress(ctx context.Context, allocationID, instanceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := a.vpc.AssociateEipAddress((&vpc.AssociateEipAddressRequest{}).
		SetRegionId(deref(a.vpc.RegionId)).SetAllocationId(allocationID).SetInstanceId(instanceID).SetInstanceType("EcsInstance"))
	return err
}

// SetIngress adds or removes one exact run-owned SSH or MCP rule.
func (a *OpenAPI) SetIngress(ctx context.Context, request IngressRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Open {
		description := ingressDescription(request.RunID, request.Port, request.Until)
		_, err := a.ecs.AuthorizeSecurityGroup((&ecs.AuthorizeSecurityGroupRequest{}).
			SetRegionId(deref(a.ecs.RegionId)).SetSecurityGroupId(request.SecurityGroupID).
			SetIpProtocol("tcp").SetPortRange(fmt.Sprintf("%d/%d", request.Port, request.Port)).
			SetSourceCidrIp(request.Source.String()).SetPolicy("accept").SetPriority("1").SetDescription(description))
		return err
	}
	list := func(listCtx context.Context) ([]securityGroupPermission, error) {
		return a.listSecurityGroupPermissions(listCtx, request.SecurityGroupID)
	}
	revoke := func(_ context.Context, permission securityGroupPermission) error {
		_, revokeErr := a.ecs.RevokeSecurityGroup((&ecs.RevokeSecurityGroupRequest{}).
			SetRegionId(deref(a.ecs.RegionId)).SetSecurityGroupId(request.SecurityGroupID).
			SetSecurityGroupRuleId([]*string{dara.String(permission.SecurityGroupRuleID)}))
		return revokeErr
	}
	return closeOwnedIngress(ctx, request.RunID, request.Port, list, revoke)
}

// ListIngress reads run-owned temporary rules so cleanup can distinguish an
// active local Analysis Session from an expired or malformed window.
func (a *OpenAPI) ListIngress(ctx context.Context, request IngressListRequest) ([]IngressWindow, error) {
	if a == nil || a.ecs == nil || strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.SecurityGroupID) == "" {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	permissions, err := a.listSecurityGroupPermissions(ctx, request.SecurityGroupID)
	if err != nil {
		return nil, err
	}
	return ingressWindowsFromPermissions(request.RunID, permissions), nil
}

type securityGroupPermission struct {
	SecurityGroupRuleID string `json:"SecurityGroupRuleId"`
	Description         string `json:"Description"`
	PortRange           string `json:"PortRange"`
	SourceCidrIP        string `json:"SourceCidrIp"`
}

func (a *OpenAPI) listSecurityGroupPermissions(ctx context.Context, securityGroupID string) ([]securityGroupPermission, error) {
	return collectTokenPages(ctx, func(nextToken string) ([]securityGroupPermission, string, error) {
		request := (&ecs.DescribeSecurityGroupAttributeRequest{}).
			SetRegionId(deref(a.ecs.RegionId)).SetSecurityGroupId(securityGroupID).
			SetDirection("ingress").SetMaxResults(500)
		if nextToken != "" {
			request.SetNextToken(nextToken)
		}
		response, err := a.ecs.DescribeSecurityGroupAttribute(request)
		if err != nil {
			return nil, "", err
		}
		var body struct {
			NextToken   string `json:"NextToken"`
			Permissions struct {
				Permission []securityGroupPermission `json:"Permission"`
			} `json:"Permissions"`
		}
		if err := decodeSDKBody(response.Body, &body); err != nil {
			return nil, "", err
		}
		return body.Permissions.Permission, body.NextToken, nil
	})
}

func ownedIngressPermissions(runID string, port uint16, permissions []securityGroupPermission) []securityGroupPermission {
	prefix := ingressDescriptionPrefix(runID, port)
	portRange := fmt.Sprintf("%d/%d", port, port)
	owned := make([]securityGroupPermission, 0, 1)
	for _, permission := range permissions {
		if strings.HasPrefix(permission.Description, prefix) && permission.PortRange == portRange {
			owned = append(owned, permission)
		}
	}
	return owned
}

func closeOwnedIngress(
	ctx context.Context,
	runID string,
	port uint16,
	list func(context.Context) ([]securityGroupPermission, error),
	revoke func(context.Context, securityGroupPermission) error,
) error {
	permissions, err := list(ctx)
	if err != nil {
		return err
	}
	owned := ownedIngressPermissions(runID, port, permissions)
	if len(owned) == 0 {
		return nil
	}
	errs := make([]error, 0)
	for _, permission := range owned {
		if permission.SecurityGroupRuleID == "" {
			errs = append(errs, ErrAmbiguousInventory)
			continue
		}
		if revokeErr := revoke(ctx, permission); revokeErr != nil {
			errs = append(errs, revokeErr)
		}
	}
	remaining, verifyErr := list(ctx)
	if verifyErr != nil {
		return errors.Join(append(errs, verifyErr)...)
	}
	if count := len(ownedIngressPermissions(runID, port, remaining)); count != 0 {
		errs = append(errs, fmt.Errorf("%w: %d ingress rules", ErrResidualResources, count))
	}
	return errors.Join(errs...)
}

func ingressWindowsFromPermissions(runID string, permissions []securityGroupPermission) []IngressWindow {
	windows := make([]IngressWindow, 0, 2)
	for _, permission := range permissions {
		for _, port := range []uint16{22, 19092} {
			prefix := ingressDescriptionPrefix(runID, port)
			if !strings.HasPrefix(permission.Description, prefix) || permission.PortRange != fmt.Sprintf("%d/%d", port, port) {
				continue
			}
			window := IngressWindow{Port: port}
			window.Until, _ = time.Parse(time.RFC3339, strings.TrimPrefix(permission.Description, prefix))
			if source, parseErr := netip.ParsePrefix(permission.SourceCidrIP); parseErr == nil &&
				source.Addr().Is4() && source.Bits() == 32 && source == source.Masked() {
				window.Source = source
			}
			windows = append(windows, window)
		}
	}
	return windows
}

// UpdateRunState writes the same lifecycle tags to every exact run asset.
func (a *OpenAPI) UpdateRunState(ctx context.Context, request StateUpdateRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.Region == "" || len(request.Assets) == 0 || !validReconciledState(request.State) {
		return ErrInvalidConfig
	}
	tags := map[string]string{tagRunState: string(request.State)}
	if request.State == cloudsim.StateRunning {
		if request.ActiveUntil.IsZero() {
			return ErrInvalidConfig
		}
		tags[tagActiveUntil] = request.ActiveUntil.UTC().Format(time.RFC3339)
	}
	ecsGroups := make(map[string][]*string)
	vpcGroups := make(map[string][]*string)
	for _, asset := range request.Assets {
		if asset.ID == "" {
			return ErrInvalidConfig
		}
		switch asset.Kind {
		case "compute":
			ecsGroups["instance"] = append(ecsGroups["instance"], dara.String(asset.ID))
		case "disk":
			ecsGroups["disk"] = append(ecsGroups["disk"], dara.String(asset.ID))
		case "security-group":
			ecsGroups["securitygroup"] = append(ecsGroups["securitygroup"], dara.String(asset.ID))
		case "vpc":
			vpcGroups["VPC"] = append(vpcGroups["VPC"], dara.String(asset.ID))
		case "subnet":
			vpcGroups["VSWITCH"] = append(vpcGroups["VSWITCH"], dara.String(asset.ID))
		case "public-address":
			vpcGroups["EIP"] = append(vpcGroups["EIP"], dara.String(asset.ID))
		default:
			return ErrInvalidConfig
		}
	}
	var updateErrs []error
	for resourceType, ids := range ecsGroups {
		ecsTags := make([]*ecs.TagResourcesRequestTag, 0, len(tags))
		for _, pair := range sortedTagPairs(tags) {
			ecsTags = append(ecsTags, (&ecs.TagResourcesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
		}
		if _, err := a.ecs.TagResources((&ecs.TagResourcesRequest{}).
			SetRegionId(request.Region).SetResourceType(resourceType).SetResourceId(ids).SetTag(ecsTags)); err != nil {
			updateErrs = append(updateErrs, err)
		}
	}
	for resourceType, ids := range vpcGroups {
		if _, err := a.vpc.TagResources((&vpc.TagResourcesRequest{}).
			SetRegionId(request.Region).SetResourceType(resourceType).SetResourceId(ids).SetTag(tagResourceTags(tags))); err != nil {
			updateErrs = append(updateErrs, err)
		}
	}
	return errors.Join(updateErrs...)
}

// DeleteAsset releases one normalized asset. Provider orders dependencies.
func (a *OpenAPI) DeleteAsset(ctx context.Context, asset Asset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	region := deref(a.ecs.RegionId)
	switch asset.Kind {
	case "public-address":
		if asset.AttachedTo != "" {
			_, _ = a.vpc.UnassociateEipAddress((&vpc.UnassociateEipAddressRequest{}).
				SetRegionId(region).SetAllocationId(asset.ID).SetInstanceId(asset.AttachedTo).SetInstanceType("EcsInstance").SetForce(true))
		}
		_, err := a.vpc.ReleaseEipAddress((&vpc.ReleaseEipAddressRequest{}).SetRegionId(region).SetAllocationId(asset.ID))
		return err
	case "compute":
		_, err := a.ecs.DeleteInstance((&ecs.DeleteInstanceRequest{}).SetInstanceId(asset.ID).SetForce(true))
		return err
	case "disk":
		_, err := a.ecs.DeleteDisk((&ecs.DeleteDiskRequest{}).SetDiskId(asset.ID))
		return ignoreIncorrectDiskStatus(err)
	case "security-group":
		_, err := a.ecs.DeleteSecurityGroup((&ecs.DeleteSecurityGroupRequest{}).SetRegionId(region).SetSecurityGroupId(asset.ID))
		return err
	case "subnet":
		_, err := a.vpc.DeleteVSwitch((&vpc.DeleteVSwitchRequest{}).SetRegionId(region).SetVSwitchId(asset.ID))
		return err
	case "vpc":
		_, err := a.vpc.DeleteVpc((&vpc.DeleteVpcRequest{}).SetRegionId(region).SetVpcId(asset.ID))
		return err
	default:
		return fmt.Errorf("%w: unsupported asset kind %q", ErrInvalidConfig, asset.Kind)
	}
}

func (a *OpenAPI) waitVpc(ctx context.Context, region, vpcID string) error {
	deadline := time.Now().Add(a.waitTimeout)
	for {
		response, err := a.vpc.DescribeVpcAttribute((&vpc.DescribeVpcAttributeRequest{}).SetRegionId(region).SetVpcId(vpcID))
		if err == nil && response.Body != nil && (deref(response.Body.Status) == "Available" || deref(response.Body.Status) == "Created") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for VPC %s: %w", vpcID, err)
		}
		if err := waitContext(ctx, a.pollInterval); err != nil {
			return err
		}
	}
}

func cloudInit(publicKey string, secondaryIPv4 []string, prefixBits int) string {
	var content strings.Builder
	fmt.Fprintf(&content, "#cloud-config\nusers:\n  - name: wukong\n    shell: /bin/bash\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    ssh_authorized_keys:\n      - %s\nssh_pwauth: false\ndisable_root: true\n", strings.TrimSpace(publicKey))
	if len(secondaryIPv4) != 0 {
		content.WriteString("runcmd:\n")
		for _, address := range secondaryIPv4 {
			fmt.Fprintf(&content, "  - ip address replace %s/%d dev $(ip -4 route show default | awk '{print $5; exit}')\n", address, prefixBits)
		}
	}
	return content.String()
}

func resourceName(runID, suffix string) string {
	clean := strings.NewReplacer("_", "-", "/", "-", ":", "-").Replace(runID)
	if len(clean) > 48 {
		clean = clean[:48]
	}
	return "wksim-" + clean + "-" + suffix
}

func hostName(runID, role string) string {
	name := resourceName(runID, role)
	if len(name) > 63 {
		return name[:63]
	}
	return name
}

func clientToken(runID, suffix string) string {
	digest := sha256.Sum256([]byte(runID + "\x00" + suffix))
	return fmt.Sprintf("%x", digest[:])
}

func ingressDescriptionPrefix(runID string, port uint16) string {
	return fmt.Sprintf("wksim:%s:%d:", runID, port)
}

func ingressDescription(runID string, port uint16, until time.Time) string {
	return ingressDescriptionPrefix(runID, port) + until.UTC().Format(time.RFC3339)
}

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func decodeSDKBody(input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, output)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func strconvInt32(value int32) string { return fmt.Sprintf("%d", value) }

func cloneTags(tags map[string]string) map[string]string {
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

func sortedTagPairs(tags map[string]string) [][2]string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([][2]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, [2]string{key, tags[key]})
	}
	return pairs
}

func createVPCTags(tags map[string]string) []*vpc.CreateVpcRequestTag {
	result := make([]*vpc.CreateVpcRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&vpc.CreateVpcRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func createVSwitchTags(tags map[string]string) []*vpc.CreateVSwitchRequestTag {
	result := make([]*vpc.CreateVSwitchRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&vpc.CreateVSwitchRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func createSecurityGroupTags(tags map[string]string) []*ecs.CreateSecurityGroupRequestTag {
	result := make([]*ecs.CreateSecurityGroupRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&ecs.CreateSecurityGroupRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func createDiskTags(tags map[string]string) []*ecs.CreateDiskRequestTag {
	result := make([]*ecs.CreateDiskRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&ecs.CreateDiskRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func runInstanceTags(tags map[string]string) []*ecs.RunInstancesRequestTag {
	result := make([]*ecs.RunInstancesRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&ecs.RunInstancesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func tagResourceTags(tags map[string]string) []*vpc.TagResourcesRequestTag {
	result := make([]*vpc.TagResourcesRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&vpc.TagResourcesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

type ecsTagJSON struct {
	Key   string `json:"TagKey"`
	Value string `json:"TagValue"`
}

type vpcTagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func ecsJSONTags(tags []ecsTagJSON) map[string]string {
	result := make(map[string]string, len(tags))
	for _, tag := range tags {
		result[tag.Key] = tag.Value
	}
	return result
}

func vpcJSONTags(tags []vpcTagJSON) map[string]string {
	result := make(map[string]string, len(tags))
	for _, tag := range tags {
		result[tag.Key] = tag.Value
	}
	return result
}

func describeInstanceTags(tags map[string]string) []*ecs.DescribeInstancesRequestTag {
	result := make([]*ecs.DescribeInstancesRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&ecs.DescribeInstancesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func describeDiskTags(tags map[string]string) []*ecs.DescribeDisksRequestTag {
	result := make([]*ecs.DescribeDisksRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&ecs.DescribeDisksRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func describeSecurityGroupTags(tags map[string]string) []*ecs.DescribeSecurityGroupsRequestTag {
	result := make([]*ecs.DescribeSecurityGroupsRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&ecs.DescribeSecurityGroupsRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func describeVPCTags(tags map[string]string) []*vpc.DescribeVpcsRequestTag {
	result := make([]*vpc.DescribeVpcsRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&vpc.DescribeVpcsRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func describeVSwitchTags(tags map[string]string) []*vpc.DescribeVSwitchesRequestTag {
	result := make([]*vpc.DescribeVSwitchesRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&vpc.DescribeVSwitchesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func describeEIPTags(tags map[string]string) []*vpc.DescribeEipAddressesRequestTag {
	result := make([]*vpc.DescribeEipAddressesRequestTag, 0, len(tags))
	for _, pair := range sortedTagPairs(tags) {
		result = append(result, (&vpc.DescribeEipAddressesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func ignoreIncorrectDiskStatus(err error) error {
	if err != nil && strings.Contains(err.Error(), "IncorrectDiskStatus") {
		return nil
	}
	return err
}
