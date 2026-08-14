package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
)

func TestLocalSingleNodeCompletionCommandVerifiesAtomicPublication(t *testing.T) {
	root, markerPath := writeLocalSingleNodeCompletionFixture(t)
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
	}, &stderr)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr = %q", code, stderr.String())
	}
}

func TestLocalSingleNodeCompletionRejectsResealedSourceConfigIdentityMismatch(t *testing.T) {
	root, markerPath := writeLocalSingleNodeCompletionFixture(t)
	identityPath := filepath.Join(root, "artifact-identity.tsv")
	identity, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(identity),
		"original_config_sha256\t"+strings.Repeat("d", 64),
		"original_config_sha256\t"+strings.Repeat("e", 64), 1,
	)
	if changed == string(identity) {
		t.Fatal("fixture original config digest was not replaced")
	}
	if err := os.WriteFile(identityPath, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := writeLocalSingleNodeCompletionManifest(t, root, filepath.Join(root, "checksums.sha256"))
	rewriteLocalSingleNodeMarkerManifestDigest(t, markerPath, []byte(manifest))

	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
	}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "source config") {
		t.Fatalf("source-config identity mismatch exit/stderr = %d/%q", code, stderr.String())
	}
}

func TestParseLocalSingleNodeArtifactIdentityRejectsInvalidSourceConfigDigest(t *testing.T) {
	root, _ := writeLocalSingleNodeCompletionFixture(t)
	data, err := os.ReadFile(filepath.Join(root, "artifact-identity.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data),
		"original_config_sha256\t"+strings.Repeat("d", 64),
		"original_config_sha256\tunavailable", 1,
	))
	if _, err := parseLocalSingleNodeArtifactIdentity(data); err == nil {
		t.Fatal("invalid source config digest was accepted")
	}
}

func TestLocalSingleNodeCompletionRejectsManifestAuthenticatedInvalidTopology(t *testing.T) {
	valid := string(localSingleNodeReviewedEffectiveConfigFixture())
	tests := map[string]struct {
		config string
		want   string
	}{
		"multi-node": {
			config: strings.Replace(valid,
				`nodes = [{ id = 1, addr = "127.0.0.1:7001" }]`,
				`nodes = [{ id = 1, addr = "127.0.0.1:7001" }, { id = 2, addr = "127.0.0.1:7002" }]`, 1),
			want: "single-node cluster",
		},
		"node id mismatch": {
			config: strings.Replace(valid, "[node]\nid = 1", "[node]\nid = 2", 1),
			want:   "single-node cluster",
		},
		"unsealed runtime projection": {
			config: strings.Replace(valid, "topology_environment_overrides_rejected = true", "topology_environment_overrides_rejected = false", 1),
			want:   "typed reviewed settings",
		},
		"api listener differs from executed target": {
			config: strings.Replace(valid, "[api]\nlisten_addr = \"127.0.0.1:5001\"", "[api]\nlisten_addr = \"127.0.0.1:5002\"", 1),
			want:   "execution target",
		},
		"published gateway differs from executed target": {
			config: strings.Replace(valid, `external_tcp_addr = "127.0.0.1:5100"`, `external_tcp_addr = "127.0.0.1:5101"`, 1),
			want:   "execution target",
		},
		"wkproto listener differs from executed target": {
			config: strings.Replace(valid, `address = "127.0.0.1:5100"`, `address = "127.0.0.1:5101"`, 1),
			want:   "execution target",
		},
		"multiple wkproto listeners": {
			config: strings.Replace(valid,
				`listeners = [{ address = "127.0.0.1:5100", name = "tcp-wkproto", network = "tcp", protocol = "wkproto", transport = "gnet" }]`,
				`listeners = [{ address = "127.0.0.1:5100", name = "tcp-wkproto", network = "tcp", protocol = "wkproto", transport = "gnet" }, { address = "127.0.0.1:5101", name = "tcp-wkproto-2", network = "tcp", protocol = "wkproto", transport = "gnet" }]`, 1),
			want: "execution target",
		},
		"endpoint override rejection not sealed": {
			config: strings.Replace(valid, "endpoint_environment_overrides_rejected = true", "endpoint_environment_overrides_rejected = false", 1),
			want:   "typed reviewed settings",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			root, markerPath := writeLocalSingleNodeCompletionFixtureWithConfig(t, []byte(test.config))
			var stderr bytes.Buffer

			code := runWithStderr([]string{
				"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
			}, &stderr)

			if code != exitInternal || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("invalid topology exit/stderr = %d/%q", code, stderr.String())
			}
		})
	}
}

func TestLocalSingleNodeCompletionRejectsAbsentSummaryTarget(t *testing.T) {
	root, markerPath := writeLocalSingleNodeCompletionFixture(t)
	if err := os.Remove(filepath.Join(root, "summary.tsv")); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
	}, &stderr)

	if code != exitInternal || !strings.Contains(stderr.String(), "completion verification failed") {
		t.Fatalf("absent summary target exit/stderr = %d/%q", code, stderr.String())
	}
}

