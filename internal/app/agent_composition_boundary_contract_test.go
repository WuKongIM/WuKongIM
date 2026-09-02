package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	issuecli "github.com/WuKongIM/WuKongIM/internal/access/issueagentcli"
	reviewcli "github.com/WuKongIM/WuKongIM/internal/access/reviewagentcli"
	issuecontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
	reviewcontract "github.com/WuKongIM/WuKongIM/internal/contracts/reviewagent"
	issuegithub "github.com/WuKongIM/WuKongIM/internal/infra/issueagentgithub"
	reviewgithub "github.com/WuKongIM/WuKongIM/internal/infra/reviewagentgithub"
	issueusecase "github.com/WuKongIM/WuKongIM/internal/usecase/issueagent"
)

func TestIssueAgentCompositionRejectsUnscopedCommandsBeforeExternalAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	operations := NewIssueAgentOperations(IssueAgentConfig{})

	calls := []struct {
		name string
		call func() error
	}{
		{name: "reconcile", call: func() error {
			_, err := operations.ReconcileGitHub(ctx, issuecli.ReconcileGitHubRequest{
				Repository: "WuKongIM/WuKongIM", Now: time.Now().UTC(),
			})
			return err
		}},
		{name: "recover", call: func() error {
			_, err := operations.RecoverTask(ctx, issuecli.RecoverTaskRequest{Repository: "WuKongIM/WuKongIM"})
			return err
		}},
		{name: "context", call: func() error {
			_, err := operations.BuildContext(ctx, issuecli.BuildContextRequest{Repository: "WuKongIM/WuKongIM"})
			return err
		}},
		{name: "publish", call: func() error {
			_, err := operations.PublishCandidate(ctx, issuecli.PublishCandidateRequest{Repository: "WuKongIM/WuKongIM"})
			return err
		}},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			if err := call.call(); err == nil || !strings.Contains(err.Error(), "composition is incomplete") {
				t.Fatalf("operation error = %v, want fail-closed composition error", err)
			}
		})
	}
	if _, err := operations.MintAppToken(ctx, issuecli.MintAppTokenRequest{Repository: "other/repository"}); err == nil || !strings.Contains(err.Error(), "repository is invalid") {
		t.Fatalf("MintAppToken() error = %v, want repository scope rejection", err)
	}
}

func TestReviewAgentCompositionRejectsIncompleteRoleWiringBeforeExternalAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	operations := NewReviewAgentOperations(ReviewAgentConfig{})
	generation := reviewcontract.GenerationIdentity{
		Repository: "WuKongIM/WuKongIM", PullRequest: 42,
		HeadSHA: strings.Repeat("a", 40), BaseSHA: strings.Repeat("b", 40),
		TestMergeSHA: strings.Repeat("c", 40),
		IntentDigest: "sha256:" + strings.Repeat("d", 64), Generation: 1,
		StateParentSHA: strings.Repeat("e", 40),
	}
	staleGeneration := generation
	staleGeneration.Generation++

	for _, call := range []struct {
		name string
		call func() error
	}{
		{name: "reconcile", call: func() error {
			_, err := operations.ReconcileGitHub(ctx, reviewcli.ReconcileGitHubRequest{})
			return err
		}},
		{name: "recover", call: func() error {
			_, err := operations.RecoverReview(ctx, reviewcli.ReconcileGitHubRequest{})
			return err
		}},
		{name: "build context", call: func() error {
			_, err := operations.BuildContext(ctx, reviewcli.BuildContextRequest{})
			return err
		}},
		{name: "verify baseline", call: func() error {
			_, err := operations.VerifyBaseline(ctx, reviewcli.VerifyBaselineRequest{})
			return err
		}},
		{name: "validate result", call: func() error {
			_, err := operations.ValidateReviewResult(ctx, reviewcli.ValidateReviewResultRequest{})
			return err
		}},
		{name: "validate explanation", call: func() error {
			_, err := operations.ValidateExplanation(ctx, reviewcli.ValidateExplanationRequest{
				Generation: generation,
				Result: reviewcontract.ExplanationResult{
					SchemaVersion: 1, Generation: staleGeneration, Reply: "explanation",
				},
			})
			return err
		}},
		{name: "append state", call: func() error {
			_, err := operations.AppendState(ctx, reviewcli.AppendStateRequest{Kind: "unknown"})
			return err
		}},
		{name: "publish", call: func() error {
			_, err := operations.PublishReview(ctx, reviewcli.PublishReviewRequest{})
			return err
		}},
	} {
		t.Run(call.name, func(t *testing.T) {
			if err := call.call(); err == nil {
				t.Fatal("operation unexpectedly accepted incomplete composition")
			}
		})
	}

	if _, err := newReviewGitHubClient(ReviewAgentConfig{}, "token"); err == nil || !strings.Contains(err.Error(), "HTTP client is unavailable") {
		t.Fatalf("newReviewGitHubClient() error = %v", err)
	}
	if _, err := mintReviewAppToken(ctx, ReviewAgentConfig{}, nil, reviewgithub.AppRoleStateWriter, "state-writer"); err == nil || !strings.Contains(err.Error(), "role configuration is unavailable") {
		t.Fatalf("mintReviewAppToken() error = %v", err)
	}
}

