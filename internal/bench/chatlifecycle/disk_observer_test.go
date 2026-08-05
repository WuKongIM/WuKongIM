package chatlifecycle

import (
	"errors"
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