func TestLocalSingleNodeCompletionAuthenticatesCanonicalSummaryTargets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{
			name: "manifest omitted",
			mutate: func(t *testing.T, root, markerPath string) {
				manifestPath := filepath.Join(root, "checksums.sha256")
				data, err := os.ReadFile(manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				var kept []string
				for _, line := range strings.Split(string(data), "\n") {
					if line != "" && !strings.HasSuffix(line, "  storage_metrics_summary.tsv") {
						kept = append(kept, line)
					}
				}
				body := strings.Join(kept, "\n") + "\n"
				if err := os.WriteFile(manifestPath, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				rewriteLocalSingleNodeMarkerManifestDigest(t, markerPath, []byte(body))
			},
		},
		{
			name: "traversal",
			mutate: func(t *testing.T, _ string, markerPath string) {
				data, err := os.ReadFile(markerPath)
				if err != nil {
					t.Fatal(err)
				}
				var marker localSingleNodeCompletionMarker
				if err := json.Unmarshal(data, &marker); err != nil {
					t.Fatal(err)
				}
				marker.Summary = "../summary.tsv"
				if err := writeLocalSingleNodeJSON(markerPath, marker); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symlink",
			mutate: func(t *testing.T, root, _ string) {
				path := filepath.Join(root, "host_io_summary.tsv")
				real := path + ".real"
				if err := os.Rename(path, real); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "tamper",
			mutate: func(t *testing.T, root, _ string) {
				appendLocalSingleNodeCompletionTestFile(t, filepath.Join(root, "summary.tsv"), "# changed\n")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, markerPath := writeLocalSingleNodeCompletionFixture(t)
			test.mutate(t, root, markerPath)
			var stderr bytes.Buffer
			code := runWithStderr([]string{
				"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
			}, &stderr)
			if code != exitInternal || !strings.Contains(stderr.String(), "completion verification failed") {
				t.Fatalf("exit/stderr = %d/%q", code, stderr.String())
			}
		})
	}
}

func TestLocalSingleNodeCompletionRejectsManifestAuthenticatedMalformedSummaryContent(t *testing.T) {
	tests := []struct {
		name   string
		target string
		mutate func(string) string
	}{
		{name: "summary schema", target: "summary.tsv", mutate: func(body string) string {
			return strings.Replace(body, "offered_qps", "claimed_qps", 1)
		}},
		{name: "storage row binding", target: "storage_metrics_summary.tsv", mutate: func(body string) string {
			return strings.Replace(body, "000250\t127_0_0_1_5001", "000251\t127_0_0_1_5001", 1)
		}},
		{name: "host row binding", target: "host_io_summary.tsv", mutate: func(body string) string {
			return strings.Replace(body, "000250\thost-local", "000250\thost-other", 1)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, markerPath := writeLocalSingleNodeCompletionFixture(t)
			path := filepath.Join(root, test.target)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.mutate(string(data))), 0o600); err != nil {
				t.Fatal(err)
			}
			manifest := writeLocalSingleNodeCompletionManifest(t, root, filepath.Join(root, "checksums.sha256"))
			rewriteLocalSingleNodeMarkerManifestDigest(t, markerPath, []byte(manifest))
			var stderr bytes.Buffer
			code := runWithStderr([]string{
				"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
			}, &stderr)
			if code != exitInternal || !strings.Contains(stderr.String(), "completion verification failed") {
				t.Fatalf("exit/stderr = %d/%q", code, stderr.String())
			}
		})
	}
}

func TestLocalSingleNodeCompletionRejectsMarkerDataFilesystemIdentityMismatch(t *testing.T) {
	root, markerPath := writeLocalSingleNodeCompletionFixture(t)
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker localSingleNodeCompletionMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	marker.CanonicalDataDir = "/var/lib/other"
	if err := writeLocalSingleNodeJSON(markerPath, marker); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
	}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "completion verification failed") {
		t.Fatalf("filesystem identity mismatch exit/stderr = %d/%q", code, stderr.String())
	}
}

func rewriteLocalSingleNodeMarkerManifestDigest(t *testing.T, markerPath string, manifest []byte) {
	t.Helper()
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker localSingleNodeCompletionMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	marker.ArtifactManifestSHA256 = digestLocalSingleNodeBytes(manifest)
	if err := writeLocalSingleNodeJSON(markerPath, marker); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSingleNodePublishCommandCreatesMarkerOnceAfterVerification(t *testing.T) {
	root, markerPath := writeLocalSingleNodeCompletionFixture(t)
	draftPath := filepath.Join(root, "completion-draft.json")
	if err := os.Rename(markerPath, draftPath); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	args := []string{
		"report", "local-single-node-publish", "--root", root, "--draft", draftPath, "--output", markerPath,
	}
	if code := runWithStderr(args, &stderr); code != 0 {
		t.Fatalf("publish exit/stderr = %d/%q", code, stderr.String())
	}
	stderr.Reset()
	if code := runWithStderr(args, &stderr); code != exitInternal || !strings.Contains(stderr.String(), "publication failed") {
		t.Fatalf("collision exit/stderr = %d/%q", code, stderr.String())
	}
}

func TestLocalSingleNodePublishAndCompletionAcceptPreflightWithoutMeasuredArtifacts(t *testing.T) {
	root, draftPath, markerPath := writeLocalSingleNodeFilesystemIncompleteCompletionFixture(
		t, localbaseline.OutcomeInsufficientEvidence, "filesystem_preflight_unavailable", false,
	)
	for _, relative := range []string{"summary.tsv", "storage_metrics_summary.tsv", "host_io_summary.tsv"} {
		if _, err := os.Lstat(filepath.Join(root, relative)); !os.IsNotExist(err) {
			t.Fatalf("preflight fixture unexpectedly contains measured artifact %q: %v", relative, err)
		}
	}
	var stderr bytes.Buffer

	publishCode := runWithStderr([]string{
		"report", "local-single-node-publish", "--root", root, "--draft", draftPath, "--output", markerPath,
	}, &stderr)
	if publishCode != exitInternal || strings.Contains(stderr.String(), "failed") {
		t.Fatalf("publish exit/stderr = %d/%q, want typed insufficient-evidence exit without verification failure", publishCode, stderr.String())
	}
	if _, err := os.Lstat(markerPath); err != nil {
		t.Fatalf("published marker: %v", err)
	}

	stderr.Reset()
	completionCode := runWithStderr([]string{
		"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
	}, &stderr)
	if completionCode != exitInternal || strings.Contains(stderr.String(), "verification failed") {
		t.Fatalf("completion exit/stderr = %d/%q, want consumed typed insufficient-evidence decision", completionCode, stderr.String())
	}
}

func TestLocalSingleNodePublishRejectsCleanMarkerWithIncompleteFilesystemObservation(t *testing.T) {
	root, draftPath, markerPath := writeLocalSingleNodeFilesystemIncompleteCompletionFixture(
		t, localbaseline.OutcomeInsufficientEvidence, "filesystem_preflight_unavailable", true,
	)
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"report", "local-single-node-publish", "--root", root, "--draft", draftPath, "--output", markerPath,
	}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "draft verification failed") {
		t.Fatalf("contradictory publish exit/stderr = %d/%q", code, stderr.String())
	}
	if _, err := os.Lstat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("contradictory public marker exists: %v", err)
	}
}

