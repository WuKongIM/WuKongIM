package model

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelRuntimeProbeRequestJSONRoundTripGeneratedSelector(t *testing.T) {
	want := ChannelRuntimeProbeRequest{
		RunID:       "run-a",
		Profile:     "person",
		ChannelType: 1,
		Range:       ChannelRuntimeRange{Start: 3, End: 8},
	}

	data, err := json.Marshal(want)
	require.NoError(t, err)
	require.NotContains(t, string(data), `"channels"`)

	var got ChannelRuntimeProbeRequest
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, want, got)
}

func TestBenchCapabilitiesRoundTripTerminalFenceSupport(t *testing.T) {
	want := BenchCapabilities{Enabled: true, Version: "bench/v1", Supports: BenchCapabilitiesSupports{TerminalFencePrepare: true}}
	body, err := json.Marshal(want)
	require.NoError(t, err)
	require.Contains(t, string(body), `"terminal_fence_prepare":true`)
	var got BenchCapabilities
	require.NoError(t, json.Unmarshal(body, &got))
	require.True(t, got.Supports.TerminalFencePrepare)
}

func TestChannelRuntimeProbeRequestJSONRoundTripExplicitSelector(t *testing.T) {
	want := ChannelRuntimeProbeRequest{
		Channels: []ChannelRuntimeChannelIdentity{
			{ChannelID: "person-a", ChannelType: 1},
			{ChannelID: "group-b", ChannelType: 2},
		},
	}

	data, err := json.Marshal(want)
	require.NoError(t, err)

	var got ChannelRuntimeProbeRequest
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, want, got)
}

func TestChannelRuntimeProbeResultJSONRoundTripDetailedChannels(t *testing.T) {
	want := ChannelRuntimeProbeResult{
		Version:        "bench/v1",
		NodeID:         3,
		Checked:        2,
		LoadedLeader:   1,
		LoadedFollower: 0,
		Missing:        []string{"missing-b"},
		Channels: []ChannelRuntimeProbeChannel{
			{
				ChannelID:    "person-a",
				ChannelType:  1,
				Role:         "leader",
				Status:       "active",
				LEO:          31,
				HW:           29,
				CheckpointHW: 27,
				LeaderEpoch:  11,
				ChannelEpoch: 7,
			},
			{
				ChannelID:   "missing-b",
				ChannelType: 2,
				Role:        "missing",
				Status:      "missing",
			},
		},
	}

	data, err := json.Marshal(want)
	require.NoError(t, err)

	var got ChannelRuntimeProbeResult
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, want, got)
}

func TestChannelRuntimeProbeFailureCollapsesUnknownReason(t *testing.T) {
	const sensitive = "unbounded-sensitive-reason"
	err := &ChannelRuntimeProbeFailure{Reason: ChannelRuntimeProbeFailureReason(sensitive), Cause: errors.New("private cause")}

	require.Equal(t, ChannelRuntimeProbeFailureInternal, ChannelRuntimeProbeFailureReasonOf(err))
	require.NotContains(t, err.Error(), sensitive)
	require.NotContains(t, err.Error(), "private cause")
}
