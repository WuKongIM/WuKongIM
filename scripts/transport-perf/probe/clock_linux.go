//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// monotonicNS shares CLOCK_MONOTONIC with the external Linux sampler.
func monotonicNS() int64 {
	var t unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &t); err != nil {
		return 0
	}
	return t.Nano()
}

func processIdentity(h *host) error {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	digest := sha256.Sum256(boot)
	h.BootID = hex.EncodeToString(digest[:])
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return err
	}
	end := strings.LastIndexByte(string(stat), ')')
	if end < 0 {
		return fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(stat)[end+1:])
	if len(fields) <= 19 {
		return fmt.Errorf("short process stat")
	}
	h.StartTicks, err = strconv.ParseUint(fields[19], 10, 64)
	return err
}

// measurementStart bounds time.Now by two readings of the sampler's clock.
func measurementStart() (time.Time, clockSpan) {
	lo := monotonicNS()
	t := time.Now()
	return t, clockSpan{LowNS: lo, HighNS: monotonicNS()}
}