func TestLocalSingleNodePublishRejectsNonFilesystemDecisionWhenFilesystemObservationIsIncomplete(t *testing.T) {
	for _, test := range []struct {
		name    string
		outcome localbaseline.Outcome
		reason  string
	}{
		{name: "unrelated insufficient evidence", outcome: localbaseline.OutcomeInsufficientEvidence, reason: "artifact_seal_verification_failed"},
		{name: "host confounded", outcome: localbaseline.OutcomeHostConfounded, reason: "overlapping_wukongim_workload"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, draftPath, markerPath := writeLocalSingleNodeFilesystemIncompleteCompletionFixture(t, test.outcome, test.reason, false)
			var stderr bytes.Buffer
			code := runWithStderr([]string{
				"report", "local-single-node-publish", "--root", root, "--draft", draftPath, "--output", markerPath,
			}, &stderr)
			if code != exitInternal || !strings.Contains(stderr.String(), "draft verification failed") {
				t.Fatalf("publish exit/stderr = %d/%q", code, stderr.String())
			}
			if _, err := os.Lstat(markerPath); !os.IsNotExist(err) {
				t.Fatalf("invalid public marker exists: %v", err)
			}
		})
	}
}

func TestLocalSingleNodePublishRequiresFilesystemObservationBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "absent", mutate: func(marker map[string]any) { delete(marker, "filesystem_observation_complete") }},
		{name: "contradicts evidence", mutate: func(marker map[string]any) { marker["filesystem_observation_complete"] = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, draftPath, markerPath := writeLocalSingleNodeFilesystemIncompleteCompletionFixture(
				t, localbaseline.OutcomeInsufficientEvidence, "filesystem_preflight_unavailable", false,
			)
			data, err := os.ReadFile(draftPath)
			if err != nil {
				t.Fatal(err)
			}
			var marker map[string]any
			if err := json.Unmarshal(data, &marker); err != nil {
				t.Fatal(err)
			}
			test.mutate(marker)
			if err := writeLocalSingleNodeJSON(draftPath, marker); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			code := runWithStderr([]string{
				"report", "local-single-node-publish", "--root", root, "--draft", draftPath, "--output", markerPath,
			}, &stderr)
			if code != exitInternal || !strings.Contains(stderr.String(), "draft verification failed") {
				t.Fatalf("publish exit/stderr = %d/%q", code, stderr.String())
			}
			if _, err := os.Lstat(markerPath); !os.IsNotExist(err) {
				t.Fatalf("unbound public marker exists: %v", err)
			}
		})
	}
}

func TestLocalSingleNodePublishNeverCreatesContradictoryCleanMarkerAfterFilesystemDrop(t *testing.T) {
	root, markerPath := writeLocalSingleNodeCompletionFixture(t)
	markerData, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(root, "reports", "local-baseline-evidence.json")
	evidenceData, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}
	var evidence localbaseline.BaselineEvidence
	if err := json.Unmarshal(evidenceData, &evidence); err != nil {
		t.Fatal(err)
	}
	evidence.ObservedFilesystemFreePercent = evidence.Settings.MinimumFreePercent - 1
	localbaseline.SealBaselineEvidence(&evidence)
	if err := writeLocalSingleNodeJSON(evidencePath, evidence); err != nil {
		t.Fatal(err)
	}
	authorization := localbaseline.AuthorizeThreeNodeDiagnostic(evidence)
	if authorization.Authorizes || authorization.Outcome != localbaseline.OutcomeStorageConfounded {
		t.Fatalf("filesystem authorization = %+v", authorization)
	}
	authorizationPath := filepath.Join(root, "reports", "local-baseline-authorization.json")
	if err := writeLocalSingleNodeJSON(authorizationPath, authorization); err != nil {
		t.Fatal(err)
	}
	manifest := writeLocalSingleNodeCompletionManifest(t, root, filepath.Join(root, "checksums.sha256"))
	authorizationData, err := os.ReadFile(authorizationPath)
	if err != nil {
		t.Fatal(err)
	}
	var contradictory localSingleNodeCompletionMarker
	if err := json.Unmarshal(markerData, &contradictory); err != nil {
		t.Fatal(err)
	}
	contradictory.CompletionGeneration = authorization.CompletionGeneration
	contradictory.ArtifactManifestSHA256 = digestLocalSingleNodeBytes([]byte(manifest))
	contradictory.TypedAuthorizationSHA256 = digestLocalSingleNodeBytes(authorizationData)
	contradictory.ObservedFilesystemFreePercent = evidence.ObservedFilesystemFreePercent
	// Deliberately retain the old clean/authorizing marker projection. The
	// publisher must reject it before creating the public path.
	draftPath := filepath.Join(root, "completion-draft.json")
	if err := writeLocalSingleNodeJSON(draftPath, contradictory); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-publish", "--root", root, "--draft", draftPath, "--output", markerPath,
	}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "draft verification failed") {
		t.Fatalf("contradictory publish exit/stderr = %d/%q", code, stderr.String())
	}
	if _, err := os.Lstat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("contradictory public marker exists: %v", err)
	}
}

