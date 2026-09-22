//go:build darwin

package main

import "time"

// Darwin remains a functional smoke platform, without Linux clock alignment.
func monotonicNS() int64                       { return 0 }
func processIdentity(h *host) error            { return nil }
func measurementStart() (time.Time, clockSpan) { return time.Now(), clockSpan{} }
