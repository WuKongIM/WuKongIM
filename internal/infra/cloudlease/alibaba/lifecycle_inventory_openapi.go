package alibaba

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudlease"
	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
)

const (
	lifecycleInventoryPageSize = int32(100)
	lifecycleVPCPageSize       = int32(50)
)

type lifecycleECSTagJSON struct {
	Key   string `json:"TagKey"`
	Value string `json:"TagValue"`
}

type lifecycleVPCTagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type lifecycleListedInstance struct {
	InstanceID    string `json:"InstanceId"`
	InstanceType  string `json:"InstanceType"`
	ImageID       string `json:"ImageId"`
	VpcAttributes struct {
		VPCID     string `json:"VpcId"`
		VSwitchID string `json:"VSwitchId"`
		PrivateIP struct {
			IPAddress []string `json:"IpAddress"`
		} `json:"PrivateIpAddress"`
	} `json:"VpcAttributes"`
	Tags struct {
		Tag []lifecycleECSTagJSON `json:"Tag"`
	} `json:"Tags"`
}

func (i lifecycleListedInstance) networkIDs() (string, string) {
	return i.VpcAttributes.VPCID, i.VpcAttributes.VSwitchID
}

type lifecycleListedDisk struct {
	DiskID           string `json:"DiskId"`
	InstanceID       string `json:"InstanceId"`
	Type             string `json:"Type"`
	Size             int64  `json:"Size"`
	Category         string `json:"Category"`
	PerformanceLevel string `json:"PerformanceLevel"`
	Tags             struct {
		Tag []lifecycleECSTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type lifecycleListedENI struct {
	NetworkInterfaceID string `json:"NetworkInterfaceId"`
	InstanceID         string `json:"InstanceId"`
	PrivateIPAddress   string `json:"PrivateIpAddress"`
	Type               string `json:"Type"`
	VPCID              string `json:"VpcId"`
	VSwitchID          string `json:"VSwitchId"`
	Tags               struct {
		Tag []lifecycleECSTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type lifecycleListedSecurityGroup struct {
	SecurityGroupID string `json:"SecurityGroupId"`
	VPCID           string `json:"VpcId"`
	Tags            struct {
		Tag []lifecycleECSTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type lifecycleListedVPC struct {
	VPCID string `json:"VpcId"`
	Tags  struct {
		Tag []lifecycleVPCTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type lifecycleListedVSwitch struct {
	VSwitchID string `json:"VSwitchId"`
	VPCID     string `json:"VpcId"`
	Tags      struct {
		Tag []lifecycleVPCTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type lifecycleListedEIP struct {
	AllocationID string `json:"AllocationId"`
	IPAddress    string `json:"IpAddress"`
	InstanceID   string `json:"InstanceId"`
	Tags         struct {
		Tag []lifecycleVPCTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type lifecycleListedNATGateway struct {
	NATGatewayID string `json:"NatGatewayId"`
	VPCID        string `json:"VpcId"`
	Tags         struct {
		Tag []lifecycleVPCTagJSON `json:"Tag"`
	} `json:"Tags"`
}

type lifecycleListedRouteTable struct {
	RouteTableID string `json:"RouteTableId"`
	VPCID        string `json:"VpcId"`
}

type lifecycleListedRouteEntry struct {
	RouteEntryID         string `json:"RouteEntryId"`
	RouteTableID         string `json:"RouteTableId"`
	DestinationCIDRBlock string `json:"DestinationCidrBlock"`
	Origin               string `json:"Origin"`
	Type                 string `json:"Type"`
}

type lifecycleSecurityPermission struct {
	RuleID       string `json:"SecurityGroupRuleId"`
	Description  string `json:"Description"`
	IPProtocol   string `json:"IpProtocol"`
	PortRange    string `json:"PortRange"`
	SourceCIDRIP string `json:"SourceCidrIp"`
	DestCIDRIP   string `json:"DestCidrIp"`
}

func (a *OpenAPI) inventoryReady() bool {
	return a != nil && a.region == RegionHangzhou && a.ecs != nil && a.vpc != nil
}

// ListAssets exhaustively discovers tagged Lease roots and then traverses
// provider relationships so missing child tags cannot hide residual resources.
func (a *OpenAPI) ListAssets(ctx context.Context, query InventoryQuery) ([]LifecycleAsset, error) {
	if !a.inventoryReady() || query.Region != a.region || (query.LeaseID == "" && query.Repository == "") {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selector := map[string]string{cloudlease.TagManagedBy: cloudlease.ManagedByValue}
	if query.LeaseID != "" {
		selector[cloudlease.TagLeaseID] = query.LeaseID
	}
	if query.Repository != "" {
		selector[cloudlease.TagRepository] = query.Repository
	}

	instances, err := a.listLifecycleInstances(ctx, query.Region, selector)
	if err != nil || !lifecycleUnique(instances, func(value lifecycleListedInstance) string { return value.InstanceID }) {
		if err == nil {
			err = ErrAmbiguousInventory
		}
		return nil, err
	}
	disks, err := a.listLifecycleDisks(ctx, query.Region, selector, "")
	if err != nil || !lifecycleUnique(disks, func(value lifecycleListedDisk) string { return value.DiskID }) {
		if err == nil {
			err = ErrAmbiguousInventory
		}
		return nil, err
	}
	enis, err := a.listLifecycleENIs(ctx, query.Region, selector, "")
	if err != nil || !lifecycleUnique(enis, func(value lifecycleListedENI) string { return value.NetworkInterfaceID }) {
		if err == nil {
			err = ErrAmbiguousInventory
		}
		return nil, err
	}
	groups, err := a.listLifecycleSecurityGroups(ctx, query.Region, selector)
	if err != nil || !lifecycleUnique(groups, func(value lifecycleListedSecurityGroup) string { return value.SecurityGroupID }) {
		if err == nil {
			err = ErrAmbiguousInventory
		}
		return nil, err
	}
	vpcs, err := a.listLifecycleVPCs(ctx, query.Region, selector)
	if err != nil || !lifecycleUnique(vpcs, func(value lifecycleListedVPC) string { return value.VPCID }) {
		if err == nil {
			err = ErrAmbiguousInventory
		}
		return nil, err
	}
	vswitches, err := a.listLifecycleVSwitches(ctx, query.Region, selector)
	if err != nil || !lifecycleUnique(vswitches, func(value lifecycleListedVSwitch) string { return value.VSwitchID }) {
		if err == nil {
			err = ErrAmbiguousInventory
		}
		return nil, err
	}
	eips, err := a.listLifecycleEIPs(ctx, query.Region, selector)
	if err != nil || !lifecycleUnique(eips, func(value lifecycleListedEIP) string { return value.AllocationID }) {
		if err == nil {
			err = ErrAmbiguousInventory
		}
		return nil, err
	}

	inventory := newLifecycleInventory()
	for _, instance := range instances {
		tags := lifecycleECSTagsFromJSON(instance.Tags.Tag)
		vpcID, vSwitchID := instance.networkIDs()
		privateAddress := ""
		if len(instance.VpcAttributes.PrivateIP.IPAddress) == 1 {
			privateAddress = instance.VpcAttributes.PrivateIP.IPAddress[0]
		}
		asset := LifecycleAsset{ID: instance.InstanceID, Kind: ResourceKindInstance,
			Role: tags[cloudlease.TagResourceRole], ParentID: vSwitchID, Billable: true,
			PrivateAddress: privateAddress, Tags: tags,
			Attributes: map[string]string{"instance_type": instance.InstanceType, "image_id": instance.ImageID, "vpc_id": vpcID}}
		if err := inventory.addActual(asset); err != nil {
			return nil, err
		}
	}
	for _, disk := range disks {
		if err := inventory.addActual(lifecycleDiskAsset(disk, lifecycleECSTagsFromJSON(disk.Tags.Tag), false)); err != nil {
			return nil, err
		}
	}
	for _, eni := range enis {
		if err := inventory.addActual(lifecycleENIAsset(eni, lifecycleECSTagsFromJSON(eni.Tags.Tag), false)); err != nil {
			return nil, err
		}
	}
	for _, group := range groups {
		tags := lifecycleECSTagsFromJSON(group.Tags.Tag)
		if err := inventory.addActual(LifecycleAsset{ID: group.SecurityGroupID, Kind: ResourceKindSecurityGroup,
			Role: tags[cloudlease.TagResourceRole], ParentID: group.VPCID, Tags: tags}); err != nil {
			return nil, err
		}
	}
	for _, item := range vpcs {
		tags := lifecycleVPCTagsFromJSON(item.Tags.Tag)
		if err := inventory.addActual(LifecycleAsset{ID: item.VPCID, Kind: ResourceKindVPC,
			Role: tags[cloudlease.TagResourceRole], Tags: tags}); err != nil {
			return nil, err
		}
	}
	for _, item := range vswitches {
		tags := lifecycleVPCTagsFromJSON(item.Tags.Tag)
		if err := inventory.addActual(LifecycleAsset{ID: item.VSwitchID, Kind: ResourceKindVSwitch,
			Role: tags[cloudlease.TagResourceRole], ParentID: item.VPCID, Tags: tags}); err != nil {
			return nil, err
		}
	}
	for _, item := range eips {
		tags := lifecycleVPCTagsFromJSON(item.Tags.Tag)
		eip := LifecycleAsset{ID: item.AllocationID, Kind: ResourceKindEIP,
			Role: tags[cloudlease.TagResourceRole], Billable: true, PublicAddress: item.IPAddress, Tags: tags}
		if err := inventory.addActual(eip); err != nil {
			return nil, err
		}
		if item.InstanceID != "" {
			association := LifecycleAsset{ID: lifecycleRelationshipID(ResourceKindEIPAssociation, item.AllocationID, item.InstanceID),
				Kind: ResourceKindEIPAssociation, Role: eip.Role, ParentID: item.InstanceID, Tags: maps.Clone(tags),
				Attributes: map[string]string{"eip_id": item.AllocationID}}
			if err := inventory.addRelated(association); err != nil {
				return nil, err
			}
		}
	}

	for _, instance := range instances {
		parentTags := lifecycleECSTagsFromJSON(instance.Tags.Tag)
		relatedDisks, listErr := a.listLifecycleDisks(ctx, query.Region, nil, instance.InstanceID)
		if listErr != nil || !lifecycleUnique(relatedDisks, func(value lifecycleListedDisk) string { return value.DiskID }) {
			if listErr == nil {
				listErr = ErrAmbiguousInventory
			}
			return nil, listErr
		}
		for _, disk := range relatedDisks {
			tags, inherited := lifecycleRelationshipTags(lifecycleECSTagsFromJSON(disk.Tags.Tag), parentTags)
			if err := inventory.addRelated(lifecycleDiskAsset(disk, tags, inherited)); err != nil {
				return nil, err
			}
			if disk.Type == "data" {
				attachment := LifecycleAsset{ID: lifecycleRelationshipID(ResourceKindDiskAttachment, disk.DiskID, instance.InstanceID),
					Kind: ResourceKindDiskAttachment, Role: tags[cloudlease.TagResourceRole], ParentID: instance.InstanceID,
					Tags: maps.Clone(tags), Attributes: map[string]string{"disk_id": disk.DiskID}, IdentityInherited: inherited}
				if err := inventory.addRelated(attachment); err != nil {
					return nil, err
				}
			}
		}
		relatedENIs, listErr := a.listLifecycleENIs(ctx, query.Region, nil, instance.InstanceID)
		if listErr != nil || !lifecycleUnique(relatedENIs, func(value lifecycleListedENI) string { return value.NetworkInterfaceID }) {
			if listErr == nil {
				listErr = ErrAmbiguousInventory
			}
			return nil, listErr
		}
		for _, eni := range relatedENIs {
			tags, inherited := lifecycleRelationshipTags(lifecycleECSTagsFromJSON(eni.Tags.Tag), parentTags)
			if err := inventory.addRelated(lifecycleENIAsset(eni, tags, inherited)); err != nil {
				return nil, err
			}
		}
	}

	for _, group := range groups {
		groupTags := lifecycleECSTagsFromJSON(group.Tags.Tag)
		permissions, listErr := a.listLifecycleSecurityRules(ctx, group.SecurityGroupID)
		if listErr != nil || !lifecycleUnique(permissions, func(value lifecycleSecurityPermission) string { return value.RuleID }) {
			if listErr == nil {
				listErr = ErrAmbiguousInventory
			}
			return nil, listErr
		}
		for _, permission := range permissions {
			asset := lifecycleSecurityRuleAsset(group.SecurityGroupID, groupTags, permission)
			if err := inventory.addRelated(asset); err != nil {
				return nil, err
			}
		}
	}
	for _, item := range vpcs {
		parentTags := lifecycleVPCTagsFromJSON(item.Tags.Tag)
		natGateways, listErr := a.listLifecycleNATGateways(ctx, query.Region, item.VPCID)
		if listErr != nil || !lifecycleUnique(natGateways, func(value lifecycleListedNATGateway) string { return value.NATGatewayID }) {
			if listErr == nil {
				listErr = ErrAmbiguousInventory
			}
			return nil, listErr
		}
		for _, gateway := range natGateways {
			tags, inherited := lifecycleRelationshipTags(lifecycleVPCTagsFromJSON(gateway.Tags.Tag), parentTags)
			asset := LifecycleAsset{ID: gateway.NATGatewayID, Kind: ResourceKindNATGateway,
				Role: tags[cloudlease.TagResourceRole], ParentID: item.VPCID, Billable: true, Tags: tags, IdentityInherited: inherited}
			if err := inventory.addRelated(asset); err != nil {
				return nil, err
			}
		}
		routeTables, listErr := a.listLifecycleRouteTables(ctx, query.Region, item.VPCID)
		if listErr != nil || !lifecycleUnique(routeTables, func(value lifecycleListedRouteTable) string { return value.RouteTableID }) {
			if listErr == nil {
				listErr = ErrAmbiguousInventory
			}
			return nil, listErr
		}
		for _, routeTable := range routeTables {
			entries, entryErr := a.listLifecycleCustomRouteEntries(ctx, query.Region, routeTable.RouteTableID)
			if entryErr != nil || !lifecycleUnique(entries, func(value lifecycleListedRouteEntry) string { return value.RouteEntryID }) {
				if entryErr == nil {
					entryErr = ErrAmbiguousInventory
				}
				return nil, entryErr
			}
			for _, entry := range entries {
				if entry.Type != "Custom" && entry.Origin != "CustomCreate" {
					return nil, ErrAmbiguousInventory
				}
				asset := LifecycleAsset{ID: entry.RouteEntryID, Kind: ResourceKindRouteEntry,
					Role: parentTags[cloudlease.TagResourceRole], ParentID: routeTable.RouteTableID,
					Tags: maps.Clone(parentTags), IdentityInherited: true,
					Attributes: map[string]string{"destination_cidr": entry.DestinationCIDRBlock, "vpc_id": item.VPCID}}
				if err := inventory.addRelated(asset); err != nil {
					return nil, err
				}
			}
		}
	}
	return inventory.assets(), nil
}

type lifecycleInventory struct {
	items  map[string]LifecycleAsset
	actual map[string]bool
}

func newLifecycleInventory() *lifecycleInventory {
	return &lifecycleInventory{items: make(map[string]LifecycleAsset), actual: make(map[string]bool)}
}

func (i *lifecycleInventory) addActual(asset LifecycleAsset) error  { return i.add(asset, true) }
func (i *lifecycleInventory) addRelated(asset LifecycleAsset) error { return i.add(asset, false) }

func (i *lifecycleInventory) add(asset LifecycleAsset, actual bool) error {
	if asset.ID == "" || asset.Kind == "" {
		return ErrAmbiguousInventory
	}
	key := asset.Kind + "\x00" + asset.ID
	previous, exists := i.items[key]
	if !exists {
		i.items[key], i.actual[key] = asset, actual
		return nil
	}
	if actual && i.actual[key] {
		return ErrAmbiguousInventory
	}
	if actual {
		i.items[key], i.actual[key] = asset, true
		return nil
	}
	if previous.ParentID != "" && asset.ParentID != "" && previous.ParentID != asset.ParentID {
		return ErrAmbiguousInventory
	}
	return nil
}

func (i *lifecycleInventory) assets() []LifecycleAsset {
	result := make([]LifecycleAsset, 0, len(i.items))
	for _, asset := range i.items {
		result = append(result, asset)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Kind+"\x00"+result[left].ID < result[right].Kind+"\x00"+result[right].ID
	})
	return result
}

func lifecycleDiskAsset(disk lifecycleListedDisk, tags map[string]string, inherited bool) LifecycleAsset {
	return LifecycleAsset{ID: disk.DiskID, Kind: ResourceKindDisk, Role: tags[cloudlease.TagResourceRole],
		ParentID: disk.InstanceID, Billable: true, SizeBytes: disk.Size << 30, Tags: tags, IdentityInherited: inherited,
		Attributes: map[string]string{"disk_type": disk.Type, "category": disk.Category, "performance_level": disk.PerformanceLevel}}
}

func lifecycleENIAsset(eni lifecycleListedENI, tags map[string]string, inherited bool) LifecycleAsset {
	return LifecycleAsset{ID: eni.NetworkInterfaceID, Kind: ResourceKindENI, Role: tags[cloudlease.TagResourceRole],
		ParentID: eni.InstanceID, PrivateAddress: eni.PrivateIPAddress, Tags: tags, IdentityInherited: inherited,
		Attributes: map[string]string{"eni_type": eni.Type, "vpc_id": eni.VPCID, "vswitch_id": eni.VSwitchID}}
}

func lifecycleRelationshipTags(tags, parent map[string]string) (map[string]string, bool) {
	if tags[cloudlease.TagManagedBy] != "" {
		return tags, false
	}
	return maps.Clone(parent), true
}

func lifecycleRelationshipID(kind, childID, parentID string) string {
	return kind + ":" + childID + ":" + parentID
}

func lifecycleSecurityRuleAsset(groupID string, groupTags map[string]string, permission lifecycleSecurityPermission) LifecycleAsset {
	tags := maps.Clone(groupTags)
	asset := LifecycleAsset{ID: permission.RuleID, Kind: ResourceKindSecurityRule,
		Role: tags[cloudlease.TagResourceRole], ParentID: groupID, Tags: tags, IdentityInherited: true,
		Attributes: map[string]string{"rule_kind": "unknown"}}
	decoded, ok := parseSecurityRuleDescription(permission.Description)
	if !ok || !lifecyclePermissionMatchesDescription(permission, decoded) {
		return asset
	}
	asset.Role = decoded.TargetRole
	asset.Tags[cloudlease.TagResourceRole] = decoded.TargetRole
	asset.Attributes["rule_kind"] = decoded.Kind
	asset.IdentityInherited = false
	if decoded.Kind == string(AccessRuleGrant) {
		source, _ := netip.ParsePrefix(decoded.Source)
		asset.Grant = &cloudlease.AccessGrant{ID: decoded.ID, TargetRole: decoded.TargetRole,
			Protocol: cloudlease.Protocol(decoded.Protocol), PortFrom: decoded.PortFrom, PortTo: decoded.PortTo,
			SourcePrefix: source, Until: time.Unix(decoded.UntilUnix, int64(decoded.UntilNanosecond)).UTC()}
	}
	return asset
}

func lifecyclePermissionMatchesDescription(permission lifecycleSecurityPermission, decoded encodedSecurityRule) bool {
	from, to, ok := parseLifecyclePortRange(permission.PortRange)
	return ok && strings.EqualFold(permission.IPProtocol, decoded.Protocol) && from == decoded.PortFrom && to == decoded.PortTo &&
		permission.SourceCIDRIP == decoded.Source && permission.DestCIDRIP == decoded.Destination
}

func parseLifecyclePortRange(value string) (uint16, uint16, bool) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return 0, 0, false
	}
	from, errFrom := strconv.ParseUint(parts[0], 10, 16)
	to, errTo := strconv.ParseUint(parts[1], 10, 16)
	return uint16(from), uint16(to), errFrom == nil && errTo == nil && from > 0 && to >= from
}

func (a *OpenAPI) listLifecycleSecurityRules(ctx context.Context, securityGroupID string) ([]lifecycleSecurityPermission, error) {
	return lifecycleCollectTokenPages(ctx, func(nextToken string) ([]lifecycleSecurityPermission, string, error) {
		request := (&ecs.DescribeSecurityGroupAttributeRequest{}).SetRegionId(a.region).
			SetSecurityGroupId(securityGroupID).SetDirection("ingress").SetMaxResults(500)
		if nextToken != "" {
			request.SetNextToken(nextToken)
		}
		response, err := a.ecs.DescribeSecurityGroupAttribute(request)
		if err != nil || response == nil || response.Body == nil {
			return nil, "", errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			NextToken   string `json:"NextToken"`
			Permissions struct {
				Permission []lifecycleSecurityPermission `json:"Permission"`
			} `json:"Permissions"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, "", err
		}
		return body.Permissions.Permission, body.NextToken, nil
	})
}

func (a *OpenAPI) listLifecycleInstances(ctx context.Context, region string, tags map[string]string) ([]lifecycleListedInstance, error) {
	return lifecycleCollectPages(ctx, func(page int32) ([]lifecycleListedInstance, int, error) {
		response, err := a.ecs.DescribeInstances((&ecs.DescribeInstancesRequest{}).SetRegionId(region).
			SetPageNumber(page).SetPageSize(lifecycleInventoryPageSize).SetTag(describeLifecycleInstanceTags(tags)))
		if err != nil || response == nil || response.Body == nil {
			return nil, 0, errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			TotalCount int `json:"TotalCount"`
			Instances  struct {
				Instance []lifecycleListedInstance `json:"Instance"`
			} `json:"Instances"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, 0, err
		}
		return body.Instances.Instance, body.TotalCount, nil
	})
}

func (a *OpenAPI) listLifecycleDisks(ctx context.Context, region string, tags map[string]string, instanceID string) ([]lifecycleListedDisk, error) {
	return lifecycleCollectPages(ctx, func(page int32) ([]lifecycleListedDisk, int, error) {
		request := (&ecs.DescribeDisksRequest{}).SetRegionId(region).SetPageNumber(page).SetPageSize(lifecycleInventoryPageSize)
		if len(tags) > 0 {
			request.SetTag(describeLifecycleDiskTags(tags))
		}
		if instanceID != "" {
			request.SetInstanceId(instanceID)
		}
		response, err := a.ecs.DescribeDisks(request)
		if err != nil || response == nil || response.Body == nil {
			return nil, 0, errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			TotalCount int `json:"TotalCount"`
			Disks      struct {
				Disk []lifecycleListedDisk `json:"Disk"`
			} `json:"Disks"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, 0, err
		}
		return body.Disks.Disk, body.TotalCount, nil
	})
}

func (a *OpenAPI) listLifecycleENIs(ctx context.Context, region string, tags map[string]string, instanceID string) ([]lifecycleListedENI, error) {
	return lifecycleCollectTokenPages(ctx, func(nextToken string) ([]lifecycleListedENI, string, error) {
		request := (&ecs.DescribeNetworkInterfacesRequest{}).SetRegionId(region).SetMaxResults(100)
		if len(tags) > 0 {
			request.SetTag(describeLifecycleENITags(tags))
		}
		if instanceID != "" {
			request.SetInstanceId(instanceID)
		}
		if nextToken != "" {
			request.SetNextToken(nextToken)
		}
		response, err := a.ecs.DescribeNetworkInterfaces(request)
		if err != nil || response == nil || response.Body == nil {
			return nil, "", errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			NextToken            string `json:"NextToken"`
			NetworkInterfaceSets struct {
				NetworkInterfaceSet []lifecycleListedENI `json:"NetworkInterfaceSet"`
			} `json:"NetworkInterfaceSets"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, "", err
		}
		return body.NetworkInterfaceSets.NetworkInterfaceSet, body.NextToken, nil
	})
}

func (a *OpenAPI) listLifecycleSecurityGroups(ctx context.Context, region string, tags map[string]string) ([]lifecycleListedSecurityGroup, error) {
	return lifecycleCollectPages(ctx, func(page int32) ([]lifecycleListedSecurityGroup, int, error) {
		response, err := a.ecs.DescribeSecurityGroups((&ecs.DescribeSecurityGroupsRequest{}).SetRegionId(region).
			SetPageNumber(page).SetPageSize(lifecycleInventoryPageSize).SetTag(describeLifecycleSecurityGroupTags(tags)))
		if err != nil || response == nil || response.Body == nil {
			return nil, 0, errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			TotalCount     int `json:"TotalCount"`
			SecurityGroups struct {
				SecurityGroup []lifecycleListedSecurityGroup `json:"SecurityGroup"`
			} `json:"SecurityGroups"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, 0, err
		}
		return body.SecurityGroups.SecurityGroup, body.TotalCount, nil
	})
}

func (a *OpenAPI) listLifecycleVPCs(ctx context.Context, region string, tags map[string]string) ([]lifecycleListedVPC, error) {
	return lifecycleCollectPages(ctx, func(page int32) ([]lifecycleListedVPC, int, error) {
		response, err := a.vpc.DescribeVpcs((&vpc.DescribeVpcsRequest{}).SetRegionId(region).
			SetPageNumber(page).SetPageSize(lifecycleVPCPageSize).SetTag(describeLifecycleVPCTags(tags)))
		if err != nil || response == nil || response.Body == nil {
			return nil, 0, errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			TotalCount int `json:"TotalCount"`
			Vpcs       struct {
				VPC []lifecycleListedVPC `json:"Vpc"`
			} `json:"Vpcs"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, 0, err
		}
		return body.Vpcs.VPC, body.TotalCount, nil
	})
}

func (a *OpenAPI) listLifecycleVSwitches(ctx context.Context, region string, tags map[string]string) ([]lifecycleListedVSwitch, error) {
	return lifecycleCollectPages(ctx, func(page int32) ([]lifecycleListedVSwitch, int, error) {
		response, err := a.vpc.DescribeVSwitches((&vpc.DescribeVSwitchesRequest{}).SetRegionId(region).
			SetPageNumber(page).SetPageSize(lifecycleVPCPageSize).SetTag(describeLifecycleVSwitchTags(tags)))
		if err != nil || response == nil || response.Body == nil {
			return nil, 0, errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			TotalCount int `json:"TotalCount"`
			VSwitches  struct {
				VSwitch []lifecycleListedVSwitch `json:"VSwitch"`
			} `json:"VSwitches"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, 0, err
		}
		return body.VSwitches.VSwitch, body.TotalCount, nil
	})
}

func (a *OpenAPI) listLifecycleEIPs(ctx context.Context, region string, tags map[string]string) ([]lifecycleListedEIP, error) {
	return lifecycleCollectPages(ctx, func(page int32) ([]lifecycleListedEIP, int, error) {
		response, err := a.vpc.DescribeEipAddresses((&vpc.DescribeEipAddressesRequest{}).SetRegionId(region).
			SetPageNumber(page).SetPageSize(lifecycleVPCPageSize).SetTag(describeLifecycleEIPTags(tags)))
		if err != nil || response == nil || response.Body == nil {
			return nil, 0, errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			TotalCount   int `json:"TotalCount"`
			EIPAddresses struct {
				EIPAddress []lifecycleListedEIP `json:"EipAddress"`
			} `json:"EipAddresses"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, 0, err
		}
		return body.EIPAddresses.EIPAddress, body.TotalCount, nil
	})
}

func (a *OpenAPI) listLifecycleNATGateways(ctx context.Context, region, vpcID string) ([]lifecycleListedNATGateway, error) {
	return lifecycleCollectPages(ctx, func(page int32) ([]lifecycleListedNATGateway, int, error) {
		response, err := a.vpc.DescribeNatGateways((&vpc.DescribeNatGatewaysRequest{}).SetRegionId(region).
			SetVpcId(vpcID).SetPageNumber(page).SetPageSize(lifecycleVPCPageSize))
		if err != nil || response == nil || response.Body == nil {
			return nil, 0, errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			TotalCount  int `json:"TotalCount"`
			NATGateways struct {
				NATGateway []lifecycleListedNATGateway `json:"NatGateway"`
			} `json:"NatGateways"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, 0, err
		}
		return body.NATGateways.NATGateway, body.TotalCount, nil
	})
}

func (a *OpenAPI) listLifecycleRouteTables(ctx context.Context, region, vpcID string) ([]lifecycleListedRouteTable, error) {
	return lifecycleCollectPages(ctx, func(page int32) ([]lifecycleListedRouteTable, int, error) {
		response, err := a.vpc.DescribeRouteTableList((&vpc.DescribeRouteTableListRequest{}).SetRegionId(region).
			SetVpcId(vpcID).SetPageNumber(page).SetPageSize(lifecycleVPCPageSize))
		if err != nil || response == nil || response.Body == nil {
			return nil, 0, errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			TotalCount      int `json:"TotalCount"`
			RouterTableList struct {
				Items []lifecycleListedRouteTable `json:"RouterTableListType"`
			} `json:"RouterTableList"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, 0, err
		}
		return body.RouterTableList.Items, body.TotalCount, nil
	})
}

func (a *OpenAPI) listLifecycleCustomRouteEntries(ctx context.Context, region, routeTableID string) ([]lifecycleListedRouteEntry, error) {
	return lifecycleCollectTokenPages(ctx, func(nextToken string) ([]lifecycleListedRouteEntry, string, error) {
		request := (&vpc.DescribeRouteEntryListRequest{}).SetRegionId(region).SetRouteTableId(routeTableID).
			SetRouteEntryType("Custom").SetMaxResult(100)
		if nextToken != "" {
			request.SetNextToken(nextToken)
		}
		response, err := a.vpc.DescribeRouteEntryList(request)
		if err != nil || response == nil || response.Body == nil {
			return nil, "", errors.Join(ErrAmbiguousInventory, err)
		}
		var body struct {
			NextToken   string `json:"NextToken"`
			RouteEntrys struct {
				RouteEntry []lifecycleListedRouteEntry `json:"RouteEntry"`
			} `json:"RouteEntrys"`
		}
		if err := decodeLifecycleSDKBody(response.Body, &body); err != nil {
			return nil, "", err
		}
		return body.RouteEntrys.RouteEntry, body.NextToken, nil
	})
}

func lifecycleCollectPages[T any](ctx context.Context, fetch func(int32) ([]T, int, error)) ([]T, error) {
	result := make([]T, 0)
	expectedTotal := -1
	for page := int32(1); page <= 10000; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, total, err := fetch(page)
		if err != nil {
			return nil, err
		}
		if total < 0 || total < len(result)+len(items) || expectedTotal >= 0 && total != expectedTotal {
			return nil, ErrAmbiguousInventory
		}
		if expectedTotal < 0 {
			expectedTotal = total
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

func lifecycleCollectTokenPages[T any](ctx context.Context, fetch func(string) ([]T, string, error)) ([]T, error) {
	result := make([]T, 0)
	next := ""
	seen := make(map[string]struct{})
	for page := 0; page < 10000; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items, following, err := fetch(next)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if following == "" {
			return result, nil
		}
		if following == next {
			return nil, ErrAmbiguousInventory
		}
		if _, exists := seen[following]; exists {
			return nil, ErrAmbiguousInventory
		}
		seen[following] = struct{}{}
		next = following
	}
	return nil, ErrAmbiguousInventory
}

func lifecycleUnique[T any](items []T, identity func(T) string) bool {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		id := identity(item)
		if id == "" {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func lifecycleECSTagsFromJSON(tags []lifecycleECSTagJSON) map[string]string {
	result := make(map[string]string, len(tags))
	for _, tag := range tags {
		result[tag.Key] = tag.Value
	}
	return result
}

func lifecycleVPCTagsFromJSON(tags []lifecycleVPCTagJSON) map[string]string {
	result := make(map[string]string, len(tags))
	for _, tag := range tags {
		result[tag.Key] = tag.Value
	}
	return result
}

func describeLifecycleInstanceTags(tags map[string]string) []*ecs.DescribeInstancesRequestTag {
	result := make([]*ecs.DescribeInstancesRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&ecs.DescribeInstancesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}
func describeLifecycleDiskTags(tags map[string]string) []*ecs.DescribeDisksRequestTag {
	result := make([]*ecs.DescribeDisksRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&ecs.DescribeDisksRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}
func describeLifecycleENITags(tags map[string]string) []*ecs.DescribeNetworkInterfacesRequestTag {
	result := make([]*ecs.DescribeNetworkInterfacesRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&ecs.DescribeNetworkInterfacesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}
func describeLifecycleSecurityGroupTags(tags map[string]string) []*ecs.DescribeSecurityGroupsRequestTag {
	result := make([]*ecs.DescribeSecurityGroupsRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&ecs.DescribeSecurityGroupsRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}
func describeLifecycleVPCTags(tags map[string]string) []*vpc.DescribeVpcsRequestTag {
	result := make([]*vpc.DescribeVpcsRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&vpc.DescribeVpcsRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}
func describeLifecycleVSwitchTags(tags map[string]string) []*vpc.DescribeVSwitchesRequestTag {
	result := make([]*vpc.DescribeVSwitchesRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&vpc.DescribeVSwitchesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}
func describeLifecycleEIPTags(tags map[string]string) []*vpc.DescribeEipAddressesRequestTag {
	result := make([]*vpc.DescribeEipAddressesRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&vpc.DescribeEipAddressesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}
