package main

import (
	"bufio"
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// hostDeviceIOSample is one availability-aware physical-device observation.
// Unsupported counters remain unavailable instead of being emitted as zero.
type hostDeviceIOSample struct {
	Device                  string
	IOPSAvailable           bool
	BytesPerSecondAvailable bool
	UtilizationAvailable    bool
	ServiceTimeAvailable    bool
	ReadWriteSplitAvailable bool
	TotalIOPS               float64
	ReadIOPS                float64
	WriteIOPS               float64
	TotalBytesPerSecond     float64
	ReadBytesPerSecond      float64
	WriteBytesPerSecond     float64
	UtilizationPercent      float64
	ServiceTimeMilliseconds float64
}

type linuxBlockDeviceCounters struct {
	reads, sectorsRead, readMilliseconds      uint64
	writes, sectorsWritten, writeMilliseconds uint64
	ioMilliseconds                            uint64
}

// hostDeviceIOSampler normalizes platform-specific block-device observations.
type hostDeviceIOSampler interface {
	Sample() hostDeviceIOSample
}

type unavailableHostDeviceIOSampler struct{ device string }

func (s unavailableHostDeviceIOSampler) Sample() hostDeviceIOSample {
	device := s.device
	if device == "" {
		device = "unavailable"
	}
	return hostDeviceIOSample{Device: device}
}

// linuxHostDeviceIOSampler derives rates from one resolved physical device's sysfs counters.
type linuxHostDeviceIOSampler struct {
	mu       sync.Mutex
	device   string
	statPath string
	previous linuxBlockDeviceCounters
	at       time.Time
}

// darwinHostDeviceIOSampler caches the bounded two-sample iostat observation.
type darwinHostDeviceIOSampler struct {
	mu     sync.Mutex
	device string
	last   hostDeviceIOSample
	at     time.Time
}

func newHostDeviceIOSampler(path string) hostDeviceIOSampler {
	switch runtime.GOOS {
	case "linux":
		device, statPath, ok := resolveLinuxPhysicalBlockDevice(path)
		if !ok {
			return unavailableHostDeviceIOSampler{}
		}
		counters, err := readLinuxBlockDeviceCounters(statPath)
		if err != nil {
			return unavailableHostDeviceIOSampler{device: device}
		}
		return &linuxHostDeviceIOSampler{device: device, statPath: statPath, previous: counters, at: time.Now()}
	case "darwin":
		device, ok := resolveDarwinPhysicalDevice(path)
		if !ok {
			return unavailableHostDeviceIOSampler{}
		}
		return &darwinHostDeviceIOSampler{device: device}
	default:
		return unavailableHostDeviceIOSampler{}
	}
}

func resolveDarwinPhysicalDevice(path string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	body, err := exec.CommandContext(ctx, "diskutil", "info", path).CombinedOutput()
	cancel()
	if device, ok := parseDarwinPhysicalDevice(string(body)); err == nil && ok {
		return device, true
	}
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	dfBody, dfErr := exec.CommandContext(ctx, "df", "-P", path).CombinedOutput()
	cancel()
	volume, ok := parseDarwinDFDevice(string(dfBody))
	if dfErr != nil || !ok {
		return "", false
	}
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	body, err = exec.CommandContext(ctx, "diskutil", "info", volume).CombinedOutput()
	cancel()
	if err != nil {
		return "", false
	}
	return parseDarwinPhysicalDevice(string(body))
}

func (s *linuxHostDeviceIOSampler) Sample() hostDeviceIOSample {
	if s == nil {
		return unavailableHostDeviceIOSampler{}.Sample()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	current, err := readLinuxBlockDeviceCounters(s.statPath)
	if err != nil {
		return hostDeviceIOSample{Device: s.device}
	}
	sample, ok := linuxBlockDeviceDelta(s.device, s.previous, current, now.Sub(s.at))
	s.previous, s.at = current, now
	if !ok {
		return hostDeviceIOSample{Device: s.device}
	}
	return sample
}

func (s *darwinHostDeviceIOSampler) Sample() hostDeviceIOSample {
	if s == nil {
		return unavailableHostDeviceIOSampler{}.Sample()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && time.Since(s.at) < 4*time.Second {
		return s.last
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	body, err := exec.CommandContext(ctx, "iostat", "-Id", s.device, "1", "2").CombinedOutput()
	if err != nil {
		return hostDeviceIOSample{Device: s.device}
	}
	sample, parseErr := parseDarwinIostatDeviceSample(s.device, string(body), time.Second)
	if parseErr != nil {
		return hostDeviceIOSample{Device: s.device}
	}
	s.last, s.at = sample, time.Now()
	return sample
}

func resolveLinuxPhysicalBlockDevice(path string) (string, string, bool) {
	var stat unix.Stat_t
	if unix.Stat(path, &stat) != nil {
		return "", "", false
	}
	link := filepath.Join("/sys/dev/block", strconv.FormatUint(uint64(unix.Major(uint64(stat.Dev))), 10)+":"+
		strconv.FormatUint(uint64(unix.Minor(uint64(stat.Dev))), 10))
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", "", false
	}
	device := filepath.Base(resolved)
	device, ok := resolveLinuxSingleBackingDevice(device)
	if !ok {
		return "", "", false
	}
	statPath := filepath.Join("/sys/class/block", device, "stat")
	if info, err := os.Stat(statPath); err != nil || !info.Mode().IsRegular() {
		return "", "", false
	}
	return device, statPath, true
}

func resolveLinuxSingleBackingDevice(device string) (string, bool) {
	if strings.TrimSpace(device) == "" {
		return "", false
	}
	devicePath, err := filepath.EvalSymlinks(filepath.Join("/sys/class/block", device))
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join("/sys/class/block", device, "partition")); err == nil {
		device = filepath.Base(filepath.Dir(devicePath))
	}
	slaves := filepath.Join("/sys/class/block", device, "slaves")
	entries, err := os.ReadDir(slaves)
	if err != nil || len(entries) == 0 {
		return device, device != ""
	}
	if len(entries) != 1 {
		return "", false
	}
	return resolveLinuxSingleBackingDevice(entries[0].Name())
}

func readLinuxBlockDeviceCounters(path string) (linuxBlockDeviceCounters, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return linuxBlockDeviceCounters{}, errHostMetricsConfig
	}
	return parseLinuxBlockDeviceCounters(string(body))
}

func parseLinuxBlockDeviceCounters(line string) (linuxBlockDeviceCounters, error) {
	fields := strings.Fields(line)
	if len(fields) < 11 {
		return linuxBlockDeviceCounters{}, errHostMetricsConfig
	}
	values := make([]uint64, len(fields))
	for index, field := range fields {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return linuxBlockDeviceCounters{}, errHostMetricsConfig
		}
		values[index] = value
	}
	return linuxBlockDeviceCounters{
		reads: values[0], sectorsRead: values[2], readMilliseconds: values[3],
		writes: values[4], sectorsWritten: values[6], writeMilliseconds: values[7],
		ioMilliseconds: values[9],
	}, nil
}

