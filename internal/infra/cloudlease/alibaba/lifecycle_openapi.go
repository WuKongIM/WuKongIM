package alibaba

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/alibabacloud-go/tea/dara"
	tea "github.com/alibabacloud-go/tea/tea"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
	"golang.org/x/crypto/ssh"
)

const (
	lifecycleSDKPollInterval = 2 * time.Second
	lifecycleSDKWaitTimeout  = 3 * time.Minute
	securityRulePrefix       = "wklease:v1:"
)

var _ LifecycleAPI = (*OpenAPI)(nil)

func (a *OpenAPI) lifecycleReady() bool {
	return a != nil && a.lifecycleAuthorized && a.region == RegionHangzhou && a.ecs != nil && a.vpc != nil
}

// CreateNetwork creates one tagged isolated VPC, vSwitch, and basic security group.
func (a *OpenAPI) CreateNetwork(ctx context.Context, request NetworkCreateRequest) ([]LifecycleAsset, error) {
	vpcPrefix, vpcErr := netip.ParsePrefix(request.VPCIPv4CIDR)
	vswitchPrefix, vswitchErr := netip.ParsePrefix(request.VSwitchIPv4CIDR)
	if !a.lifecycleReady() || request.Region != a.region || request.Zone == "" || request.ClientToken == "" ||
		vpcErr != nil || vswitchErr != nil || vpcPrefix != vpcPrefix.Masked() || vswitchPrefix != vswitchPrefix.Masked() ||
		vpcPrefix.Addr().Is6() || vswitchPrefix.Addr().Is6() || vpcPrefix.Bits() >= vswitchPrefix.Bits() ||
		!vpcPrefix.Contains(vswitchPrefix.Addr()) {
		return nil, ErrInvalidConfig
	}
	tags, err := openAPIResourceTags(request.Tags, "network")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vpcResponse, err := a.vpc.CreateVpc((&vpc.CreateVpcRequest{}).
		SetRegionId(request.Region).
		SetCidrBlock(request.VPCIPv4CIDR).
		SetVpcName(openAPIResourceName(tags[cloudlease.TagLeaseID], "vpc")).
		SetClientToken(request.ClientToken).
		SetTag(createLifecycleVPCTags(tags)))
	if err != nil || vpcResponse == nil || vpcResponse.Body == nil || stringValue(vpcResponse.Body.VpcId) == "" {
		return nil, errors.Join(ErrAmbiguousInventory, err)
	}
	vpcID := stringValue(vpcResponse.Body.VpcId)
	if err := a.waitVPCAvailable(ctx, request.Region, vpcID); err != nil {
		return nil, err
	}
	vswitchResponse, err := a.vpc.CreateVSwitch((&vpc.CreateVSwitchRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.Zone).
		SetVpcId(vpcID).
		SetCidrBlock(request.VSwitchIPv4CIDR).
		SetVSwitchName(openAPIResourceName(tags[cloudlease.TagLeaseID], "vswitch")).
		SetClientToken(lifecycleClientToken(tags[cloudlease.TagLeaseID], "vswitch", "network", 0)).
		SetTag(createLifecycleVSwitchTags(tags)))
	if err != nil || vswitchResponse == nil || vswitchResponse.Body == nil || stringValue(vswitchResponse.Body.VSwitchId) == "" {
		return nil, errors.Join(ErrAmbiguousInventory, err)
	}
	vswitchID := stringValue(vswitchResponse.Body.VSwitchId)
	if err := a.waitVSwitchAvailable(ctx, request.Region, vswitchID); err != nil {
		return nil, err
	}
	groupResponse, err := a.ecs.CreateSecurityGroup((&ecs.CreateSecurityGroupRequest{}).
		SetRegionId(request.Region).
		SetVpcId(vpcID).
		SetSecurityGroupType("normal").
		SetSecurityGroupName(openAPIResourceName(tags[cloudlease.TagLeaseID], "sg")).
		SetClientToken(lifecycleClientToken(tags[cloudlease.TagLeaseID], "security-group", "network", 0)).
		SetTag(createLifecycleSecurityGroupTags(tags)))
	if err != nil || groupResponse == nil || groupResponse.Body == nil || stringValue(groupResponse.Body.SecurityGroupId) == "" {
		return nil, errors.Join(ErrAmbiguousInventory, err)
	}
	securityGroupID := stringValue(groupResponse.Body.SecurityGroupId)
	return []LifecycleAsset{
		{ID: vpcID, Kind: ResourceKindVPC, Role: "network", Tags: maps.Clone(tags)},
		{ID: vswitchID, Kind: ResourceKindVSwitch, Role: "network", ParentID: vpcID, Tags: maps.Clone(tags)},
		{ID: securityGroupID, Kind: ResourceKindSecurityGroup, Role: "network", ParentID: vpcID, Tags: maps.Clone(tags)},
	}, nil
}

