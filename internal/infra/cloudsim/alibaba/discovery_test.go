package alibaba

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestDiscoverConfigSelectsFirstCompleteSpotZone(t *testing.T) {
	api := &discoveryAPIStub{
		accountIDHash: "sha256:account",
		zones:         []string{"cn-hangzhou-a", "cn-hangzhou-b"},
		imageID:       "aliyun-linux-3-x86_64",
		instanceTypes: map[resourceShape][]InstanceTypeCandidate{
			{cpu: 2, memory: 4}: {
				{ID: "ecs.g8i.large", CPUArchitecture: "X86", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8},
				{ID: "ecs.c8i.large", CPUArchitecture: "x86_64", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8},
				{ID: "ecs.g7.large", CPUArchitecture: "amd64", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8},
				{ID: "ecs.g6.large", CPUArchitecture: "ARM", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8},
				{ID: "ecs.gn7i.large", CPUArchitecture: "X86", GPUAmount: 1, FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8},
			},
			{cpu: 4, memory: 8}: {
				{ID: "ecs.g8i.xlarge", CPUArchitecture: "X86", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8},
			},
			{cpu: 8, memory: 16}: {
				{ID: "ecs.g8i.2xlarge", CPUArchitecture: "X86", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8},
			},
		},
		available: map[string]bool{
			"cn-hangzhou-a/ecs.g8i.large":   true,
			"cn-hangzhou-a/ecs.c8i.large":   true,
			"cn-hangzhou-a/ecs.g7.large":    true,
			"cn-hangzhou-a/ecs.g8i.xlarge":  true,
			"cn-hangzhou-b/ecs.g8i.large":   true,
			"cn-hangzhou-b/ecs.c8i.large":   true,
			"cn-hangzhou-b/ecs.g7.large":    true,
			"cn-hangzhou-b/ecs.g8i.xlarge":  true,
			"cn-hangzhou-b/ecs.g8i.2xlarge": true,
		},
	}

	config, err := DiscoverConfig(context.Background(), api, "cn-hangzhou")
	if err != nil {
		t.Fatalf("DiscoverConfig() error = %v", err)
	}
	if config.Region != "cn-hangzhou" || config.ZoneID != "cn-hangzhou-b" || config.ImageID != "aliyun-linux-3-x86_64" || config.AccountIDHash != "sha256:account" {
		t.Fatalf("identity config = %#v", config)
	}
	if got, want := config.Presets["small"].InstanceTypes, []string{"ecs.g8i.large", "ecs.g7.large", "ecs.c8i.large"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("small instance types = %v, want %v", got, want)
	}
	if got, want := config.Presets["standard"].InstanceTypes, []string{"ecs.g8i.xlarge"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("standard instance types = %v, want %v", got, want)
	}
	if got, want := config.Presets["stress"].InstanceTypes, []string{"ecs.g8i.2xlarge"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stress instance types = %v, want %v", got, want)
	}
	if config.VPCIPv4CIDR != "10.42.0.0/16" || config.VSwitchIPv4CIDR != "10.42.0.0/24" || config.PrivateIPv4["sim"] != "10.42.0.20" {
		t.Fatalf("network defaults = %#v", config)
	}
	if got, want := config.SimulatorSourceIPv4, []string{"10.42.0.20", "10.42.0.21", "10.42.0.22"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("simulator source addresses = %v, want %v", got, want)
	}
	if config.SystemDiskCategory != "cloud_essd" || config.SystemDiskSizeGiB != 40 || config.DataDiskCategory != "cloud_essd" || config.DataDiskSizeGiB != 100 || config.PublicBandwidthMbps != 20 {
		t.Fatalf("storage or bandwidth defaults = %#v", config)
	}
}

func TestDiscoverConfigRejectsIncompleteInventory(t *testing.T) {
	api := &discoveryAPIStub{
		accountIDHash: "sha256:account",
		zones:         []string{"cn-hangzhou-a"},
		imageID:       "aliyun-linux-3-x86_64",
		instanceTypes: map[resourceShape][]InstanceTypeCandidate{
			{cpu: 2, memory: 4}:  {{ID: "ecs.g8i.large", CPUArchitecture: "X86", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8}},
			{cpu: 4, memory: 8}:  {{ID: "ecs.g8i.xlarge", CPUArchitecture: "X86", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8}},
			{cpu: 8, memory: 16}: {{ID: "ecs.g8i.2xlarge", CPUArchitecture: "X86", FamilyLevel: "EnterpriseLevel", PrivateIPv4Capacity: 8}},
		},
		available: map[string]bool{
			"cn-hangzhou-a/ecs.g8i.large":  true,
			"cn-hangzhou-a/ecs.g8i.xlarge": true,
		},
	}

	if _, err := DiscoverConfig(context.Background(), api, "cn-hangzhou"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("DiscoverConfig() error = %v, want ErrInvalidConfig", err)
	}
}

type resourceShape struct {
	cpu    int32
	memory int32
}

type discoveryAPIStub struct {
	accountIDHash string
	zones         []string
	imageID       string
	instanceTypes map[resourceShape][]InstanceTypeCandidate
	available     map[string]bool
}

func (s *discoveryAPIStub) AccountIDHash(context.Context) (string, error) {
	return s.accountIDHash, nil
}

func (s *discoveryAPIStub) EligibleSpotZones(context.Context, string) ([]string, error) {
	return append([]string(nil), s.zones...), nil
}

func (s *discoveryAPIStub) LatestLinuxImage(context.Context, string) (string, error) {
	return s.imageID, nil
}

func (s *discoveryAPIStub) InstanceTypes(_ context.Context, _ string, cpu, memory int32) ([]InstanceTypeCandidate, error) {
	return append([]InstanceTypeCandidate(nil), s.instanceTypes[resourceShape{cpu: cpu, memory: memory}]...), nil
}

func (s *discoveryAPIStub) InstanceTypeAvailable(_ context.Context, region, zone, instanceType string) (bool, error) {
	return s.available[zone+"/"+instanceType], nil
}
