package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
)

const localSingleNodeMaximumClosureBytes = 8 << 20

func publishLocalSingleNodeUnclosedStep(
	flags localSingleNodeStepFlags,
	evidence localbaseline.StepEvidence,
	result localbaseline.ClosedStepResult,
) error {
	root, err := openLocalSingleNodeArtifactRoot(flags.payloadRoot)
	if err != nil {
		return err
	}
	evidenceRelative, err := root.relative(flags.outputPath)
	if err != nil {
		return err
	}
	resultRelative, err := root.relative(flags.resultOutputPath)
	if err != nil {
		return err
	}
	evidenceData, err := marshalLocalSingleNodeJSON(evidence)
	if err != nil {
		return err
	}
	resultData, err := marshalLocalSingleNodeJSON(result)
	if err != nil {
		return err
	}
	if err := root.writeExclusive(evidenceRelative, evidenceData); err != nil {
		return err
	}
	return root.writeExclusive(resultRelative, resultData)
}

// publishLocalSingleNodeStepClosure writes the typed evidence and decision,
// then atomically publishes the closure manifest last. Consumers never infer
// closure from the presence of either intermediate output.
func publishLocalSingleNodeStepClosure(
	flags localSingleNodeStepFlags,
	raw localSingleNodeVerifiedManifest,
	evidence localbaseline.StepEvidence,
	result localbaseline.ClosedStepResult,
) (localbaseline.StepClosure, error) {
	root := raw.artifactRoot
	rawRelative, err := root.relative(flags.payloadManifestPath)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	evidenceRelative, err := root.relative(flags.outputPath)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	resultRelative, err := root.relative(flags.resultOutputPath)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	closureRelative, err := root.relative(flags.closureOutputPath)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	evidenceData, err := marshalLocalSingleNodeJSON(evidence)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	resultData, err := marshalLocalSingleNodeJSON(result)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	manifest := closureManifestFromStepFlags(raw, flags)
	manifest.PayloadManifest = rawRelative
	manifest.PayloadSHA256 = raw.digest
	manifest.Evidence = evidenceRelative
	manifest.EvidenceSHA256 = digestLocalSingleNodeBytes(evidenceData)
	manifest.Result = resultRelative
	manifest.ResultSHA256 = digestLocalSingleNodeBytes(resultData)
	if !localbaseline.ValidateStepClosureManifest(manifest) || result.PayloadManifestSHA256 != raw.digest {
		return localbaseline.StepClosure{}, fmt.Errorf("step closure does not match raw payload")
	}
	manifestData, err := marshalLocalSingleNodeJSON(manifest)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	closure := localbaseline.StepClosure{
		Schema: localbaseline.StepClosureSchema, ClosureManifest: closureRelative,
		ClosureManifestSHA256: digestLocalSingleNodeBytes(manifestData), Evidence: evidence, Result: result,
	}
	if !localbaseline.ValidateStepClosure(closure) {
		return localbaseline.StepClosure{}, fmt.Errorf("step closure decision is invalid")
	}
	if err := root.writeExclusive(evidenceRelative, evidenceData); err != nil {
		return localbaseline.StepClosure{}, err
	}
	if err := root.writeExclusive(resultRelative, resultData); err != nil {
		return localbaseline.StepClosure{}, err
	}
	if err := root.writeExclusive(closureRelative, manifestData); err != nil {
		return localbaseline.StepClosure{}, err
	}
	return closure, nil
}

func verifyLocalSingleNodeStepClosure(root localSingleNodeArtifactRoot, relative string) (localbaseline.StepClosure, error) {
	data, err := root.read(relative, localSingleNodeMaximumClosureBytes)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	var manifest localbaseline.StepClosureManifest
	if err := decodeLocalSingleNodeStrictJSON(data, &manifest); err != nil || !localbaseline.ValidateStepClosureManifest(manifest) {
		return localbaseline.StepClosure{}, fmt.Errorf("step closure manifest is invalid")
	}
	if manifest.PayloadManifest == relative || manifest.Evidence == relative || manifest.Result == relative {
		return localbaseline.StepClosure{}, fmt.Errorf("step closure manifest is self-referential")
	}
	rawManifestData, err := root.read(manifest.PayloadManifest, localSingleNodeMaximumManifestBytes)
	if err != nil || digestLocalSingleNodeBytes(rawManifestData) != manifest.PayloadSHA256 {
		return localbaseline.StepClosure{}, fmt.Errorf("step raw payload is invalid")
	}
	rawEntries, err := parseLocalSingleNodeChecksumManifest(rawManifestData, func(relative, expected string) error {
		actual, digestErr := root.digest(relative, 0)
		if digestErr != nil {
			return digestErr
		}
		if actual != expected {
			return fmt.Errorf("checksum mismatch")
		}
		return nil
	})
	if err != nil {
		return localbaseline.StepClosure{}, fmt.Errorf("step raw payload is invalid: %w", err)
	}
	rawManifest := localSingleNodeVerifiedManifest{
		root: root.path, artifactRoot: root, digest: manifest.PayloadSHA256, entries: rawEntries,
	}
	evidenceData, err := root.read(manifest.Evidence, localSingleNodeMaximumClosureBytes)
	if err != nil || digestLocalSingleNodeBytes(evidenceData) != manifest.EvidenceSHA256 {
		return localbaseline.StepClosure{}, fmt.Errorf("step evidence is not sealed")
	}
	resultData, err := root.read(manifest.Result, localSingleNodeMaximumClosureBytes)
	if err != nil || digestLocalSingleNodeBytes(resultData) != manifest.ResultSHA256 {
		return localbaseline.StepClosure{}, fmt.Errorf("step result is not sealed")
	}
	evidence, err := parseLocalSingleNodeStepEvidence(evidenceData)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	result, err := parseLocalSingleNodeStepResult(resultData)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}

	// Reconstruct from the raw, already verified bytes. Reopening caller paths
	// here would reintroduce a check/use window after checksum verification.
	rebuilt, err := buildLocalSingleNodeStepEvidenceFromVerifiedManifest(rawManifest, manifest)
	if err != nil || !localSingleNodeStepEvidenceEqual(rebuilt, evidence) {
		return localbaseline.StepClosure{}, fmt.Errorf("typed step evidence cannot be reconstructed from sealed raw payload")
	}
	closure := localbaseline.StepClosure{
		Schema: localbaseline.StepClosureSchema, ClosureManifest: relative,
		ClosureManifestSHA256: digestLocalSingleNodeBytes(data), Evidence: evidence, Result: result,
	}
	if result.PayloadManifestSHA256 != rawManifest.digest || !localbaseline.ValidateStepClosure(closure) {
		return localbaseline.StepClosure{}, fmt.Errorf("typed step decision cannot be reconstructed")
	}
	return closure, nil
}

