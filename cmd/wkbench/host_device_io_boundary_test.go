package main

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUnavailableAndLinuxDeviceSamplersFailClosed(t *testing.T) {
	if got := (unavailableHostDeviceIOSampler{}).Sample(); got.Device != "unavailable" || got.IOPSAvailable {
		t.Fatalf("default unavailable sample = %#v", got)
	}
	if got := (unavailableHostDeviceIOSampler{device: "nvme0n1"}).Sample(); got.Device != "nvme0n1" || got.BytesPerSecondAvailable {
		t.Fatalf("named unavailable sample = %#v", got)
	}
	var nilSampler *linuxHostDeviceIOSampler
	if got := nilSampler.Sample(); got.Device != "unavailable" {
		t.Fatalf("nil Linux sampler = %#v", got)
	}

	path := filepath.Join(t.TempDir(), "stat")
	before, err := parseLinuxBlockDeviceCounters("100 0 200 300 400 0 500 600 0 700 800")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("120 0 260 340 430 0 620 660 0 900 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sampler := &linuxHostDeviceIOSampler{
		device: "nvme0n1", statPath: path, previous: before,
		at: time.Now().Add(-2 * time.Second),
	}
	if got := sampler.Sample(); !got.IOPSAvailable || got.Device != "nvme0n1" || got.TotalIOPS <= 0 {
		t.Fatalf("Linux device sample = %#v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := sampler.Sample(); got.Device != "nvme0n1" || got.IOPSAvailable {
		t.Fatalf("unreadable Linux sample = %#v", got)
	}
}

func TestDarwinDeviceSamplerRefreshesOffTheObservationPath(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	collected := make(chan struct{})
	sampler := &darwinHostDeviceIOSampler{
		device: "disk0",
		now:    func() time.Time { return now },
		collect: func() (hostDeviceIOSample, error) {
			close(collected)
			return hostDeviceIOSample{IOPSAvailable: true, TotalIOPS: 25}, nil
		},
	}
	first := sampler.Sample()
	if first.Device != "disk0" || first.IOPSAvailable {
		t.Fatalf("initial non-blocking sample = %#v", first)
	}
	select {
	case <-collected:
	case <-time.After(time.Second):
		t.Fatal("asynchronous sampler refresh did not run")
	}
	for {
		sampler.mu.Lock()
		refreshing := sampler.refreshing
		sampler.mu.Unlock()
		if !refreshing {
			break
		}
	}
	if got := sampler.Sample(); got.Device != "disk0" || !got.IOPSAvailable || got.TotalIOPS != 25 {
		t.Fatalf("refreshed sample = %#v", got)
	}

	failed := make(chan struct{})
	failing := &darwinHostDeviceIOSampler{
		device: "disk1",
		last:   hostDeviceIOSample{Device: "disk1", IOPSAvailable: true, TotalIOPS: 9},
		at:     now.Add(-darwinHostDeviceIOMaxSampleAge),
		now:    func() time.Time { return now },
		collect: func() (hostDeviceIOSample, error) {
			close(failed)
			return hostDeviceIOSample{}, errors.New("iostat failed")
		},
	}
	if got := failing.Sample(); got.Device != "disk1" || got.IOPSAvailable {
		t.Fatalf("expired sample = %#v", got)
	}
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("failed asynchronous refresh did not run")
	}
	for {
		failing.mu.Lock()
		refreshing := failing.refreshing
		failing.mu.Unlock()
		if !refreshing {
			break
		}
	}
	failing.mu.Lock()
	got := failing.last
	failing.mu.Unlock()
	if got.Device != "disk1" || got.IOPSAvailable {
		t.Fatalf("failed refresh sample = %#v", got)
	}

	var nilDarwin *darwinHostDeviceIOSampler
	if got := nilDarwin.Sample(); got.Device != "unavailable" {
		t.Fatalf("nil Darwin sampler = %#v", got)
	}
	if got := (&darwinHostDeviceIOSampler{}).currentTime(); got.IsZero() {
		t.Fatal("nil Darwin clock did not use the production clock")
	}
}