func linuxBlockDeviceDelta(
	device string,
	before, after linuxBlockDeviceCounters,
	elapsed time.Duration,
) (hostDeviceIOSample, bool) {
	seconds := elapsed.Seconds()
	if strings.TrimSpace(device) == "" || elapsed < time.Millisecond || seconds <= 0 ||
		after.reads < before.reads || after.sectorsRead < before.sectorsRead ||
		after.readMilliseconds < before.readMilliseconds || after.writes < before.writes ||
		after.sectorsWritten < before.sectorsWritten || after.writeMilliseconds < before.writeMilliseconds ||
		after.ioMilliseconds < before.ioMilliseconds {
		return hostDeviceIOSample{}, false
	}
	readOperations := after.reads - before.reads
	writeOperations := after.writes - before.writes
	readSectors := after.sectorsRead - before.sectorsRead
	writeSectors := after.sectorsWritten - before.sectorsWritten
	if readSectors > math.MaxUint64/512 || writeSectors > math.MaxUint64/512 ||
		math.MaxUint64-readOperations < writeOperations {
		return hostDeviceIOSample{}, false
	}
	totalOperations := readOperations + writeOperations
	readBytes, writeBytes := readSectors*512, writeSectors*512
	readServiceMilliseconds := after.readMilliseconds - before.readMilliseconds
	writeServiceMilliseconds := after.writeMilliseconds - before.writeMilliseconds
	if math.MaxUint64-readBytes < writeBytes || math.MaxUint64-readServiceMilliseconds < writeServiceMilliseconds {
		return hostDeviceIOSample{}, false
	}
	sample := hostDeviceIOSample{
		Device: device, IOPSAvailable: true, BytesPerSecondAvailable: true,
		UtilizationAvailable: true, ServiceTimeAvailable: totalOperations > 0, ReadWriteSplitAvailable: true,
		TotalIOPS: float64(totalOperations) / seconds, ReadIOPS: float64(readOperations) / seconds,
		WriteIOPS: float64(writeOperations) / seconds, TotalBytesPerSecond: float64(readBytes+writeBytes) / seconds,
		ReadBytesPerSecond: float64(readBytes) / seconds, WriteBytesPerSecond: float64(writeBytes) / seconds,
		UtilizationPercent: float64(after.ioMilliseconds-before.ioMilliseconds) * 100 / float64(elapsed/time.Millisecond),
	}
	if totalOperations > 0 {
		totalServiceMilliseconds := readServiceMilliseconds + writeServiceMilliseconds
		sample.ServiceTimeMilliseconds = float64(totalServiceMilliseconds) / float64(totalOperations)
	}
	return sample, true
}