// CreateHost creates one regular PostPaid private ECS instance with one system
// and one delete-with-instance ESSD PL0 data disk, then tags disks and its ENI.
func (a *OpenAPI) CreateHost(ctx context.Context, request HostCreateRequest) ([]LifecycleAsset, error) {
	if !a.lifecycleReady() || request.Region != a.region || request.Zone == "" || request.Role == "" || request.Ordinal <= 0 ||
		request.InstanceType == "" || request.ImageID == "" || request.VSwitchID == "" || request.SecurityGroupID == "" ||
		request.SystemDiskGiB <= 0 || request.DataDiskGiB <= 0 || request.PublicIPv4 ||
		request.AutoReleaseAt.IsZero() || request.ClientToken == "" || len(request.BootstrapAuthorizedKeys) != 2 {
		return nil, ErrInvalidConfig
	}
	userData, err := lifecycleCloudInit(request.BootstrapAuthorizedKeys)
	if err != nil {
		return nil, err
	}
	tags, err := openAPIResourceTags(request.Tags, request.Role)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := a.ecs.RunInstances((&ecs.RunInstancesRequest{}).
		SetRegionId(request.Region).
		SetZoneId(request.Zone).
		SetImageId(request.ImageID).
		SetInstanceType(request.InstanceType).
		SetInstanceChargeType(providerBillingPostPaid).
		SetSpotStrategy(providerSpotNoSpot).
		SetAmount(1).
		SetMinAmount(1).
		SetVSwitchId(request.VSwitchID).
		SetSecurityGroupId(request.SecurityGroupID).
		SetInternetMaxBandwidthOut(0).
		SetInstanceName(openAPIResourceName(tags[cloudlease.TagLeaseID], fmt.Sprintf("%s-%d", request.Role, request.Ordinal))).
		SetHostName(openAPIHostName(tags[cloudlease.TagLeaseID], request.Role, request.Ordinal)).
		SetAutoReleaseTime(request.AutoReleaseAt.UTC().Format("2006-01-02T15:04:00Z")).
		SetUserData(userData).
		SetClientToken(request.ClientToken).
		SetSystemDisk((&ecs.RunInstancesRequestSystemDisk{}).
			SetCategory(providerDiskESSD).SetPerformanceLevel(providerDiskLevelPL0).SetSize(strconv.Itoa(request.SystemDiskGiB))).
		SetDataDisk([]*ecs.RunInstancesRequestDataDisk{(&ecs.RunInstancesRequestDataDisk{}).
			SetCategory(providerDiskESSD).SetPerformanceLevel(providerDiskLevelPL0).
			SetSize(int32(request.DataDiskGiB)).SetDeleteWithInstance(true)}).
		SetTag(runLifecycleInstanceTags(tags)))
	if err != nil || response == nil || response.Body == nil || response.Body.InstanceIdSets == nil ||
		len(response.Body.InstanceIdSets.InstanceIdSet) != 1 || stringValue(response.Body.InstanceIdSets.InstanceIdSet[0]) == "" {
		return nil, errors.Join(ErrAmbiguousInventory, err)
	}
	instanceID := stringValue(response.Body.InstanceIdSets.InstanceIdSet[0])
	assets, err := a.waitCreatedHostAssets(ctx, request, instanceID, tags)
	if err != nil {
		return nil, err
	}
	return assets, nil
}