func TestLinuxBlockDeviceCounterParsingAndDeltaBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat")
	if _, err := readLinuxBlockDeviceCounters(path); !errors.Is(err, errHostMetricsConfig) {
		t.Fatalf("missing stat error = %v", err)
	}
	for _, line := range []string{"1 2 3", "1 2 bad 4 5 6 7 8 9 10 11"} {
		if _, err := parseLinuxBlockDeviceCounters(line); !errors.Is(err, errHostMetricsConfig) {
			t.Fatalf("invalid counters %q error = %v", line, err)
		}
	}

	validBefore := linuxBlockDeviceCounters{}
	validAfter := linuxBlockDeviceCounters{reads: 2, sectorsRead: 4, readMilliseconds: 6, writes: 3, sectorsWritten: 5, writeMilliseconds: 7, ioMilliseconds: 8}
	if sample, ok := linuxBlockDeviceDelta("disk", validBefore, validAfter, time.Second); !ok || !sample.ServiceTimeAvailable {
		t.Fatalf("valid delta = %#v, %v", sample, ok)
	}
	if sample, ok := linuxBlockDeviceDelta("disk", validBefore, linuxBlockDeviceCounters{ioMilliseconds: 1}, time.Second); !ok || sample.ServiceTimeAvailable {
		t.Fatalf("zero-operation delta = %#v, %v", sample, ok)
	}
	for name, test := range map[string]struct {
		device  string
		before  linuxBlockDeviceCounters
		after   linuxBlockDeviceCounters
		elapsed time.Duration
	}{
		"empty device":     {elapsed: time.Second},
		"short interval":   {device: "disk", elapsed: time.Microsecond},
		"counter reset":    {device: "disk", before: linuxBlockDeviceCounters{reads: 2}, after: linuxBlockDeviceCounters{reads: 1}, elapsed: time.Second},
		"sector multiply":  {device: "disk", after: linuxBlockDeviceCounters{sectorsRead: math.MaxUint64/512 + 1}, elapsed: time.Second},
		"operation sum":    {device: "disk", after: linuxBlockDeviceCounters{reads: math.MaxUint64, writes: 1}, elapsed: time.Second},
		"byte sum":         {device: "disk", after: linuxBlockDeviceCounters{sectorsRead: math.MaxUint64 / 512, sectorsWritten: math.MaxUint64 / 512}, elapsed: time.Second},
		"service time sum": {device: "disk", after: linuxBlockDeviceCounters{readMilliseconds: math.MaxUint64, writeMilliseconds: 1}, elapsed: time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := linuxBlockDeviceDelta(test.device, test.before, test.after, test.elapsed); ok {
				t.Fatal("invalid counter delta unexpectedly accepted")
			}
		})
	}
}

func TestDarwinDeviceOutputParsersFailClosed(t *testing.T) {
	for name, output := range map[string]string{
		"physical store": "Device Identifier: disk3s5\nAPFS Physical Store: disk2s1\nPart of Whole: disk3\n",
		"whole disk":     "Device Identifier: disk3s5\nPart of Whole: disk3\n",
		"identifier":     "Device Identifier: /dev/disk4s2\n",
	} {
		t.Run(name, func(t *testing.T) {
			device, ok := parseDarwinPhysicalDevice(output)
			if !ok || !strings.HasPrefix(device, "disk") {
				t.Fatalf("physical device = %q, %v", device, ok)
			}
		})
	}
	if _, ok := parseDarwinPhysicalDevice("not a key-value document"); ok {
		t.Fatal("malformed diskutil output unexpectedly accepted")
	}
	for raw, want := range map[string]string{"disk0": "disk0", "/dev/disk12s3": "disk12"} {
		if got, ok := normalizeDarwinWholeDisk(raw); !ok || got != want {
			t.Fatalf("normalize %q = %q, %v; want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "nvme0n1", "disk"} {
		if _, ok := normalizeDarwinWholeDisk(raw); ok {
			t.Fatalf("invalid whole disk %q unexpectedly accepted", raw)
		}
	}
	if got, ok := parseDarwinDFDevice("Filesystem Blocks Used Mounted\n/dev/disk2s1 1 1 /\n/dev/disk3s1 1 1 /Data\n"); !ok || got != "/dev/disk3s1" {
		t.Fatalf("df device = %q, %v", got, ok)
	}
	for _, output := range []string{"", "Filesystem Blocks Used Mounted\ntmpfs 1 1 /tmp\n"} {
		if _, ok := parseDarwinDFDevice(output); ok {
			t.Fatalf("invalid df output unexpectedly accepted: %q", output)
		}
	}

	for name, input := range map[string]struct {
		device  string
		output  string
		elapsed time.Duration
	}{
		"empty device":    {output: "1 2 3", elapsed: time.Second},
		"zero interval":   {device: "disk0", output: "1 2 3"},
		"no sample":       {device: "disk0", output: "header only", elapsed: time.Second},
		"negative sample": {device: "disk0", output: "1 -2 3", elapsed: time.Second},
		"nonfinite":       {device: "disk0", output: "1 NaN +Inf", elapsed: time.Second},
		"scanner limit":   {device: "disk0", output: strings.Repeat("x", 70<<10), elapsed: time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDarwinIostatDeviceSample(input.device, input.output, input.elapsed); !errors.Is(err, errHostMetricsConfig) {
				t.Fatalf("invalid iostat error = %v", err)
			}
		})
	}
	sample, err := parseDarwinIostatDeviceSample("disk0", "header\n1 bad 3\n4 6 8\n", 2*time.Second)
	if err != nil || sample.TotalIOPS != 3 || sample.TotalBytesPerSecond != 4*1024*1024 {
		t.Fatalf("parsed iostat sample = %#v, %v", sample, err)
	}
}
