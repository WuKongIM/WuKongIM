package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
)

const (
	localSingleNodeProductExecutableSchema      = "wukongim/chat-lifecycle-local-single-node-product-executable/v1"
	localSingleNodeMaximumExecutableAttestBytes = 16 << 10
)

type localSingleNodeProductExecutableAttestation struct {
	BaselineInvocationID string
	RateTag              string
	Generation           int
	Binary               string
	SourceConfigSHA256   string
	PreSpawnStage        string
	PreSpawnSHA256       string
	PostStopSHA256       string
	SealedBinarySHA256   string
}

func parseLocalSingleNodeProductExecutableAttestation(
	data []byte,
	expectedInvocationID string,
	offeredQPS int,
	expectedBinarySHA256 string,
) (localSingleNodeProductExecutableAttestation, error) {
	text := string(data)
	if len(data) == 0 || len(data) > localSingleNodeMaximumExecutableAttestBytes ||
		!strings.HasSuffix(text, "\n") || strings.Contains(text, "\r") {
		return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation framing is invalid")
	}
	rows := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	keys := [...]string{
		"schema", "baseline_invocation_id", "rate_tag", "generation", "binary",
		"source_config_sha256", "pre_spawn_stage", "pre_spawn_sha256", "post_stop_sha256", "sealed_binary_sha256",
	}
	if len(rows) != len(keys) {
		return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation field set is invalid")
	}
	values := make(map[string]string, len(keys))
	for index, row := range rows {
		parts := strings.Split(row, "\t")
		if len(parts) != 2 || parts[0] != keys[index] || strings.TrimSpace(parts[1]) != parts[1] || parts[1] == "" {
			return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation row is invalid")
		}
		values[parts[0]] = parts[1]
	}
	if values["schema"] != localSingleNodeProductExecutableSchema ||
		!validLocalSingleNodeInvocationID(values["baseline_invocation_id"]) ||
		values["baseline_invocation_id"] != expectedInvocationID {
		return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation invocation identity is invalid")
	}
	expectedRateTag, expectedGeneration, ok := localSingleNodeReviewedGeneration(offeredQPS)
	if !ok || values["rate_tag"] != expectedRateTag {
		return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation rate identity is invalid")
	}
	generation, err := strconv.Atoi(values["generation"])
	if err != nil || strconv.Itoa(generation) != values["generation"] || generation != expectedGeneration {
		return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation generation is invalid")
	}
	expectedStage := "pre_spawn"
	if generation == 1 {
		expectedStage = "post_ready_first_generation"
	}
	if values["binary"] != "bin/wukongim" || values["pre_spawn_stage"] != expectedStage {
		return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation execution identity is invalid")
	}
	for _, key := range []string{"source_config_sha256", "pre_spawn_sha256", "post_stop_sha256", "sealed_binary_sha256"} {
		if !validLocalSingleNodeDigest(values[key]) {
			return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation digest is invalid")
		}
	}
	if values["pre_spawn_sha256"] != values["post_stop_sha256"] ||
		values["pre_spawn_sha256"] != values["sealed_binary_sha256"] ||
		values["sealed_binary_sha256"] != expectedBinarySHA256 {
		return localSingleNodeProductExecutableAttestation{}, fmt.Errorf("product executable attestation binary changed")
	}
	return localSingleNodeProductExecutableAttestation{
		BaselineInvocationID: values["baseline_invocation_id"], RateTag: values["rate_tag"],
		Generation: generation, Binary: values["binary"], SourceConfigSHA256: values["source_config_sha256"],
		PreSpawnStage: values["pre_spawn_stage"], PreSpawnSHA256: values["pre_spawn_sha256"],
		PostStopSHA256: values["post_stop_sha256"], SealedBinarySHA256: values["sealed_binary_sha256"],
	}, nil
}

func localSingleNodeReviewedGeneration(offeredQPS int) (string, int, bool) {
	for index, reviewedQPS := range localbaseline.ReviewedOfferedSendQPS {
		if offeredQPS == reviewedQPS {
			return fmt.Sprintf("%06d", offeredQPS), index + 1, true
		}
	}
	return "", 0, false
}