func TestIssueAgentTrustedFilesUseStrictBoundedDecoding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	if err := os.WriteFile(eventPath, []byte(`{"issue":{"number":42}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	event, err := readIssueAgentEvent(eventPath)
	if err != nil || event.Issue.Number != 42 {
		t.Fatalf("readIssueAgentEvent() = %#v, %v", event, err)
	}

	for _, body := range []string{`{`, `{"issue":{"number":42}} {}`} {
		if err := os.WriteFile(eventPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readIssueAgentEvent(eventPath); err == nil {
			t.Fatalf("readIssueAgentEvent() accepted %q", body)
		}
	}
	if _, err := readIssueAgentEvent(""); err == nil {
		t.Fatal("readIssueAgentEvent() accepted an empty path")
	}
	if err := os.WriteFile(eventPath, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readIssueAgentEvent(eventPath); err == nil {
		t.Fatal("readIssueAgentEvent() accepted an oversized event")
	}

	trustedPath := filepath.Join(dir, "trusted.txt")
	trustedBody := []byte("frozen control artifact\n")
	if err := os.WriteFile(trustedPath, trustedBody, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := digestFile(trustedPath)
	if err != nil {
		t.Fatalf("digestFile() error = %v", err)
	}
	sum := sha256.Sum256(trustedBody)
	if want := "sha256:" + hex.EncodeToString(sum[:]); digest != want {
		t.Fatalf("digestFile() = %q, want %q", digest, want)
	}
	if _, err := digestFile(dir); err == nil {
		t.Fatal("digestFile() accepted a directory")
	}
	if _, err := digestJSON(make(chan int)); err == nil {
		t.Fatal("digestJSON() accepted a non-JSON value")
	}

	for name, read := range map[string]func(string) error{
		"context":   func(path string) error { _, err := readContextBundle(path); return err },
		"engineer":  func(path string) error { _, err := readEngineerResult(path); return err },
		"candidate": func(path string) error { _, err := readCandidate(path); return err },
		"evidence":  func(path string) error { _, err := readEvidence(path); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := read(filepath.Join(dir, "missing-"+name)); err == nil {
				t.Fatal("trusted artifact reader accepted a missing file")
			}
		})
	}
}

func TestIssueAgentPolicyIsStrictAndDigestBound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policyDir := filepath.Join(dir, ".github", "issue-agent")
	if err := os.MkdirAll(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(policyDir, "policy.json")
	valid := []byte(`{
		"schema_version":2,
		"enabled":true,
		"rollout_mode":"active",
		"default_branch":"main",
		"publisher_environment":"issue-agent-publisher",
		"engineer":{"action_sha":"frozen","codex_version":"1","model":"codex","reasoning_effort":"high","sandbox":"workspace-write","network_access":true,"ephemeral":true,"wall_time_seconds":60,"modify_test_iterations":1},
		"budgets":{"max_engineer_attempts_per_issue":1,"max_review_iterations":1,"max_base_syncs_per_issue":1,"task_stale_after_seconds":60},
		"candidate_limits":{},
		"protected_paths":[],"high_risk_paths":[],"high_risk_topics":[],
		"required_suites":["unit"],"verification_commands":[{}],"knowledge_paths":[]
	}`)
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	policy, digest, err := loadIssueAgentPolicy(IssueAgentConfig{WorkingDirectory: dir})
	if err != nil || !policy.Enabled {
		t.Fatalf("loadIssueAgentPolicy() policy=%#v err=%v", policy, err)
	}
	sum := sha256.Sum256(valid)
	if want := "sha256:" + hex.EncodeToString(sum[:]); digest != want {
		t.Fatalf("policy digest = %q, want %q", digest, want)
	}

	for name, body := range map[string][]byte{
		"unknown field": append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"unexpected":true}`)...),
		"trailing json": append(append([]byte(nil), valid...), []byte(` {}`)...),
		"disabled":      []byte(`{"schema_version":2,"enabled":false}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadIssueAgentPolicy(IssueAgentConfig{WorkingDirectory: dir}); err == nil {
				t.Fatal("loadIssueAgentPolicy() accepted an untrusted policy")
			}
		})
	}
}

func TestIssueAgentVersionSelectionFailsClosedAndUsesImmutableObjects(t *testing.T) {
	t.Parallel()
	defaultSHA := strings.Repeat("a", 40)
	commitSHA := strings.Repeat("b", 40)
	tagSHA := strings.Repeat("c", 40)
	client := newIssueAgentMemoryClient(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/repos/WuKongIM/WuKongIM/git/commits/" + commitSHA:
			return appContractHTTPResponse(http.StatusOK, `{"sha":"`+commitSHA+`"}`), nil
		case "/repos/WuKongIM/WuKongIM/git/ref/tags/v2.1.0":
			return appContractHTTPResponse(http.StatusOK, `{"ref":"refs/tags/v2.1.0","object":{"type":"commit","sha":"`+tagSHA+`"}}`), nil
		default:
			return appContractHTTPResponse(http.StatusNotFound, `{}`), nil
		}
	})

	tests := []struct {
		name        string
		body        string
		wantSHA     string
		wantMissing string
	}{
		{name: "no answer", body: "### Affected version\n_No response_", wantSHA: defaultSHA},
		{name: "ambiguous", body: "### Affected version\nv2.0.0\n### Affected version\nv2.1.0", wantSHA: defaultSHA, wantMissing: "more than once"},
		{name: "invalid ref", body: "### Affected version\nmain", wantSHA: defaultSHA, wantMissing: "release tag or full commit"},
		{name: "commit", body: "### Affected version\n" + commitSHA, wantSHA: commitSHA},
		{name: "missing commit", body: "### Affected version\n" + strings.Repeat("d", 40), wantSHA: defaultSHA, wantMissing: "does not exist"},
		{name: "tag", body: "### Affected version\nv2.1.0", wantSHA: tagSHA},
		{name: "missing tag", body: "### Affected version\nv9.9.9", wantSHA: defaultSHA, wantMissing: "does not exist"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sha, missing, err := resolveAffectedSource(context.Background(), client, test.body, defaultSHA)
			if err != nil || sha != test.wantSHA || !strings.Contains(missing, test.wantMissing) {
				t.Fatalf("resolveAffectedSource() = (%q, %q, %v), want (%q, contains %q, nil)", sha, missing, err, test.wantSHA, test.wantMissing)
			}
		})
	}
}

func TestIssueAgentReviewWakeupRequiresExactDurableWorkIdentity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	current := &issuecontract.IssueAgentState{
		State: issuecontract.IssueStateDraft,
		Work: &issuecontract.IssueWork{
			Branch: "agent/issue-42", PullRequest: 77, HeadSHA: strings.Repeat("a", 40),
		},
	}

	if authorization, digest, err := currentReviewAuthorization(context.Background(), nil, issuecli.ReconcileGitHubRequest{}, nil, "review-agent[bot]"); err != nil || authorization != nil || digest != "" {
		t.Fatalf("nil current authorization = %#v, %q, %v", authorization, digest, err)
	}
	writeEvent := func(name, body string) string {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	repositoryMismatch := writeEvent("repository", `{"workflow_run":{"id":9,"name":"Safety Automation - Issue Agent PR Signal","event":"pull_request_review","conclusion":"success","head_branch":"agent/issue-42","head_repository":{"full_name":"other/repo"},"pull_requests":[{"number":77,"head":{"ref":"agent/issue-42"}}],"actor":{"login":"review-agent[bot]"}}}`)
	if _, _, err := currentReviewAuthorization(context.Background(), nil, issuecli.ReconcileGitHubRequest{Repository: "WuKongIM/WuKongIM", EventName: "workflow_run", EventPath: repositoryMismatch}, current, "review-agent[bot]"); err == nil || !strings.Contains(err.Error(), "repository does not match") {
		t.Fatalf("repository mismatch error = %v", err)
	}

	irrelevant := writeEvent("irrelevant", `{}`)
	if authorization, _, err := currentReviewAuthorization(context.Background(), nil, issuecli.ReconcileGitHubRequest{EventName: "push", EventPath: irrelevant}, current, "review-agent[bot]"); err != nil || authorization != nil {
		t.Fatalf("irrelevant wakeup = %#v, %v", authorization, err)
	}

	mismatch := writeEvent("mismatch", `{"pull_request":{"number":78,"head":{"ref":"agent/issue-42"}},"review":{"id":5},"sender":{"login":"review-agent[bot]"}}`)
	if _, _, err := currentReviewAuthorization(context.Background(), nil, issuecli.ReconcileGitHubRequest{EventName: "pull_request_review", EventPath: mismatch}, current, "review-agent[bot]"); err == nil || !strings.Contains(err.Error(), "does not match Agent work") {
		t.Fatalf("work mismatch error = %v", err)
	}

	matching := writeEvent("matching", `{"pull_request":{"number":77,"head":{"ref":"agent/issue-42"}},"review":{"id":5},"sender":{"login":"review-agent[bot]"}}`)
	if authorization, _, err := currentReviewAuthorization(context.Background(), nil, issuecli.ReconcileGitHubRequest{EventName: "pull_request_review", EventPath: matching}, current, ""); err != nil || authorization != nil {
		t.Fatalf("unconfigured review identity = %#v, %v", authorization, err)
	}
}

func TestIssueAgentContextRequiresTaskAuthorizationAndReviewIdentity(t *testing.T) {
	t.Parallel()
	if _, err := buildContextForState(context.Background(), IssueAgentConfig{}, nil, issueAgentPolicy{}, issuecontract.IssueAgentState{}); err == nil || !strings.Contains(err.Error(), "trusted authorization") {
		t.Fatalf("buildContextForState() error = %v", err)
	}
	state := issuecontract.IssueAgentState{
		Task:          &issuecontract.TaskIdentity{Kind: issuecontract.TaskKindReview},
		Authorization: &issuecontract.AuthorizationRecord{},
	}
	if _, err := buildContextForState(context.Background(), IssueAgentConfig{}, nil, issueAgentPolicy{}, state); err == nil || !strings.Contains(err.Error(), "Review Agent App identity") {
		t.Fatalf("buildContextForState() review error = %v", err)
	}
}

func TestIssueAgentClientAndStateStoreStayRepositoryScoped(t *testing.T) {
	t.Parallel()
	clientConfig := IssueAgentConfig{
		HTTPClient: &http.Client{Transport: appContractRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return appContractHTTPResponse(http.StatusTeapot, `{}`), nil
		})},
		APIBaseURL:       "https://api.invalid",
		Repository:       "WuKongIM/WuKongIM",
		GitHubToken:      "read-token",
		AppLogin:         "issue-agent[bot]",
		WorkingDirectory: t.TempDir(),
		Now:              func() time.Time { return time.Unix(1, 0).UTC() },
	}
	client, err := issueAgentClient(context.Background(), clientConfig, false)
	if err != nil {
		t.Fatalf("issueAgentClient() error = %v", err)
	}
	if _, err := issueAgentStateStore(clientConfig, client); err != nil {
		t.Fatalf("issueAgentStateStore() error = %v", err)
	}
	if err := validateCompositionConfig(clientConfig, clientConfig.Repository); err != nil {
		t.Fatalf("validateCompositionConfig() error = %v", err)
	}
	for _, repository := range []string{"", "other/repo"} {
		if err := validateCompositionConfig(clientConfig, repository); err == nil {
			t.Fatalf("validateCompositionConfig() accepted repository %q", repository)
		}
	}
}

func TestIssueAgentStatusProjectionIsSingleOwnedAndIdempotent(t *testing.T) {
	t.Parallel()
	state := validIssueAgentProjectionState()
	rendered, err := issueusecase.RenderIssueStatus(state)
	if err != nil {
		t.Fatalf("render issue status: %v", err)
	}

	if id, err := ensureIssueStatus(context.Background(), nil, "issue-agent[bot]", nil, issuecontract.IssueAgentState{StatusCommentID: 19}); err != nil || id != 19 {
		t.Fatalf("ensureIssueStatus() existing id = %d, %v", id, err)
	}
	comments := []issuegithub.IssueComment{{ID: 7, Author: "issue-agent[bot]", AuthorType: "Bot", Body: issueAgentStatusMarker}}
	if id, err := ensureIssueStatus(context.Background(), nil, "issue-agent[bot]", comments, state); err != nil || id != 7 {
		t.Fatalf("ensureIssueStatus() discovered id = %d, %v", id, err)
	}
	comments = append(comments, issuegithub.IssueComment{ID: 8, Author: "issue-agent[bot]", AuthorType: "Bot", Body: issueAgentStatusMarker})
	if _, err := ensureIssueStatus(context.Background(), nil, "issue-agent[bot]", comments, state); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("ensureIssueStatus() duplicate error = %v", err)
	}

	createdClient := newIssueAgentMemoryClient(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/repos/WuKongIM/WuKongIM/issues/42/comments" {
			t.Fatalf("unexpected create request %s %s", request.Method, request.URL.Path)
		}
		var input struct {
			Body string `json:"body"`
		}
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatalf("decode create body: %v", err)
		}
		if input.Body != rendered {
			t.Fatalf("created body = %q, want canonical status", input.Body)
		}
		return appContractJSONResponse(http.StatusCreated, map[string]any{
			"id": 9, "user": map[string]any{"login": "issue-agent[bot]", "type": "Bot"},
			"body": input.Body, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
		}), nil
	})
	if id, err := ensureIssueStatus(context.Background(), createdClient, "issue-agent[bot]", nil, state); err != nil || id != 9 {
		t.Fatalf("ensureIssueStatus() created id = %d, %v", id, err)
	}

	state.StatusCommentID = 9
	patchCalls := 0
	repairClient := newIssueAgentMemoryClient(t, func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodGet:
			return appContractJSONResponse(http.StatusOK, map[string]any{
				"id": 9, "issue_url": "https://api.invalid/repos/WuKongIM/WuKongIM/issues/42",
				"user": map[string]any{"login": "issue-agent[bot]", "type": "Bot"},
				"body": "stale status", "author_association": "MEMBER",
				"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
			}), nil
		case http.MethodPatch:
			patchCalls++
			var input struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatalf("decode repair body: %v", err)
			}
			return appContractJSONResponse(http.StatusOK, map[string]any{
				"id": 9, "user": map[string]any{"login": "issue-agent[bot]", "type": "Bot"},
				"body": input.Body, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:01Z",
			}), nil
		default:
			t.Fatalf("unexpected repair request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	if err := repairIssueStatus(context.Background(), repairClient, "issue-agent[bot]", state); err != nil {
		t.Fatalf("repairIssueStatus() error = %v", err)
	}
	if patchCalls != 1 {
		t.Fatalf("repair patch calls = %d, want 1", patchCalls)
	}

	state.StatusCommentID = 0
	if err := repairIssueStatus(context.Background(), nil, "issue-agent[bot]", state); err == nil || !strings.Contains(err.Error(), "lacks a status comment") {
		t.Fatalf("repairIssueStatus() missing identity error = %v", err)
	}
}

func TestIssueAgentTrackingMutatesOnlyItsOwnedLabelAndVerifiesProjection(t *testing.T) {
	t.Parallel()
	initial := issuegithub.IssueFacts{Number: 42, Labels: []string{"bug"}}
	mutations := 0
	client := newIssueAgentMemoryClient(t, func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodPost:
			mutations++
			if request.URL.Path != "/repos/WuKongIM/WuKongIM/issues/42/labels" {
				t.Fatalf("unexpected label mutation path %s", request.URL.Path)
			}
			return appContractJSONResponse(http.StatusOK, []any{}), nil
		case http.MethodGet:
			return appContractJSONResponse(http.StatusOK, map[string]any{
				"node_id": "I_kw", "number": 42, "state": "open", "title": "Bug",
				"body": "reproduction", "updated_at": "2026-01-01T00:00:00Z",
				"user": map[string]any{"login": "reporter"}, "author_association": "NONE",
				"labels": []map[string]string{{"name": "bug"}, {"name": issueAgentTrackingLabel}},
			}), nil
		default:
			t.Fatalf("unexpected tracking request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	current, changed, err := setIssueAgentTracking(context.Background(), client, initial, true)
	if err != nil || !changed || mutations != 1 || len(current.Labels) != 2 {
		t.Fatalf("setIssueAgentTracking() = labels:%v changed:%v mutations:%d err:%v", current.Labels, changed, mutations, err)
	}
	if same, changed, err := setIssueAgentTracking(context.Background(), client, current, true); err != nil || changed || same.Number != current.Number || mutations != 1 {
		t.Fatalf("idempotent tracking = %#v changed:%v mutations:%d err:%v", same, changed, mutations, err)
	}

	inconsistent := newIssueAgentMemoryClient(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost {
			return appContractJSONResponse(http.StatusOK, []any{}), nil
		}
		return appContractJSONResponse(http.StatusOK, map[string]any{
			"node_id": "I_kw", "number": 42, "state": "open", "title": "Bug", "body": "body",
			"updated_at": "2026-01-01T00:00:00Z", "user": map[string]any{"login": "reporter"},
			"author_association": "NONE", "labels": []map[string]string{{"name": "bug"}},
		}), nil
	})
	if _, _, err := setIssueAgentTracking(context.Background(), inconsistent, initial, true); err == nil || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("inconsistent tracking error = %v", err)
	}
}

func TestIssueAgentReviewContextIdentityCannotBorrowMaintainerPermission(t *testing.T) {
	t.Parallel()
	source := issueAgentReviewContextSource{AppLogin: "review-agent[bot]"}
	permission, err := source.ReadActorPermission(context.Background(), "review-agent[bot]")
	if err != nil || permission != issuegithub.Permission("review_agent") {
		t.Fatalf("ReadActorPermission() = %q, %v", permission, err)
	}
}

func TestResolveIssueNumberRejectsAmbiguousEventHintsWithoutGitHubReads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if number, err := resolveIssueNumber(context.Background(), nil, issuecli.ReconcileGitHubRequest{IssueNumber: 17}); err != nil || number != 17 {
		t.Fatalf("explicit issue number = %d, %v", number, err)
	}
	issueEvent := write("issue", `{"issue":{"number":23}}`)
	if number, err := resolveIssueNumber(context.Background(), nil, issuecli.ReconcileGitHubRequest{EventPath: issueEvent}); err != nil || number != 23 {
		t.Fatalf("issue event number = %d, %v", number, err)
	}
	untrusted := write("untrusted", `{"workflow_run":{"name":"other","conclusion":"success","head_repository":{"full_name":"WuKongIM/WuKongIM"}}}`)
	if number, err := resolveIssueNumber(context.Background(), nil, issuecli.ReconcileGitHubRequest{Repository: "WuKongIM/WuKongIM", EventName: "workflow_run", EventPath: untrusted}); err != nil || number != 0 {
		t.Fatalf("untrusted workflow number = %d, %v", number, err)
	}
	badBranch := write("branch", `{"pull_request":{"number":9,"head":{"ref":"agent/issue-not-a-number"}}}`)
	if number, err := resolveIssueNumber(context.Background(), nil, issuecli.ReconcileGitHubRequest{EventName: "pull_request", EventPath: badBranch}); err != nil || number != 0 {
		t.Fatalf("ambiguous branch number = %d, %v", number, err)
	}
}

func TestReviewAgentFileDigestRejectsMissingAndOversizedControlArtifacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(path, []byte("trusted prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if digest, err := fileDigest(path, 1024); err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("fileDigest() = %q, %v", digest, err)
	}
	if _, err := fileDigest(filepath.Join(dir, "missing"), 1024); err == nil {
		t.Fatal("fileDigest() accepted a missing file")
	}
	if _, err := fileDigest(path, 1); err == nil {
		t.Fatal("fileDigest() accepted an oversized control artifact")
	}
}

type appContractRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn appContractRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func appContractHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func appContractJSONResponse(status int, value any) *http.Response {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return appContractHTTPResponse(status, string(body))
}

func validIssueAgentProjectionState() issuecontract.IssueAgentState {
	return issuecontract.IssueAgentState{
		SchemaVersion: 2, Repository: "WuKongIM/WuKongIM", IssueNumber: 42,
		Sequence: 1, State: issuecontract.IssueStateTriaging,
		IssueSnapshotDigest: "sha256:" + strings.Repeat("a", 64),
		SourceSHA:           strings.Repeat("b", 40),
		UpdatedAt:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func newIssueAgentMemoryClient(t *testing.T, roundTrip appContractRoundTripFunc) *issuegithub.Client {
	t.Helper()
	client, err := issuegithub.NewClient(issuegithub.ClientConfig{
		BaseURL: "https://api.invalid", Repository: "WuKongIM/WuKongIM",
		Token: "token", MaxPages: 2, MaxBodyBytes: 1 << 20,
	}, &http.Client{Transport: roundTrip})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
