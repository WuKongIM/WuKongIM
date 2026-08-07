package chatlifecycle

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestDiskObserverSelectsExactMountpointAndDevice(t *testing.T) {
	metrics := `
node_filesystem_size_bytes{device="/dev/other",fstype="ext4",mountpoint="/data"} 999
node_filesystem_avail_bytes{mountpoint="/data",device="/dev/other",fstype="ext4"} 888
node_filesystem_size_bytes{fstype="xfs",mountpoint="/var/lib/wukongim",device="/dev/wukongim-data"} 1e+12
node_filesystem_avail_bytes{device="/dev/wukongim-data",mountpoint="/var/lib/wukongim",fstype="xfs"} 9.5e+11
node_filesystem_size_bytes{device="/dev/wukongim-data",mountpoint="/shadow"} 777
node_filesystem_avail_bytes{device="/dev/wukongim-data",mountpoint="/shadow"} 666
`
	filesystem, err := parseNodeExporterFilesystem(strings.NewReader(metrics), "/var/lib/wukongim", "/dev/wukongim-data")
	if err != nil {
		t.Fatalf("parseNodeExporterFilesystem() error = %v", err)
	}
	if filesystem.SizeBytes != 1_000_000_000_000 || filesystem.AvailableBytes != 950_000_000_000 {
		t.Fatalf("filesystem = %+v", filesystem)
	}
}

func TestDiskObserverRejectsMissingAndAmbiguousExactMatches(t *testing.T) {
	tests := []struct {
		name    string
		metrics string
		want    error
	}{
		{
			name: "missing available",
			metrics: `node_filesystem_size_bytes{device="/dev/data",mountpoint="/data"} 1000
`,
			want: ErrDiskMissing,
		},
		{
			name: "duplicate size",
			metrics: `node_filesystem_size_bytes{device="/dev/data",mountpoint="/data",fstype="xfs"} 1000
node_filesystem_size_bytes{fstype="xfs",mountpoint="/data",device="/dev/data"} 1000
node_filesystem_avail_bytes{device="/dev/data",mountpoint="/data"} 900
`,
			want: ErrDiskAmbiguous,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseNodeExporterFilesystem(strings.NewReader(tt.metrics), "/data", "/dev/data")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDiskObserverParsesBoundedHostAndPrometheusSafetyMetrics(t *testing.T) {
	metrics := `node_filesystem_size_bytes{device="/dev/data",mountpoint="/data"} 500000000000
node_filesystem_avail_bytes{device="/dev/data",mountpoint="/data"} 400000000000
wkbench_host_system_filesystem_size_bytes 40000000000
wkbench_host_system_filesystem_avail_bytes 20000000000
wkbench_host_cpu_busy_percent 90.125
wkbench_host_memory_used_percent 84.5
wkbench_host_watched_directory_bytes 139000000000
wkbench_host_network_transmit_bytes 123456789
`
	for index, unit := range productionProcessUnits {
		up := 0
		if index == 0 {
			up = 1
		}
		metrics += `wukongim_process_up{unit="` + unit + `"} ` + strconv.Itoa(up) + "\n"
		if up == 1 {
			metrics += `wukongim_process_cpu_jiffies_total{unit="` + unit + `"} 123` + "\n"
			metrics += `wukongim_process_resident_memory_bytes{unit="` + unit + `"} 456` + "\n"
		}
	}
	filesystem, err := parseNodeExporterFilesystem(strings.NewReader(metrics), "/data", "/dev/data")
	if err != nil {
		t.Fatal(err)
	}
	if !filesystem.HostResourcesObserved || !filesystem.WatchedDirectoryObserved || !filesystem.NetworkTransmitObserved ||
		!filesystem.ProcessResourcesObserved || !filesystem.ProcessUp[0] || filesystem.ProcessCPUJiffies[0] != 123 ||
		filesystem.ProcessResidentMemoryBytes[0] != 456 ||
		filesystem.SystemSizeBytes != 40_000_000_000 || filesystem.SystemAvailableBytes != 20_000_000_000 ||
		filesystem.CPUPercent != 90.125 || filesystem.MemoryPercent != 84.5 ||
		filesystem.WatchedDirectoryBytes != 139_000_000_000 || filesystem.NetworkTransmitBytes != 123_456_789 {
		t.Fatalf("host filesystem = %+v", filesystem)
	}
}