func TestLocalSingleNodeCompletionCommandRejectsTampering(t *testing.T) {
	for _, target := range []string{"manifest", "authorization", "evidence", "identity", "config", "closure"} {
		t.Run(target, func(t *testing.T) {
			root, markerPath := writeLocalSingleNodeCompletionFixture(t)
			path := filepath.Join(root, "checksums.sha256")
			if target == "authorization" {
				path = filepath.Join(root, "reports", "local-baseline-authorization.json")
			} else if target == "evidence" {
				path = filepath.Join(root, "reports", "local-baseline-evidence.json")
			} else if target == "identity" {
				path = filepath.Join(root, "artifact-identity.tsv")
			} else if target == "config" {
				path = filepath.Join(root, "config", "effective-wukongim.toml")
			} else if target == "closure" {
				path = filepath.Join(root, "reports", "000250-qps", "evidence", "step-closure.json")
			}
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString("\n"); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			code := runWithStderr([]string{
				"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
			}, &stderr)
			if code != exitInternal || !strings.Contains(stderr.String(), "completion verification failed") {
				t.Fatalf("%s tamper exit/stderr = %d/%q", target, code, stderr.String())
			}
		})
	}
}

func TestLocalSingleNodeCompletionRejectsSymlinkedManifestAndMarkerComponents(t *testing.T) {
	for _, target := range []string{"manifest", "marker"} {
		t.Run(target, func(t *testing.T) {
			root, markerPath := writeLocalSingleNodeCompletionFixture(t)
			path := filepath.Join(root, "checksums.sha256")
			if target == "marker" {
				path = markerPath
			}
			real := path + ".real"
			if err := os.Rename(path, real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, path); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			code := runWithStderr([]string{"report", "local-single-node-completion", "--root", root, "--marker", markerPath}, &stderr)
			if code != exitInternal || !strings.Contains(stderr.String(), "completion verification failed") {
				t.Fatalf("symlinked %s exit/stderr = %d/%q", target, code, stderr.String())
			}
		})
	}
}

func TestLocalSingleNodeCompletionRejectsConsistentlyRewrittenAuthorizationEnvelope(t *testing.T) {
	root, markerPath := writeLocalSingleNodeCompletionFixture(t)
	authorizationPath := filepath.Join(root, "reports", "local-baseline-authorization.json")
	authorizationData, err := os.ReadFile(authorizationPath)
	if err != nil {
		t.Fatal(err)
	}
	var authorization localbaseline.AuthorizationResult
	if err := json.Unmarshal(authorizationData, &authorization); err != nil {
		t.Fatal(err)
	}
	authorization.Reason = "attacker_rewrote_authorization"
	if err := writeLocalSingleNodeJSON(authorizationPath, authorization); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "checksums.sha256")
	manifest := writeLocalSingleNodeCompletionManifest(t, root, manifestPath)
	markerData, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker localSingleNodeCompletionMarker
	if err := json.Unmarshal(markerData, &marker); err != nil {
		t.Fatal(err)
	}
	authorizationData, err = os.ReadFile(authorizationPath)
	if err != nil {
		t.Fatal(err)
	}
	marker.Reason = authorization.Reason
	marker.TypedAuthorizationSHA256 = digestLocalSingleNodeBytes(authorizationData)
	marker.ArtifactManifestSHA256 = digestLocalSingleNodeBytes([]byte(manifest))
	if err := writeLocalSingleNodeJSON(markerPath, marker); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	code := runWithStderr([]string{"report", "local-single-node-completion", "--root", root, "--marker", markerPath}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "recomputed authorization") {
		t.Fatalf("rewritten envelope exit/stderr = %d/%q", code, stderr.String())
	}
}