func lifecycleCloudInit(authorizedKeys []string) (string, error) {
	normalized, _, err := lifecycleBootstrapIdentity(authorizedKeys)
	if err != nil {
		return "", err
	}
	var document strings.Builder
	document.WriteString("#cloud-config\nusers:\n  - default\n  - name: wkdeploy\n    gecos: WuKongIM deployment\n    shell: /bin/bash\n    lock_passwd: true\n    sudo: ALL=(ALL) NOPASSWD:ALL\n    ssh_authorized_keys:\n")
	for _, key := range normalized {
		document.WriteString("      - ")
		document.WriteString(key)
		document.WriteByte('\n')
	}
	document.WriteString("ssh_pwauth: false\ndisable_root: true\n")
	return base64.StdEncoding.EncodeToString([]byte(document.String())), nil
}

func lifecycleBootstrapIdentity(authorizedKeys []string) ([]string, string, error) {
	if len(authorizedKeys) != 2 {
		return nil, "", ErrInvalidConfig
	}
	normalized := make([]string, 0, len(authorizedKeys))
	seen := make(map[string]struct{}, len(authorizedKeys))
	for _, value := range authorizedKeys {
		trimmed := strings.TrimSpace(value)
		publicKey, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(trimmed))
		if err != nil || publicKey.Type() != ssh.KeyAlgoED25519 || strings.TrimSpace(string(rest)) != "" || strings.ContainsAny(trimmed, "\r\n") {
			return nil, "", ErrInvalidConfig
		}
		key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey)))
		if _, exists := seen[key]; exists {
			return nil, "", ErrInvalidConfig
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	sort.Strings(normalized)
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\n")))
	return normalized, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// CreatePublicAddress atomically allocates a tagged PostPaid pay-by-traffic EIP.
func (a *OpenAPI) CreatePublicAddress(ctx context.Context, request PublicAddressCreateRequest) (LifecycleAsset, error) {
	if !a.lifecycleReady() || request.Region != a.region || request.Role == "" || request.PeakBandwidthMbps <= 0 ||
		request.InternetChargeType != providerInternetPayTraffic || request.ClientToken == "" {
		return LifecycleAsset{}, ErrInvalidConfig
	}
	tags, err := openAPIResourceTags(request.Tags, request.Role)
	if err != nil {
		return LifecycleAsset{}, err
	}
	if err := ctx.Err(); err != nil {
		return LifecycleAsset{}, err
	}
	response, err := a.vpc.AllocateEipAddress((&vpc.AllocateEipAddressRequest{}).
		SetRegionId(request.Region).
		SetInstanceChargeType(providerBillingPostPaid).
		SetInternetChargeType(request.InternetChargeType).
		SetBandwidth(strconv.Itoa(request.PeakBandwidthMbps)).
		SetClientToken(request.ClientToken).
		SetTag(allocateLifecycleEIPTags(tags)))
	if err != nil || response == nil || response.Body == nil || stringValue(response.Body.AllocationId) == "" ||
		stringValue(response.Body.EipAddress) == "" {
		return LifecycleAsset{}, errors.Join(ErrAmbiguousInventory, err)
	}
	return LifecycleAsset{
		ID: stringValue(response.Body.AllocationId), Kind: ResourceKindEIP, Role: request.Role,
		Billable: true, PublicAddress: stringValue(response.Body.EipAddress), Tags: tags,
	}, nil
}

// AssociatePublicAddress idempotently binds one EIP to its exact ECS instance.
func (a *OpenAPI) AssociatePublicAddress(ctx context.Context, request PublicAddressAssociationRequest) error {
	if !a.lifecycleReady() || request.Region != a.region || request.AllocationID == "" || request.InstanceID == "" || request.ClientToken == "" {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := a.vpc.AssociateEipAddress((&vpc.AssociateEipAddressRequest{}).
		SetRegionId(request.Region).SetAllocationId(request.AllocationID).
		SetInstanceId(request.InstanceID).SetInstanceType("EcsInstance").SetClientToken(request.ClientToken))
	return err
}

type encodedSecurityRule struct {
	LeaseID     string `json:"l"`
	Kind        string `json:"k"`
	ID          string `json:"i"`
	TargetRole  string `json:"r"`
	Protocol    string `json:"p"`
	PortFrom    uint16 `json:"f"`
	PortTo      uint16 `json:"t"`
	Source      string `json:"s"`
	Destination string `json:"d"`
	UntilUnix   int64  `json:"u"`
}

func securityRuleDescription(request AccessRuleRequest) (string, error) {
	document := encodedSecurityRule{
		LeaseID: request.Tags[cloudlease.TagLeaseID], Kind: string(request.Kind), ID: request.ID,
		TargetRole: request.TargetRole, Protocol: string(request.Protocol), PortFrom: request.PortFrom,
		PortTo: request.PortTo, Source: request.SourcePrefix.String(), Destination: request.DestinationPrefix.String(),
		UntilUnix: request.Until.UTC().Unix(),
	}
	data, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	value := securityRulePrefix + base64.RawURLEncoding.EncodeToString(data)
	if len(value) > 512 {
		return "", ErrInvalidConfig
	}
	return value, nil
}

func parseSecurityRuleDescription(value string) (encodedSecurityRule, bool) {
	if !strings.HasPrefix(value, securityRulePrefix) {
		return encodedSecurityRule{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, securityRulePrefix))
	if err != nil {
		return encodedSecurityRule{}, false
	}
	var rule encodedSecurityRule
	if json.Unmarshal(data, &rule) != nil || rule.LeaseID == "" || rule.ID == "" || rule.TargetRole == "" ||
		(rule.Kind != string(AccessRulePrivate) && rule.Kind != string(AccessRuleGrant)) ||
		(rule.Protocol != string(cloudlease.ProtocolTCP) && rule.Protocol != string(cloudlease.ProtocolUDP)) ||
		rule.PortFrom == 0 || rule.PortTo < rule.PortFrom || rule.UntilUnix <= 0 {
		return encodedSecurityRule{}, false
	}
	source, sourceErr := netip.ParsePrefix(rule.Source)
	destination, destinationErr := netip.ParsePrefix(rule.Destination)
	if sourceErr != nil || destinationErr != nil || source != source.Masked() || destination != destination.Masked() {
		return encodedSecurityRule{}, false
	}
	return rule, true
}

// SetAccessRule creates or removes one exact description-owned quintuple rule.
func (a *OpenAPI) SetAccessRule(ctx context.Context, request AccessRuleRequest) error {
	if !a.lifecycleReady() || request.Region != a.region || request.ID == "" || request.SecurityGroupID == "" ||
		!request.SourcePrefix.IsValid() || !request.DestinationPrefix.IsValid() || request.PortFrom == 0 || request.PortTo < request.PortFrom {
		return ErrInvalidConfig
	}
	description, err := securityRuleDescription(request)
	if err != nil {
		return err
	}
	permissions, err := a.listLifecycleSecurityRules(ctx, request.SecurityGroupID)
	if err != nil {
		return err
	}
	matches := make([]lifecycleSecurityPermission, 0, 1)
	for _, permission := range permissions {
		decoded, ok := parseSecurityRuleDescription(permission.Description)
		if ok && decoded.LeaseID == request.Tags[cloudlease.TagLeaseID] && decoded.ID == request.ID {
			matches = append(matches, permission)
		}
	}
	if request.Remove {
		for _, permission := range matches {
			if permission.RuleID == "" {
				return ErrAmbiguousInventory
			}
			if _, revokeErr := a.ecs.RevokeSecurityGroup((&ecs.RevokeSecurityGroupRequest{}).
				SetRegionId(request.Region).SetSecurityGroupId(request.SecurityGroupID).
				SetSecurityGroupRuleId([]*string{dara.String(permission.RuleID)})); revokeErr != nil && !alreadyAbsentError(revokeErr) {
				return revokeErr
			}
		}
		return a.verifyAccessRule(ctx, request, description, false)
	}
	if len(matches) == 1 && matches[0].Description == description {
		return nil
	}
	if len(matches) != 0 {
		return ErrAmbiguousInventory
	}
	permission := (&ecs.AuthorizeSecurityGroupRequestPermissions{}).
		SetIpProtocol(strings.ToUpper(string(request.Protocol))).
		SetPortRange(fmt.Sprintf("%d/%d", request.PortFrom, request.PortTo)).
		SetSourceCidrIp(request.SourcePrefix.String()).
		SetDestCidrIp(request.DestinationPrefix.String()).
		SetPolicy("accept").SetPriority("1").SetDescription(description)
	_, err = a.ecs.AuthorizeSecurityGroup((&ecs.AuthorizeSecurityGroupRequest{}).
		SetRegionId(request.Region).SetSecurityGroupId(request.SecurityGroupID).
		SetClientToken(lifecycleClientToken(request.Tags[cloudlease.TagLeaseID], "security-rule", request.ID, 0)).
		SetPermissions([]*ecs.AuthorizeSecurityGroupRequestPermissions{permission}))
	if err != nil {
		return err
	}
	return a.verifyAccessRule(ctx, request, description, true)
}

func (a *OpenAPI) verifyAccessRule(ctx context.Context, request AccessRuleRequest, description string, present bool) error {
	permissions, err := a.listLifecycleSecurityRules(ctx, request.SecurityGroupID)
	if err != nil {
		return err
	}
	matches := 0
	for _, permission := range permissions {
		decoded, ok := parseSecurityRuleDescription(permission.Description)
		if ok && decoded.LeaseID == request.Tags[cloudlease.TagLeaseID] && decoded.ID == request.ID {
			if permission.Description != description || !lifecyclePermissionMatchesDescription(permission, decoded) {
				return ErrAmbiguousInventory
			}
			matches++
		}
	}
	if present && matches == 1 || !present && matches == 0 {
		return nil
	}
	return ErrAmbiguousInventory
}

// SetLifecycleState updates the lifecycle tag on every taggable related resource.
func (a *OpenAPI) SetLifecycleState(ctx context.Context, query InventoryQuery, state string) error {
	if !a.lifecycleReady() || (state != lifecycleStateAcquiring && state != lifecycleStateActive && state != lifecycleStateCleanup) {
		return ErrInvalidConfig
	}
	assets, err := a.ListAssets(ctx, query)
	if err != nil {
		return err
	}
	return a.tagLifecycleAssets(query.Region, assets, map[string]string{lifecycleStateTag: state})
}

// DeleteAsset performs one dependency-ordered idempotent deletion operation.
func (a *OpenAPI) DeleteAsset(ctx context.Context, asset LifecycleAsset) error {
	if !a.lifecycleReady() || asset.ID == "" {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	switch asset.Kind {
	case ResourceKindSecurityRule:
		_, err = a.ecs.RevokeSecurityGroup((&ecs.RevokeSecurityGroupRequest{}).
			SetRegionId(a.region).SetSecurityGroupId(asset.ParentID).
			SetSecurityGroupRuleId([]*string{dara.String(asset.ID)}))
	case ResourceKindRouteEntry:
		_, err = a.vpc.DeleteRouteEntry((&vpc.DeleteRouteEntryRequest{}).
			SetRegionId(a.region).SetRouteEntryId(asset.ID))
	case ResourceKindEIPAssociation:
		_, err = a.vpc.UnassociateEipAddress((&vpc.UnassociateEipAddressRequest{}).
			SetRegionId(a.region).SetAllocationId(asset.Attributes["eip_id"]).
			SetInstanceId(asset.ParentID).SetInstanceType("EcsInstance").SetForce(true))
	case ResourceKindEIP:
		_, err = a.vpc.ReleaseEipAddress((&vpc.ReleaseEipAddressRequest{}).SetRegionId(a.region).SetAllocationId(asset.ID))
	case ResourceKindDiskAttachment:
		_, err = a.ecs.DetachDisk((&ecs.DetachDiskRequest{}).SetInstanceId(asset.ParentID).SetDiskId(asset.Attributes["disk_id"]))
	case ResourceKindInstance:
		_, err = a.ecs.DeleteInstance((&ecs.DeleteInstanceRequest{}).SetInstanceId(asset.ID).SetForce(true))
	case ResourceKindDisk:
		_, err = a.ecs.DeleteDisk((&ecs.DeleteDiskRequest{}).SetDiskId(asset.ID))
	case ResourceKindENI:
		_, err = a.ecs.DeleteNetworkInterface((&ecs.DeleteNetworkInterfaceRequest{}).SetRegionId(a.region).SetNetworkInterfaceId(asset.ID))
	case ResourceKindSecurityGroup:
		_, err = a.ecs.DeleteSecurityGroup((&ecs.DeleteSecurityGroupRequest{}).SetRegionId(a.region).SetSecurityGroupId(asset.ID))
	case ResourceKindNATGateway:
		_, err = a.vpc.DeleteNatGateway((&vpc.DeleteNatGatewayRequest{}).SetRegionId(a.region).SetNatGatewayId(asset.ID).SetForce(true))
	case ResourceKindVSwitch:
		_, err = a.vpc.DeleteVSwitch((&vpc.DeleteVSwitchRequest{}).SetRegionId(a.region).SetVSwitchId(asset.ID))
	case ResourceKindVPC:
		_, err = a.vpc.DeleteVpc((&vpc.DeleteVpcRequest{}).SetRegionId(a.region).SetVpcId(asset.ID))
	default:
		return ErrAmbiguousInventory
	}
	if alreadyAbsentError(err) {
		return nil
	}
	return err
}

func alreadyAbsentError(err error) bool {
	if err == nil {
		return false
	}
	code := lifecycleSDKErrorCode(err)
	return strings.Contains(strings.ToLower(code), "notfound") || strings.Contains(strings.ToLower(code), "notexist")
}

func lifecycleSDKErrorCode(err error) string {
	var teaErr *tea.SDKError
	if errors.As(err, &teaErr) {
		return stringValue(teaErr.Code)
	}
	var daraErr *dara.SDKError
	if errors.As(err, &daraErr) {
		return stringValue(daraErr.Code)
	}
	return ""
}

func openAPIResourceTags(base map[string]string, role string) (map[string]string, error) {
	tags := maps.Clone(base)
	tags[cloudlease.TagResourceRole] = role
	if len(tags) > 20 {
		return nil, ErrInvalidConfig
	}
	for key, value := range tags {
		if key == "" || len(key) > 128 || len(value) > 128 || strings.Contains(key, "http://") || strings.Contains(key, "https://") ||
			strings.Contains(value, "http://") || strings.Contains(value, "https://") || strings.HasPrefix(key, "acs:") || strings.HasPrefix(key, "aliyun") {
			return nil, ErrInvalidConfig
		}
	}
	return tags, nil
}

func openAPIResourceName(leaseID, suffix string) string {
	clean := strings.NewReplacer("_", "-", "/", "-", ":", "-").Replace(leaseID)
	if len(clean) > 42 {
		clean = clean[:42]
	}
	return "wklease-" + clean + "-" + suffix
}

func openAPIHostName(leaseID, role string, ordinal int) string {
	name := openAPIResourceName(leaseID, fmt.Sprintf("%s-%d", role, ordinal))
	if len(name) > 63 {
		return name[:63]
	}
	return name
}

func sortedLifecycleTagPairs(tags map[string]string) [][2]string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([][2]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, [2]string{key, tags[key]})
	}
	return result
}
