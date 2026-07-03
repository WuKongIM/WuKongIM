package pluginhost

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRegistryRunningByMethodOrdersByPriorityThenPluginNo(t *testing.T) {
	r := NewRegistry()
	r.Upsert(ObservedPlugin{No: "b", Priority: 10, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})
	r.Upsert(ObservedPlugin{No: "a", Priority: 10, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})
	r.Upsert(ObservedPlugin{No: "c", Priority: 11, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})

	got := r.RunningByMethod(MethodSend)

	require.Equal(t, []string{"c", "a", "b"}, pluginNos(got))
}

func TestRegistryRunningByMethodFiltersDisabledOfflineAndDifferentMethods(t *testing.T) {
	r := NewRegistry()
	r.Upsert(ObservedPlugin{No: "send", Priority: 1, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})
	r.Upsert(ObservedPlugin{No: "disabled", Priority: 100, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: false})
	r.Upsert(ObservedPlugin{No: "offline", Priority: 100, Methods: []Method{MethodSend}, Status: StatusOffline, Enabled: true})
	r.Upsert(ObservedPlugin{No: "receive", Priority: 100, Methods: []Method{MethodReceive}, Status: StatusRunning, Enabled: true})

	got := r.RunningByMethod(MethodSend)

	require.Equal(t, []string{"send"}, pluginNos(got))
}

func TestRegistryHighestRunningForUIDUsesDeterministicOrdering(t *testing.T) {
	r := NewRegistry()
	r.Upsert(ObservedPlugin{No: "low", Priority: 1, Methods: []Method{MethodRoute}, Status: StatusRunning, Enabled: true})
	r.Upsert(ObservedPlugin{No: "tie-b", Priority: 9, Methods: []Method{MethodRoute}, Status: StatusRunning, Enabled: true})
	r.Upsert(ObservedPlugin{No: "tie-a", Priority: 9, Methods: []Method{MethodRoute}, Status: StatusRunning, Enabled: true})
	r.Upsert(ObservedPlugin{No: "disabled", Priority: 99, Methods: []Method{MethodRoute}, Status: StatusRunning, Enabled: false})
	r.Upsert(ObservedPlugin{No: "offline", Priority: 98, Methods: []Method{MethodRoute}, Status: StatusOffline, Enabled: true})
	r.Upsert(ObservedPlugin{No: "wrong-method", Priority: 97, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})

	got, ok := r.HighestRunningForUID([]string{"missing", "low", "tie-b", "disabled", "tie-a", "offline", "wrong-method"}, MethodRoute)

	require.True(t, ok)
	require.Equal(t, "tie-a", got.No)
}

func TestRegistryHighestRunningForUIDReturnsFalseWhenNoCandidateMatches(t *testing.T) {
	r := NewRegistry()
	r.Upsert(ObservedPlugin{No: "send", Priority: 1, Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true})

	got, ok := r.HighestRunningForUID([]string{"missing", "send"}, MethodReceive)

	require.False(t, ok)
	require.Equal(t, ObservedPlugin{}, got)
}

func TestRegistryReturnsCopies(t *testing.T) {
	r := NewRegistry()
	now := time.Now().UTC()
	r.Upsert(ObservedPlugin{No: "copy", Methods: []Method{MethodSend}, Status: StatusRunning, Enabled: true, LastSeenAt: now})

	got, ok := r.Get("copy")
	require.True(t, ok)
	got.Methods[0] = MethodReceive
	got.No = "mutated"

	again, ok := r.Get("copy")
	require.True(t, ok)
	require.Equal(t, "copy", again.No)
	require.Equal(t, []Method{MethodSend}, again.Methods)

	listed := r.List()
	require.Len(t, listed, 1)
	listed[0].Methods[0] = MethodRoute

	running := r.RunningByMethod(MethodSend)
	require.Equal(t, []string{"copy"}, pluginNos(running))
	require.False(t, running[0].LastSeenAt.IsZero())
}

func pluginNos(plugins []ObservedPlugin) []string {
	nos := make([]string, 0, len(plugins))
	for _, p := range plugins {
		nos = append(nos, p.No)
	}
	return nos
}
