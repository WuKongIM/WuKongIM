package pluginhost

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWatcherDebouncerCoalescesDuplicatePluginEvents(t *testing.T) {
	dir := t.TempDir()
	scheduler := &manualDebounceScheduler{}
	var requests []RestartRequest
	debouncer := newPluginDebouncer(50*time.Millisecond, scheduler, func(req RestartRequest) {
		requests = append(requests, req)
	})
	path := filepath.Join(dir, "alpha.wkp")

	debouncer.HandlePath(path)
	debouncer.HandlePath(path)
	scheduler.FireAll()

	require.Equal(t, []RestartRequest{{PluginNo: "alpha", Path: path}}, requests)
}

func TestWatcherDebouncerIgnoresNonPluginFiles(t *testing.T) {
	scheduler := &manualDebounceScheduler{}
	var requests []RestartRequest
	debouncer := newPluginDebouncer(time.Millisecond, scheduler, func(req RestartRequest) {
		requests = append(requests, req)
	})

	debouncer.HandlePath(filepath.Join(t.TempDir(), "alpha.txt"))
	scheduler.FireAll()

	require.Empty(t, requests)
}

func TestWatcherDebouncerStopCancelsPendingEvents(t *testing.T) {
	scheduler := &manualDebounceScheduler{}
	var requests []RestartRequest
	debouncer := newPluginDebouncer(time.Millisecond, scheduler, func(req RestartRequest) {
		requests = append(requests, req)
	})

	debouncer.HandlePath(filepath.Join(t.TempDir(), "alpha.wkp"))
	debouncer.Stop()
	scheduler.FireAll()

	require.Empty(t, requests)
}

func TestWatcherDebouncerStopDoesNotDeadlockWithInFlightEmit(t *testing.T) {
	scheduler := &manualDebounceScheduler{}
	emitStarted := make(chan struct{})
	releaseEmit := make(chan struct{})
	debouncer := newPluginDebouncer(time.Millisecond, scheduler, func(req RestartRequest) {
		close(emitStarted)
		<-releaseEmit
	})
	debouncer.HandlePath(filepath.Join(t.TempDir(), "alpha.wkp"))

	go scheduler.FireAll()
	select {
	case <-emitStarted:
	case <-time.After(time.Second):
		t.Fatal("debounced emit did not start")
	}
	stopped := make(chan struct{})
	go func() {
		debouncer.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(100 * time.Millisecond):
		close(releaseEmit)
		t.Fatal("Stop blocked behind an in-flight emit")
	}
	close(releaseEmit)
}

type manualDebounceScheduler struct {
	funcs []func()
}

func (m *manualDebounceScheduler) AfterFunc(delay time.Duration, fn func()) debounceTimer {
	m.funcs = append(m.funcs, fn)
	return manualDebounceTimer{stop: func() { fn = nil }}
}

func (m *manualDebounceScheduler) FireAll() {
	funcs := append([]func(){}, m.funcs...)
	m.funcs = nil
	for _, fn := range funcs {
		if fn != nil {
			fn()
		}
	}
}

type manualDebounceTimer struct {
	stop func()
}

func (m manualDebounceTimer) Stop() bool {
	if m.stop != nil {
		m.stop()
	}
	return true
}