func TestLocalSingleNodeCompletionCommandRejectsTrailingJSONAndContradictoryMarker(t *testing.T) {
	for _, target := range []string{"marker-trailing", "authorization-trailing", "reviewed-contract-mismatch"} {
		t.Run(target, func(t *testing.T) {
			root, markerPath := writeLocalSingleNodeCompletionFixture(t)
			switch target {
			case "marker-trailing":
				appendLocalSingleNodeCompletionTestFile(t, markerPath, "{}")
			case "authorization-trailing":
				authorizationPath := filepath.Join(root, "reports", "local-baseline-authorization.json")
				appendLocalSingleNodeCompletionTestFile(t, authorizationPath, "{}")
				rewriteLocalSingleNodeCompletionManifest(t, root, markerPath, authorizationPath)
			case "reviewed-contract-mismatch":
				data, err := os.ReadFile(markerPath)
				if err != nil {
					t.Fatal(err)
				}
				var marker localSingleNodeCompletionMarker
				if err := json.Unmarshal(data, &marker); err != nil {
					t.Fatal(err)
				}
				marker.ReviewedContract = !marker.ReviewedContractSatisfied
				if err := writeLocalSingleNodeJSON(markerPath, marker); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			code := runWithStderr([]string{
				"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
			}, &stderr)
			if code != exitInternal {
				t.Fatalf("%s exit/stderr = %d/%q", target, code, stderr.String())
			}
		})
	}
}

func appendLocalSingleNodeCompletionTestFile(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func rewriteLocalSingleNodeCompletionManifest(t *testing.T, root, markerPath, authorizationPath string) {
	t.Helper()
	authorization, err := os.ReadFile(authorizationPath)
	if err != nil {
		t.Fatal(err)
	}
	authorizationDigest := sha256.Sum256(authorization)
	manifest := fmt.Sprintf("%x  reports/local-baseline-authorization.json\n", authorizationDigest)
	if err := os.WriteFile(filepath.Join(root, "checksums.sha256"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	markerData, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker localSingleNodeCompletionMarker
	if err := json.Unmarshal(markerData, &marker); err != nil {
		t.Fatal(err)
	}
	marker.TypedAuthorizationSHA256 = fmt.Sprintf("%x", authorizationDigest)
	marker.ArtifactManifestSHA256 = digestLocalSingleNodeBytes([]byte(manifest))
	if err := writeLocalSingleNodeJSON(markerPath, marker); err != nil {
		t.Fatal(err)
	}
}

func TestLocalSingleNodeCompletionCommandRejectsMarkerInsideManifest(t *testing.T) {
	root, markerPath := writeLocalSingleNodeCompletionFixture(t)
	markerData, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "checksums.sha256")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	markerDigest := sha256.Sum256(markerData)
	manifestData = append(manifestData, []byte(fmt.Sprintf("%x  local-baseline.json\n", markerDigest))...)
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	var marker localSingleNodeCompletionMarker
	if err := json.Unmarshal(markerData, &marker); err != nil {
		t.Fatal(err)
	}
	marker.ArtifactManifestSHA256 = digestLocalSingleNodeBytes(manifestData)
	writeLocalSingleNodeJSON(markerPath, marker)
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-completion", "--root", root, "--marker", markerPath,
	}, &stderr)
	if code != exitInternal || !strings.Contains(stderr.String(), "completion verification failed") {
		t.Fatalf("marker-in-manifest exit/stderr = %d/%q", code, stderr.String())
	}
}

func writeLocalSingleNodeCompletionFixture(t *testing.T) (string, string) {
	return writeLocalSingleNodeCompletionFixtureWithConfig(t, localSingleNodeReviewedEffectiveConfigFixture())
}

func writeLocalSingleNodeCompletionFixtureWithConfig(t *testing.T, configData []byte) (string, string) {
	t.Helper()
	root := t.TempDir()
	reports := filepath.Join(root, "reports")
	if err := os.Mkdir(reports, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config", "effective-wukongim.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	binDirectory := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	wukongimData, wkbenchData := []byte("sealed-wukongim"), []byte("sealed-wkbench")
	if err := os.WriteFile(filepath.Join(binDirectory, "wukongim"), wukongimData, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDirectory, "wkbench"), wkbenchData, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence := localSingleNodeEvidenceFixture()
	evidence.StepClosures = evidence.StepClosures[:0]
	for _, qps := range localbaseline.ReviewedOfferedSendQPS {
		evidence.StepClosures = append(evidence.StepClosures, writeLocalSingleNodeCompletionStepFixture(t, root, qps, evidence.Settings))
	}
	localbaseline.SealBaselineEvidence(&evidence)
	evidencePath := filepath.Join(reports, "local-baseline-evidence.json")
	writeAnyLocalSingleNodeJSONFixture(t, evidencePath, evidence)
	authorization := localbaseline.AuthorizeThreeNodeDiagnostic(evidence)
	authorizationPath := filepath.Join(reports, "local-baseline-authorization.json")
	if err := writeLocalSingleNodeJSON(authorizationPath, authorization); err != nil {
		t.Fatal(err)
	}
	authorizationData, err := os.ReadFile(authorizationPath)
	if err != nil {
		t.Fatal(err)
	}
	authorizationDigest := sha256.Sum256(authorizationData)
	identity := fmt.Sprintf("schema\twukongim/chat-lifecycle-local-single-node-artifact-identity/v1\nbaseline_invocation_id\t0123456789abcdef0123456789abcdef\nsource_revision\t%s\nsource_dirty\tfalse\nsource_rebuildable_from_revision\ttrue\nsource_capture\trevision_and_binary_identity\nseal_scope\tmeasured\ncanonical_data_dir\t/var/lib/wukongim\ndata_filesystem_device\t2049\ndata_filesystem_total_blocks\t100000\ndata_filesystem_block_size\t4096\noriginal_config_sha256\t%s\neffective_config\tconfig/effective-wukongim.toml\neffective_config_sha256\t%s\nwukongim_binary\tbin/wukongim\nwukongim_binary_sha256\t%s\nwkbench_binary\tbin/wkbench\nwkbench_binary_sha256\t%s\n",
		strings.Repeat("a", 40), strings.Repeat("d", 64), digestLocalSingleNodeBytes(configData),
		digestLocalSingleNodeBytes(wukongimData), digestLocalSingleNodeBytes(wkbenchData))
	if err := os.WriteFile(filepath.Join(root, "artifact-identity.tsv"), []byte(identity), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLocalSingleNodeGlobalSummaryFixtures(t, root, localbaseline.ReviewedOfferedSendQPS[:])
	manifestPath := filepath.Join(root, "checksums.sha256")
	manifest := writeLocalSingleNodeCompletionManifest(t, root, manifestPath)
	marker := localSingleNodeCompletionMarker{
		Schema: localSingleNodeCompletionSchema, CompletionMarker: true,
		BaselineInvocationID:     evidence.BaselineInvocationID,
		CompletionGeneration:     authorization.CompletionGeneration,
		ArtifactManifestSHA256:   digestLocalSingleNodeBytes([]byte(manifest)),
		TypedAuthorizationSHA256: fmt.Sprintf("%x", authorizationDigest),
		Outcome:                  string(authorization.Outcome), Reason: authorization.Reason,
		ReviewedContract:              authorization.ReviewedContractSatisfied,
		ReviewedContractSatisfied:     authorization.ReviewedContractSatisfied,
		ReviewedTypedEvidenceComplete: true, OnlineConnections: 2500,
		HighestCleanRate: authorization.HighestCleanRate, FirstFailingRate: authorization.FirstFailingRate,
		AuthorizesThreeNodeDiagnostic: authorization.Authorizes,
		QPSList:                       "250,500,750,1000", LogicalSlotGroups: 12, HashSlots: 256,
		SlotReplicas: 1, ChannelReplicas: 1, CommitCoordinatorFlushWindow: "200us",
		CommitCoordinatorShards: 1, SyncCommit: true, MinimumFilesystemFreePercent: 10,
		FilesystemObservationComplete: localSingleNodeCompletionBool(true), ObservedFilesystemFreePercent: 50, SourceRevision: strings.Repeat("a", 40),
		CanonicalDataDir: "/var/lib/wukongim", DataFilesystemDevice: "2049",
		DataFilesystemTotalBlocks: 100000, DataFilesystemBlockSize: 4096,
		SourceSealValid: true, ArtifactSealValid: true, ArtifactIdentity: "artifact-identity.tsv",
		TypedEvidence:      "reports/local-baseline-evidence.json",
		TypedAuthorization: "reports/local-baseline-authorization.json",
		EffectiveConfig:    "config/effective-wukongim.toml", Summary: "summary.tsv",
		StorageSummary: "storage_metrics_summary.tsv", HostIOSummary: "host_io_summary.tsv",
		ArtifactChecksums: "checksums.sha256",
	}
	markerPath := filepath.Join(root, "local-baseline.json")
	if err := writeLocalSingleNodeJSON(markerPath, marker); err != nil {
		t.Fatal(err)
	}
	return root, markerPath
}

func localSingleNodeReviewedEffectiveConfigFixture() []byte {
	return []byte(`[node]
id = 1

[cluster]
id = "wukongim-single"
listen_addr = "127.0.0.1:7001"
nodes = [{ id = 1, addr = "127.0.0.1:7001" }]
initial_slot_count = 10
hash_slot_count = 256
slot_replica_n = 1

[api]
listen_addr = "127.0.0.1:5001"
external_tcp_addr = "127.0.0.1:5100"

[gateway]
listeners = [{ address = "127.0.0.1:5100", name = "tcp-wkproto", network = "tcp", protocol = "wkproto", transport = "gnet" }]

[bench]
api_enable = true

[observability]
metrics_enable = true

[local_single_node_runtime]
topology_environment_overrides_rejected = true
endpoint_environment_overrides_rejected = true
initial_slot_count = 12
hash_slot_count = 256
slot_replica_n = 1
channel_replica_n = 1
commit_coordinator_flush_window = "200us"
commit_coordinator_shards = 1
commit_coordinator_sync = true
`)
}

func writeLocalSingleNodeFilesystemIncompleteCompletionFixture(
	t *testing.T,
	diagnosticOutcome localbaseline.Outcome,
	diagnosticReason string,
	contradictoryClean bool,
) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"reports", "config", "bin"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	evidence := localSingleNodeEvidenceFixture()
	evidence.StepClosures = make([]localbaseline.StepClosure, 0)
	evidence.DiagnosticOutcome = string(diagnosticOutcome)
	evidence.DiagnosticReason = diagnosticReason
	evidence.FilesystemObservationComplete = false
	evidence.ObservedFilesystemFreePercent = 0
	evidence.CanonicalDataDir = "/var/lib/wukongim"
	evidence.DataFilesystemDevice = "unavailable"
	evidence.DataFilesystemTotalBlocks = 0
	evidence.DataFilesystemBlockSize = 0
	evidence.Seal = localbaseline.SealEvidence{}
	localbaseline.SealBaselineEvidence(&evidence)
	evidencePath := filepath.Join(root, "reports", "local-baseline-evidence.json")
	writeAnyLocalSingleNodeJSONFixture(t, evidencePath, evidence)
	authorization := localbaseline.AuthorizeThreeNodeDiagnostic(evidence)
	wantExit := exitInternal
	if diagnosticOutcome == localbaseline.OutcomeHostConfounded || diagnosticOutcome == localbaseline.OutcomeStorageConfounded {
		wantExit = exitPreflight
	}
	if authorization.Authorizes || authorization.Outcome != diagnosticOutcome ||
		authorization.ExitCode != wantExit || authorization.Reason != diagnosticReason {
		t.Fatalf("filesystem-incomplete authorization = %+v", authorization)
	}
	authorizationPath := filepath.Join(root, "reports", "local-baseline-authorization.json")
	if err := writeLocalSingleNodeJSON(authorizationPath, authorization); err != nil {
		t.Fatal(err)
	}
	authorizationData, err := os.ReadFile(authorizationPath)
	if err != nil {
		t.Fatal(err)
	}
	configData := []byte{}
	if err := os.WriteFile(filepath.Join(root, "config", "effective-wukongim.toml"), configData, 0o600); err != nil {
		t.Fatal(err)
	}
	wkbenchData := []byte("sealed-preflight-wkbench")
	if err := os.WriteFile(filepath.Join(root, "bin", "wkbench"), wkbenchData, 0o700); err != nil {
		t.Fatal(err)
	}
	identity := fmt.Sprintf("schema\twukongim/chat-lifecycle-local-single-node-artifact-identity/v1\nbaseline_invocation_id\t0123456789abcdef0123456789abcdef\nsource_revision\t%s\nsource_dirty\tfalse\nsource_rebuildable_from_revision\ttrue\nsource_capture\tbinary_identity_only\nseal_scope\tpreflight\ncanonical_data_dir\t/var/lib/wukongim\ndata_filesystem_device\tunavailable\ndata_filesystem_total_blocks\t0\ndata_filesystem_block_size\t0\noriginal_config_sha256\t%s\neffective_config\tconfig/effective-wukongim.toml\neffective_config_sha256\t%s\nwukongim_binary\tbin/wukongim\nwukongim_binary_sha256\tunavailable\nwkbench_binary\tbin/wkbench\nwkbench_binary_sha256\t%s\n",
		strings.Repeat("a", 40), strings.Repeat("d", 64), digestLocalSingleNodeBytes(configData), digestLocalSingleNodeBytes(wkbenchData))
	if err := os.WriteFile(filepath.Join(root, "artifact-identity.tsv"), []byte(identity), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := writeLocalSingleNodeCompletionManifest(t, root, filepath.Join(root, "checksums.sha256"))
	marker := localSingleNodeCompletionMarker{
		Schema: localSingleNodeCompletionSchema, CompletionMarker: true,
		BaselineInvocationID:     evidence.BaselineInvocationID,
		CompletionGeneration:     authorization.CompletionGeneration,
		ArtifactManifestSHA256:   digestLocalSingleNodeBytes([]byte(manifest)),
		TypedAuthorizationSHA256: digestLocalSingleNodeBytes(authorizationData),
		Outcome:                  string(authorization.Outcome), Reason: authorization.Reason,
		ReviewedContract: authorization.ReviewedContractSatisfied, ReviewedContractSatisfied: authorization.ReviewedContractSatisfied,
		ReviewedTypedEvidenceComplete: false, OnlineConnections: evidence.Settings.ActiveConnections,
		HighestCleanRate: authorization.HighestCleanRate, FirstFailingRate: authorization.FirstFailingRate,
		AuthorizesThreeNodeDiagnostic: authorization.Authorizes,
		QPSList:                       "250,500,750,1000", LogicalSlotGroups: evidence.Settings.LogicalSlotGroups, HashSlots: evidence.Settings.HashSlots,
		SlotReplicas: evidence.Settings.SlotReplicas, ChannelReplicas: evidence.Settings.ChannelReplicas,
		CommitCoordinatorFlushWindow: fmt.Sprintf("%dus", evidence.Settings.CommitFlushWindowMicros),
		CommitCoordinatorShards:      evidence.Settings.CommitCoordinatorShards, SyncCommit: evidence.Settings.SyncCommit,
		MinimumFilesystemFreePercent:  evidence.Settings.MinimumFreePercent,
		FilesystemObservationComplete: localSingleNodeCompletionBool(false),
		ObservedFilesystemFreePercent: evidence.ObservedFilesystemFreePercent,
		CanonicalDataDir:              evidence.CanonicalDataDir, DataFilesystemDevice: evidence.DataFilesystemDevice,
		DataFilesystemTotalBlocks: evidence.DataFilesystemTotalBlocks, DataFilesystemBlockSize: evidence.DataFilesystemBlockSize,
		SourceRevision: strings.Repeat("a", 40), SourceSealValid: true, ArtifactSealValid: true,
		ArtifactIdentity: "artifact-identity.tsv", TypedEvidence: "reports/local-baseline-evidence.json",
		TypedAuthorization: "reports/local-baseline-authorization.json", EffectiveConfig: "config/effective-wukongim.toml",
		Summary: "summary.tsv", StorageSummary: "storage_metrics_summary.tsv", HostIOSummary: "host_io_summary.tsv",
		ArtifactChecksums: "checksums.sha256",
	}
	if contradictoryClean {
		marker.Outcome = string(localbaseline.OutcomeClean)
		marker.Reason = "complete"
		marker.ReviewedContract = true
		marker.ReviewedContractSatisfied = true
		marker.AuthorizesThreeNodeDiagnostic = true
	}
	draftPath := filepath.Join(root, "completion-draft.json")
	if err := writeLocalSingleNodeJSON(draftPath, marker); err != nil {
		t.Fatal(err)
	}
	return root, draftPath, filepath.Join(root, "local-baseline.json")
}

func localSingleNodeCompletionBool(value bool) *bool {
	return &value
}

func writeLocalSingleNodeGlobalSummaryFixtures(t *testing.T, root string, qpsList []int) {
	t.Helper()
	const summaryHeader = "tag\toffered_qps\tstatus\texit_status\tactual_qps\tsend_success\tsend_errors\tconnect_error_rate\tsendack_error_rate\tp50_seconds\tp95_seconds\tp99_seconds\tmax_seconds\tconnect_success\tscheduler_planned\tscheduler_dispatched\tscheduler_dropped\n"
	var summary, storage, host strings.Builder
	summary.WriteString(summaryHeader)
	storage.WriteString(strings.Join(localStepStorageHeader, "\t") + "\n")
	host.WriteString(strings.Join(localStepHostIOHeader, "\t") + "\n")
	for _, qps := range qpsList {
		planned := qps * 300
		fmt.Fprintf(&summary, "%06d\t%d\tpassed\t0\t%d.000000\t%d\t0\t0.000000\t0.000000\t0.001000\t0.002000\t0.003000\t0.004000\t2500\t%d\t%d\t0\n",
			qps, qps, qps, planned, planned, planned)
		storage.WriteString(strings.SplitN(localSingleNodeStorageSummaryFixture(qps), "\n", 2)[1])
		host.WriteString(strings.SplitN(localSingleNodeHostIOSummaryFixture(qps), "\n", 2)[1])
	}
	for path, body := range map[string]string{
		"summary.tsv": summary.String(), "storage_metrics_summary.tsv": storage.String(), "host_io_summary.tsv": host.String(),
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func writeLocalSingleNodeCompletionStepFixture(
	t *testing.T,
	root string,
	qps int,
	settings localbaseline.ReviewedSettings,
) localbaseline.StepClosure {
	return writeLocalSingleNodeCompletionStepFixtureWithMutation(t, root, qps, settings, nil, 0)
}

func writeLocalSingleNodeCompletionStepFixtureWithMutation(
	t *testing.T,
	root string,
	qps int,
	settings localbaseline.ReviewedSettings,
	mutate func(*localbaseline.StepEvidence),
	wantExit int,
) localbaseline.StepClosure {
	t.Helper()
	directory := filepath.Join(root, "reports", fmt.Sprintf("%06d-qps", qps))
	evidenceDirectory := filepath.Join(directory, "evidence")
	if err := os.MkdirAll(evidenceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	step := localSingleNodeStepFixture(qps, settings)
	if mutate != nil {
		mutate(&step)
	}
	diagnosticPath := filepath.Join(directory, "diagnostic-summary.json")
	scenarioPath := filepath.Join(directory, "scenario.yaml")
	planPath := filepath.Join(directory, "plan.json")
	reportPath := filepath.Join(directory, "report.json")
	lifecyclePath := filepath.Join(evidenceDirectory, "lifecycle.jsonl")
	baselineMetricsPath := filepath.Join(evidenceDirectory, "127_0_0_1_5001-post-warmup.prom")
	terminalMetricsPath := filepath.Join(evidenceDirectory, "terminal.prom")
	storagePath := filepath.Join(evidenceDirectory, "storage-overlap.tsv")
	storageSummaryPath := filepath.Join(evidenceDirectory, "storage-summary.tsv")
	hostIOSummaryPath := filepath.Join(evidenceDirectory, "host-io-summary.tsv")
	profilePath := filepath.Join(evidenceDirectory, "threshold-pprof-status.json")
	writeLocalSingleNodeReviewedExecutionFixture(t, scenarioPath, planPath, reportPath, step, settings)
	writeLocalSingleNodeDiagnosticFixture(t, diagnosticPath, step)
	writeLocalSingleNodeLifecycleFixture(t, lifecyclePath, step)
	if err := os.WriteFile(baselineMetricsPath, []byte(localSingleNodeProductQueueMetrics(step.ProductQueues.PostWarmupCut)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(terminalMetricsPath, []byte(localSingleNodeProductQueueMetrics(step.ProductQueues.TerminalCut)), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLocalSingleNodeStorageOverlapFixture(t, storagePath, step.StorageOverlap)
	writeLocalSingleNodeSummaryFixtures(t, storageSummaryPath, hostIOSummaryPath, qps)
	writeLocalSingleNodeNotTriggeredProfileFixture(t, profilePath)
	prefix := filepath.ToSlash(strings.TrimPrefix(directory, root+string(filepath.Separator)))
	entries := []string{
		prefix + "/scenario.yaml", prefix + "/plan.json", prefix + "/report.json", prefix + "/diagnostic-summary.json", prefix + "/evidence/lifecycle.jsonl",
		prefix + "/evidence/127_0_0_1_5001-post-warmup.prom", prefix + "/evidence/terminal.prom",
		prefix + "/evidence/storage-overlap.tsv", prefix + "/evidence/storage-summary.tsv",
		prefix + "/evidence/host-io-summary.tsv", prefix + "/evidence/threshold-pprof-status.json",
	}
	for _, relative := range localSingleNodeStorageManifestPaths(step.StorageOverlap) {
		entries = append(entries, prefix+"/"+relative)
	}
	entries = append(entries, ensureLocalSingleNodeExecutionPayloadFixture(t, root, qps)...)
	rawManifest := filepath.Join(evidenceDirectory, "step-checksums.sha256")
	writeLocalSingleNodeChecksumManifest(t, root, rawManifest, entries)
	evidencePath := filepath.Join(evidenceDirectory, "typed-step-evidence.json")
	resultPath := filepath.Join(evidenceDirectory, "typed-step-result.json")
	closurePath := filepath.Join(evidenceDirectory, "step-closure.json")
	var stderr bytes.Buffer
	code := runWithStderr([]string{
		"report", "local-single-node-step", "--offered-qps", fmt.Sprint(qps),
		"--required-active-connections", "2500", "--group-members", "10", "--warmup-seconds", "60", "--measured-seconds", "300",
		"--drain-budget-seconds", "90", "--maximum-sample-gap-seconds", "30",
		"--scenario", scenarioPath, "--plan", planPath, "--run-report", reportPath, "--diagnostic-summary", diagnosticPath, "--lifecycle", lifecyclePath,
		"--post-warmup-metrics", baselineMetricsPath, "--terminal-metrics", terminalMetricsPath,
		"--storage-overlap", storagePath, "--storage-summary", storageSummaryPath,
		"--host-io-summary", hostIOSummaryPath, "--profile-status", profilePath,
		"--payload-root", root, "--payload-manifest", rawManifest,
		"--output", evidencePath, "--result-output", resultPath, "--closure-output", closurePath,
	}, &stderr)
	if code != wantExit {
		resultData, _ := os.ReadFile(resultPath)
		t.Fatalf("step %d fixture exit=%d want=%d stderr=%q result=%s", qps, code, wantExit, stderr.String(), resultData)
	}
	rootHandle, err := openLocalSingleNodeArtifactRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	closure, err := verifyLocalSingleNodeStepClosure(rootHandle, prefix+"/evidence/step-closure.json")
	if err != nil {
		t.Fatal(err)
	}
	return closure
}

func writeLocalSingleNodeCompletionManifest(t *testing.T, root, manifestPath string) string {
	t.Helper()
	var entries []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || path == manifestPath || filepath.Base(path) == "local-baseline.json" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(relative))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	writeLocalSingleNodeChecksumManifest(t, root, manifestPath, entries)
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