func parseDarwinPhysicalDevice(output string) (string, bool) {
	var physicalStore, whole, identifier string
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "APFS Physical Store":
			physicalStore = strings.TrimSpace(value)
		case "Part of Whole":
			whole = strings.TrimSpace(value)
		case "Device Identifier":
			identifier = strings.TrimSpace(value)
		}
	}
	for _, candidate := range []string{physicalStore, whole, identifier} {
		if normalized, ok := normalizeDarwinWholeDisk(candidate); ok {
			return normalized, true
		}
	}
	return "", false
}

func parseDarwinDFDevice(output string) (string, bool) {
	var device string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "Filesystem" {
			continue
		}
		device = fields[0]
	}
	if !strings.HasPrefix(device, "/dev/disk") {
		return "", false
	}
	return device, true
}

func normalizeDarwinWholeDisk(device string) (string, bool) {
	device = strings.TrimPrefix(strings.TrimSpace(device), "/dev/")
	if !strings.HasPrefix(device, "disk") {
		return "", false
	}
	index := len("disk")
	for index < len(device) && device[index] >= '0' && device[index] <= '9' {
		index++
	}
	if index == len("disk") {
		return "", false
	}
	return device[:index], true
}

func parseDarwinIostatDeviceSample(device, output string, elapsed time.Duration) (hostDeviceIOSample, error) {
	if strings.TrimSpace(device) == "" || elapsed <= 0 {
		return hostDeviceIOSample{}, errHostMetricsConfig
	}
	var transfers, megabytes float64
	found := false
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		if _, err := strconv.ParseFloat(fields[0], 64); err != nil {
			continue
		}
		parsedTransfers, transferErr := strconv.ParseFloat(fields[1], 64)
		parsedMegabytes, megabyteErr := strconv.ParseFloat(fields[2], 64)
		if transferErr != nil || megabyteErr != nil || parsedTransfers < 0 || parsedMegabytes < 0 ||
			math.IsNaN(parsedTransfers) || math.IsInf(parsedTransfers, 0) ||
			math.IsNaN(parsedMegabytes) || math.IsInf(parsedMegabytes, 0) {
			continue
		}
		transfers, megabytes, found = parsedTransfers, parsedMegabytes, true
	}
	if scanner.Err() != nil || !found {
		return hostDeviceIOSample{}, errHostMetricsConfig
	}
	seconds := elapsed.Seconds()
	return hostDeviceIOSample{
		Device: device, IOPSAvailable: true, BytesPerSecondAvailable: true,
		TotalIOPS: transfers / seconds, TotalBytesPerSecond: megabytes * 1024 * 1024 / seconds,
	}, nil
}
