package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v7/client"
	"github.com/alibabacloud-go/tea/dara"
	vpc "github.com/alibabacloud-go/vpc-20160428/v6/client"
)

func createLifecycleVPCTags(tags map[string]string) []*vpc.CreateVpcRequestTag {
	result := make([]*vpc.CreateVpcRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&vpc.CreateVpcRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func createLifecycleVSwitchTags(tags map[string]string) []*vpc.CreateVSwitchRequestTag {
	result := make([]*vpc.CreateVSwitchRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&vpc.CreateVSwitchRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func createLifecycleSecurityGroupTags(tags map[string]string) []*ecs.CreateSecurityGroupRequestTag {
	result := make([]*ecs.CreateSecurityGroupRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&ecs.CreateSecurityGroupRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func runLifecycleInstanceTags(tags map[string]string) []*ecs.RunInstancesRequestTag {
	result := make([]*ecs.RunInstancesRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&ecs.RunInstancesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func allocateLifecycleEIPTags(tags map[string]string) []*vpc.AllocateEipAddressRequestTag {
	result := make([]*vpc.AllocateEipAddressRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&vpc.AllocateEipAddressRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func lifecycleECSTags(tags map[string]string) []*ecs.TagResourcesRequestTag {
	result := make([]*ecs.TagResourcesRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&ecs.TagResourcesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func lifecycleVPCTags(tags map[string]string) []*vpc.TagResourcesRequestTag {
	result := make([]*vpc.TagResourcesRequestTag, 0, len(tags))
	for _, pair := range sortedLifecycleTagPairs(tags) {
		result = append(result, (&vpc.TagResourcesRequestTag{}).SetKey(pair[0]).SetValue(pair[1]))
	}
	return result
}

func (a *OpenAPI) waitVPCAvailable(ctx context.Context, region, vpcID string) error {
	deadline := time.Now().Add(lifecycleSDKWaitTimeout)
	var lastErr error
	for {
		response, err := a.vpc.DescribeVpcAttribute((&vpc.DescribeVpcAttributeRequest{}).SetRegionId(region).SetVpcId(vpcID))
		if err == nil && response != nil && response.Body != nil && stringValue(response.Body.Status) == "Available" {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return errors.Join(ErrAmbiguousInventory, lastErr)
		}
		if err := waitContext(ctx, lifecycleSDKPollInterval); err != nil {
			return err
		}
	}
}

func (a *OpenAPI) waitVSwitchAvailable(ctx context.Context, region, vswitchID string) error {
	deadline := time.Now().Add(lifecycleSDKWaitTimeout)
	var lastErr error
	for {
		response, err := a.vpc.DescribeVSwitchAttributes((&vpc.DescribeVSwitchAttributesRequest{}).
			SetRegionId(region).SetVSwitchId(vswitchID))
		if err == nil && response != nil && response.Body != nil && stringValue(response.Body.Status) == "Available" {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return errors.Join(ErrAmbiguousInventory, lastErr)
		}
		if err := waitContext(ctx, lifecycleSDKPollInterval); err != nil {
			return err
		}
	}
}

type createdInstanceJSON struct {
	InstanceID    string `json:"InstanceId"`
	Status        string `json:"Status"`
	VpcAttributes struct {
		PrivateIP struct {
			IPAddress []string `json:"IpAddress"`
		} `json:"PrivateIpAddress"`
	} `json:"VpcAttributes"`
}

type createdDiskJSON struct {
	DiskID           string `json:"DiskId"`
	InstanceID       string `json:"InstanceId"`
	Type             string `json:"Type"`
	Category         string `json:"Category"`
	PerformanceLevel string `json:"PerformanceLevel"`
	Size             int32  `json:"Size"`
}

type createdENIJSON struct {
	NetworkInterfaceID string `json:"NetworkInterfaceId"`
	InstanceID         string `json:"InstanceId"`
	PrivateIPAddress   string `json:"PrivateIpAddress"`
	Type               string `json:"Type"`
}

func (a *OpenAPI) waitCreatedHostAssets(ctx context.Context, request HostCreateRequest, instanceID string, tags map[string]string) ([]LifecycleAsset, error) {
	deadline := time.Now().Add(lifecycleSDKWaitTimeout)
	var lastErr error
	for {
		instance, disks, eni, err := a.describeCreatedHost(request.Region, instanceID)
		if err == nil && instance.InstanceID == instanceID && len(instance.VpcAttributes.PrivateIP.IPAddress) == 1 &&
			len(disks) == 2 && eni.NetworkInterfaceID != "" && eni.InstanceID == instanceID {
			var systemDisk, dataDisk createdDiskJSON
			for _, disk := range disks {
				switch disk.Type {
				case "system":
					systemDisk = disk
				case "data":
					dataDisk = disk
				}
			}
			if validCreatedDisk(systemDisk, request.SystemDiskGiB) && validCreatedDisk(dataDisk, request.DataDiskGiB) {
				if tagErr := a.tagECSResourceIDs(request.Region, "disk", []string{systemDisk.DiskID, dataDisk.DiskID}, tags); tagErr != nil {
					return nil, tagErr
				}
				if tagErr := a.tagECSResourceIDs(request.Region, "eni", []string{eni.NetworkInterfaceID}, tags); tagErr != nil {
					return nil, tagErr
				}
				privateAddress := instance.VpcAttributes.PrivateIP.IPAddress[0]
				return []LifecycleAsset{
					{ID: instanceID, Kind: ResourceKindInstance, Role: request.Role, ParentID: request.VSwitchID,
						Billable: true, PrivateAddress: privateAddress, Tags: maps.Clone(tags),
						Attributes: map[string]string{"instance_type": request.InstanceType, "image_id": request.ImageID, "ordinal": strconv.Itoa(request.Ordinal)}},
					{ID: systemDisk.DiskID, Kind: ResourceKindDisk, Role: request.Role, ParentID: instanceID,
						Billable: true, SizeBytes: int64(systemDisk.Size) << 30, Tags: maps.Clone(tags), Attributes: map[string]string{"disk_type": "system"}},
					{ID: dataDisk.DiskID, Kind: ResourceKindDisk, Role: request.Role, ParentID: instanceID,
						Billable: true, SizeBytes: int64(dataDisk.Size) << 30, Tags: maps.Clone(tags), Attributes: map[string]string{"disk_type": "data"}},
					{ID: "disk-attachment:" + dataDisk.DiskID + ":" + instanceID, Kind: ResourceKindDiskAttachment,
						Role: request.Role, ParentID: instanceID, Tags: maps.Clone(tags), Attributes: map[string]string{"disk_id": dataDisk.DiskID}},
					{ID: eni.NetworkInterfaceID, Kind: ResourceKindENI, Role: request.Role, ParentID: instanceID,
						PrivateAddress: eni.PrivateIPAddress, Tags: maps.Clone(tags), Attributes: map[string]string{"eni_type": eni.Type}},
				}, nil
			}
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, errors.Join(ErrAmbiguousInventory, lastErr)
		}
		if err := waitContext(ctx, lifecycleSDKPollInterval); err != nil {
			return nil, err
		}
	}
}

func validCreatedDisk(disk createdDiskJSON, sizeGiB int) bool {
	return disk.DiskID != "" && disk.InstanceID != "" && disk.Category == providerDiskESSD &&
		disk.PerformanceLevel == providerDiskLevelPL0 && disk.Size == int32(sizeGiB)
}

func (a *OpenAPI) describeCreatedHost(region, instanceID string) (createdInstanceJSON, []createdDiskJSON, createdENIJSON, error) {
	instanceIDs, _ := json.Marshal([]string{instanceID})
	instanceResponse, err := a.ecs.DescribeInstances((&ecs.DescribeInstancesRequest{}).
		SetRegionId(region).SetInstanceIds(string(instanceIDs)).SetPageNumber(1).SetPageSize(10))
	if err != nil || instanceResponse == nil || instanceResponse.Body == nil {
		return createdInstanceJSON{}, nil, createdENIJSON{}, err
	}
	var instanceBody struct {
		Instances struct {
			Instance []createdInstanceJSON `json:"Instance"`
		} `json:"Instances"`
	}
	if err := decodeLifecycleSDKBody(instanceResponse.Body, &instanceBody); err != nil || len(instanceBody.Instances.Instance) != 1 {
		return createdInstanceJSON{}, nil, createdENIJSON{}, errors.Join(ErrAmbiguousInventory, err)
	}
	diskResponse, err := a.ecs.DescribeDisks((&ecs.DescribeDisksRequest{}).
		SetRegionId(region).SetInstanceId(instanceID).SetPageNumber(1).SetPageSize(100))
	if err != nil || diskResponse == nil || diskResponse.Body == nil {
		return createdInstanceJSON{}, nil, createdENIJSON{}, err
	}
	var diskBody struct {
		Disks struct {
			Disk []createdDiskJSON `json:"Disk"`
		} `json:"Disks"`
	}
	if err := decodeLifecycleSDKBody(diskResponse.Body, &diskBody); err != nil {
		return createdInstanceJSON{}, nil, createdENIJSON{}, err
	}
	eniResponse, err := a.ecs.DescribeNetworkInterfaces((&ecs.DescribeNetworkInterfacesRequest{}).
		SetRegionId(region).SetInstanceId(instanceID).SetMaxResults(100))
	if err != nil || eniResponse == nil || eniResponse.Body == nil {
		return createdInstanceJSON{}, nil, createdENIJSON{}, err
	}
	var eniBody struct {
		NetworkInterfaceSets struct {
			NetworkInterfaceSet []createdENIJSON `json:"NetworkInterfaceSet"`
		} `json:"NetworkInterfaceSets"`
	}
	if err := decodeLifecycleSDKBody(eniResponse.Body, &eniBody); err != nil || len(eniBody.NetworkInterfaceSets.NetworkInterfaceSet) != 1 {
		return createdInstanceJSON{}, nil, createdENIJSON{}, errors.Join(ErrAmbiguousInventory, err)
	}
	return instanceBody.Instances.Instance[0], diskBody.Disks.Disk, eniBody.NetworkInterfaceSets.NetworkInterfaceSet[0], nil
}

func (a *OpenAPI) tagECSResourceIDs(region, resourceType string, ids []string, tags map[string]string) error {
	resourceIDs := make([]*string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return ErrInvalidConfig
		}
		resourceIDs = append(resourceIDs, dara.String(id))
	}
	_, err := a.ecs.TagResources((&ecs.TagResourcesRequest{}).
		SetRegionId(region).SetResourceType(resourceType).SetResourceId(resourceIDs).SetTag(lifecycleECSTags(tags)))
	return err
}

func (a *OpenAPI) tagLifecycleAssets(region string, assets []LifecycleAsset, tags map[string]string) error {
	ecsGroups := make(map[string][]string)
	vpcGroups := make(map[string][]string)
	for _, asset := range assets {
		switch asset.Kind {
		case ResourceKindInstance:
			ecsGroups["instance"] = append(ecsGroups["instance"], asset.ID)
		case ResourceKindDisk:
			ecsGroups["disk"] = append(ecsGroups["disk"], asset.ID)
		case ResourceKindENI:
			ecsGroups["eni"] = append(ecsGroups["eni"], asset.ID)
		case ResourceKindSecurityGroup:
			ecsGroups["securitygroup"] = append(ecsGroups["securitygroup"], asset.ID)
		case ResourceKindVPC:
			vpcGroups["VPC"] = append(vpcGroups["VPC"], asset.ID)
		case ResourceKindVSwitch:
			vpcGroups["VSWITCH"] = append(vpcGroups["VSWITCH"], asset.ID)
		case ResourceKindEIP:
			vpcGroups["EIP"] = append(vpcGroups["EIP"], asset.ID)
		}
	}
	var errs []error
	for resourceType, ids := range ecsGroups {
		for start := 0; start < len(ids); start += 50 {
			end := min(start+50, len(ids))
			if err := a.tagECSResourceIDs(region, resourceType, ids[start:end], tags); err != nil {
				errs = append(errs, err)
			}
		}
	}
	for resourceType, ids := range vpcGroups {
		for start := 0; start < len(ids); start += 50 {
			end := min(start+50, len(ids))
			resourceIDs := make([]*string, 0, end-start)
			for _, id := range ids[start:end] {
				resourceIDs = append(resourceIDs, dara.String(id))
			}
			if _, err := a.vpc.TagResources((&vpc.TagResourcesRequest{}).
				SetRegionId(region).SetResourceType(resourceType).SetResourceId(resourceIDs).SetTag(lifecycleVPCTags(tags))); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func decodeLifecycleSDKBody(input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode SDK body: %w", err)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("decode SDK body: %w", err)
	}
	return nil
}