// verifyLocalSingleNodeStepClosureFromManifest verifies nested closure and raw
// manifests exclusively from one already authenticated completion manifest.
func verifyLocalSingleNodeStepClosureFromManifest(
	parent localSingleNodeVerifiedManifest,
	relative string,
) (localbaseline.StepClosure, error) {
	data, err := parent.bytesForRelative(relative)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	var closureManifest localbaseline.StepClosureManifest
	if err := decodeLocalSingleNodeStrictJSON(data, &closureManifest); err != nil ||
		!localbaseline.ValidateStepClosureManifest(closureManifest) {
		return localbaseline.StepClosure{}, fmt.Errorf("step closure manifest is invalid")
	}
	if closureManifest.PayloadManifest == relative || closureManifest.Evidence == relative || closureManifest.Result == relative {
		return localbaseline.StepClosure{}, fmt.Errorf("step closure manifest is self-referential")
	}
	rawManifestData, err := parent.bytesForRelative(closureManifest.PayloadManifest)
	if err != nil || digestLocalSingleNodeBytes(rawManifestData) != closureManifest.PayloadSHA256 {
		return localbaseline.StepClosure{}, fmt.Errorf("step raw manifest is not sealed")
	}
	rawEntries, err := parseLocalSingleNodeChecksumManifest(rawManifestData, parent.requireDigest)
	if err != nil {
		return localbaseline.StepClosure{}, fmt.Errorf("step raw payload is invalid: %w", err)
	}
	raw := localSingleNodeVerifiedManifest{
		root: parent.root, artifactRoot: parent.artifactRoot,
		digest: closureManifest.PayloadSHA256, entries: rawEntries,
	}
	evidenceData, err := parent.bytesForRelative(closureManifest.Evidence)
	if err != nil || digestLocalSingleNodeBytes(evidenceData) != closureManifest.EvidenceSHA256 {
		return localbaseline.StepClosure{}, fmt.Errorf("step evidence is not sealed")
	}
	resultData, err := parent.bytesForRelative(closureManifest.Result)
	if err != nil || digestLocalSingleNodeBytes(resultData) != closureManifest.ResultSHA256 {
		return localbaseline.StepClosure{}, fmt.Errorf("step result is not sealed")
	}
	evidence, err := parseLocalSingleNodeStepEvidence(evidenceData)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	result, err := parseLocalSingleNodeStepResult(resultData)
	if err != nil {
		return localbaseline.StepClosure{}, err
	}
	rebuilt, err := buildLocalSingleNodeStepEvidenceFromVerifiedManifest(raw, closureManifest)
	if err != nil || !localSingleNodeStepEvidenceEqual(rebuilt, evidence) {
		return localbaseline.StepClosure{}, fmt.Errorf("typed step evidence cannot be reconstructed from sealed raw payload")
	}
	closure := localbaseline.StepClosure{
		Schema: localbaseline.StepClosureSchema, ClosureManifest: relative,
		ClosureManifestSHA256: digestLocalSingleNodeBytes(data), Evidence: evidence, Result: result,
	}
	if result.PayloadManifestSHA256 != raw.digest || !localbaseline.ValidateStepClosure(closure) {
		return localbaseline.StepClosure{}, fmt.Errorf("typed step decision cannot be reconstructed")
	}
	return closure, nil
}

func parseLocalSingleNodeStepEvidence(data []byte) (localbaseline.StepEvidence, error) {
	var value localbaseline.StepEvidence
	err := decodeLocalSingleNodeStrictJSON(data, &value)
	return value, err
}

func parseLocalSingleNodeStepResult(data []byte) (localbaseline.ClosedStepResult, error) {
	var value localbaseline.ClosedStepResult
	err := decodeLocalSingleNodeStrictJSON(data, &value)
	return value, err
}

func decodeLocalSingleNodeStrictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func localSingleNodeStepEvidenceEqual(left, right localbaseline.StepEvidence) bool {
	leftData, leftErr := json.Marshal(left)
	rightData, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftData, rightData)
}
