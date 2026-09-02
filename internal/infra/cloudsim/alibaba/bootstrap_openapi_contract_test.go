package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ims "github.com/alibabacloud-go/ims-20190815/v4/client"
	ram "github.com/alibabacloud-go/ram-20150501/v2/client"
)

func TestCloudShellBootstrapReadFailsClosedWhenProviderMessageSaysNotFound(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"Code":"InternalError","Message":"bootstrap endpoint not found while resolving routing","RequestId":"test-request"}`))
	})

	api := newTestCloudShellBootstrapAPI(t, handler)
	if _, err := api.ReadBootstrapState(context.Background(), testBootstrapConfig()); err == nil {
		t.Fatal("ReadBootstrapState() accepted an InternalError solely because its message contained not found")
	}
}

func TestCloudShellBootstrapOpenAPILifecycleIsIdempotent(t *testing.T) {
	desired, err := DesiredBootstrapState(testBootstrapConfig())
	if err != nil {
		t.Fatalf("DesiredBootstrapState() error = %v", err)
	}
	state := newBootstrapOpenAPIState(t, desired)
	api := newTestCloudShellBootstrapAPI(t, http.HandlerFunc(state.serveHTTP))

	if err := api.ApplyBootstrapState(context.Background(), desired); err != nil {
		t.Fatalf("first ApplyBootstrapState() error = %v", err)
	}
	if got := state.writeActions(); !reflect.DeepEqual(got, []string{
		"CreateOIDCProvider", "CreateRole", "CreateRole",
		"CreatePolicy", "AttachPolicyToRole", "CreatePolicy", "AttachPolicyToRole",
	}) {
		t.Fatalf("first apply writes = %v, want one create/attach per desired resource", got)
	}

	beforeSecondApply := state.actionCount()
	if err := api.ApplyBootstrapState(context.Background(), desired); err != nil {
		t.Fatalf("second ApplyBootstrapState() error = %v", err)
	}
	if writes := state.writeActionsSince(beforeSecondApply); len(writes) != 0 {
		t.Fatalf("idempotent apply writes = %v, want read-only convergence", writes)
	}

	current, err := api.ReadBootstrapState(context.Background(), testBootstrapConfig())
	if err != nil {
		t.Fatalf("ReadBootstrapState() error = %v", err)
	}
	if !reflect.DeepEqual(current, desired) {
		t.Fatalf("ReadBootstrapState() = %#v, want %#v", current, desired)
	}

	if err := api.RemoveBootstrapState(context.Background(), desired); err != nil {
		t.Fatalf("first RemoveBootstrapState() error = %v", err)
	}
	if provider, roles, policies, attachments := state.resourceCounts(); provider || roles != 0 || policies != 0 || attachments != 0 {
		t.Fatalf("state after remove = provider %v, roles %d, policies %d, attachments %d", provider, roles, policies, attachments)
	}
	if err := api.RemoveBootstrapState(context.Background(), desired); err != nil {
		t.Fatalf("idempotent RemoveBootstrapState() error = %v", err)
	}
}

func TestCloudShellBootstrapRemoveAggregatesProviderFailures(t *testing.T) {
	desired, err := DesiredBootstrapState(testBootstrapConfig())
	if err != nil {
		t.Fatalf("DesiredBootstrapState() error = %v", err)
	}
	var (
		mu      sync.Mutex
		actions []string
	)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action := request.Header.Get("x-acs-action")
		mu.Lock()
		actions = append(actions, action)
		mu.Unlock()
		writeBootstrapOpenAPIError(t, writer, "InternalError", action+" failed")
	})

	api := newTestCloudShellBootstrapAPI(t, handler)
	err = api.RemoveBootstrapState(context.Background(), desired)
	if err == nil {
		t.Fatal("RemoveBootstrapState() error = nil, want all provider failures")
	}
	for _, context := range []string{
		"detach policy " + desired.ProvisionerPolicy.Name,
		"delete policy " + desired.ProvisionerPolicy.Name,
		"detach policy " + desired.AnalyzerPolicy.Name,
		"delete policy " + desired.AnalyzerPolicy.Name,
		"delete role " + desired.ProvisionerRole.Name,
		"delete role " + desired.AnalyzerRole.Name,
		"delete OIDC provider",
	} {
		if !strings.Contains(err.Error(), context) {
			t.Fatalf("RemoveBootstrapState() error %q does not contain %q", err, context)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(actions) != 7 {
		t.Fatalf("remove actions = %v, want all seven cleanup attempts", actions)
	}
}

func TestCloudShellBootstrapApplyConvergesMutableProviderState(t *testing.T) {
	desired, err := DesiredBootstrapState(testBootstrapConfig())
	if err != nil {
		t.Fatalf("DesiredBootstrapState() error = %v", err)
	}
	var (
		mu        sync.Mutex
		writes    []string
		oldTrust  = `{"Version":"1","Statement":[]}`
		oldPolicy = `{"Version":"1","Statement":[]}`
	)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		action := request.Header.Get("x-acs-action")
		switch action {
		case "GetOIDCProvider":
			writeBootstrapOpenAPIValue(t, writer, map[string]any{
				"RequestId": "test-request", "OIDCProvider": map[string]any{
					"OIDCProviderName": desired.OIDCProvider.Name, "Arn": desired.OIDCProvider.ARN,
					"IssuerUrl": desired.OIDCProvider.IssuerURL, "ClientIds": "old-audience", "Fingerprints": "old-fingerprint",
				},
			})
		case "GetRole":
			name := request.Form.Get("RoleName")
			writeBootstrapOpenAPIValue(t, writer, map[string]any{
				"RequestId": "test-request", "Role": map[string]any{
					"RoleName": name, "Arn": "acs:ram::1234567890123456:role/" + name,
					"AssumeRolePolicyDocument": oldTrust,
				},
			})
		case "GetPolicy":
			name := request.Form.Get("PolicyName")
			writeBootstrapOpenAPIValue(t, writer, map[string]any{
				"RequestId": "test-request", "Policy": map[string]any{"PolicyName": name},
				"DefaultPolicyVersion": map[string]any{"PolicyDocument": oldPolicy},
			})
		case "ListPoliciesForRole":
			role := request.Form.Get("RoleName")
			policy := desired.ProvisionerPolicy
			if role == desired.AnalyzerRole.Name {
				policy = desired.AnalyzerPolicy
			}
			writeBootstrapOpenAPIValue(t, writer, map[string]any{
				"RequestId": "test-request", "Policies": map[string]any{"Policy": []map[string]any{{
					"PolicyName": policy.Name, "PolicyType": "Custom",
				}}},
			})
		case "UpdateOIDCProvider", "AddFingerprintToOIDCProvider", "RemoveFingerprintFromOIDCProvider", "UpdateRole", "CreatePolicyVersion":
			mu.Lock()
			writes = append(writes, action)
			mu.Unlock()
			writeBootstrapOpenAPIJSON(t, writer, `{"RequestId":"test-request"}`)
		default:
			t.Fatalf("unexpected bootstrap convergence action %q", action)
		}
	})

	api := newTestCloudShellBootstrapAPI(t, handler)
	if err := api.ApplyBootstrapState(context.Background(), desired); err != nil {
		t.Fatalf("ApplyBootstrapState() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(writes, []string{
		"UpdateOIDCProvider", "AddFingerprintToOIDCProvider", "RemoveFingerprintFromOIDCProvider",
		"UpdateRole", "UpdateRole", "CreatePolicyVersion", "CreatePolicyVersion",
	}) {
		t.Fatalf("bootstrap convergence writes = %v", writes)
	}
}

func TestCloudShellBootstrapApplyRefusesOIDCIssuerReplacement(t *testing.T) {
	desired, err := DesiredBootstrapState(testBootstrapConfig())
	if err != nil {
		t.Fatalf("DesiredBootstrapState() error = %v", err)
	}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if action := request.Header.Get("x-acs-action"); action != "GetOIDCProvider" {
			t.Fatalf("unexpected action after issuer conflict %q", action)
		}
		writeBootstrapOpenAPIValue(t, writer, map[string]any{
			"RequestId": "test-request", "OIDCProvider": map[string]any{
				"OIDCProviderName": desired.OIDCProvider.Name, "Arn": desired.OIDCProvider.ARN,
				"IssuerUrl": "https://unexpected.example.com", "ClientIds": strings.Join(desired.OIDCProvider.Audiences, ","),
				"Fingerprints": strings.Join(desired.OIDCProvider.Fingerprints, ","),
			},
		})
	})

	api := newTestCloudShellBootstrapAPI(t, handler)
	if err := api.ApplyBootstrapState(context.Background(), desired); !errors.Is(err, ErrBootstrapConfig) {
		t.Fatalf("ApplyBootstrapState() error = %v, want protected issuer conflict", err)
	}
}

func newTestCloudShellBootstrapAPI(t *testing.T, handler http.Handler) *CloudShellBootstrapAPI {
	t.Helper()
	config := (&openapiutil.Config{}).
		SetAccessKeyId("test-access-key").
		SetAccessKeySecret("test-access-secret").
		SetProtocol("http").
		SetEndpoint("openapi.test").
		SetHttpClient(openAPITestHTTPClient{handler: handler})
	imsClient, err := ims.NewClient(config)
	if err != nil {
		t.Fatalf("ims.NewClient() error = %v", err)
	}
	ramClient, err := ram.NewClient(config)
	if err != nil {
		t.Fatalf("ram.NewClient() error = %v", err)
	}
	return &CloudShellBootstrapAPI{ims: imsClient, ram: ramClient}
}

func writeBootstrapOpenAPIJSON(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("write bootstrap response: %v", err)
	}
}

func writeBootstrapOpenAPIError(t *testing.T, writer http.ResponseWriter, code, message string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusBadRequest)
	writeBootstrapOpenAPIJSON(t, writer, `{"Code":"`+code+`","Message":"`+message+`","RequestId":"test-request"}`)
}

type bootstrapOpenAPIState struct {
	t           *testing.T
	desired     BootstrapState
	mu          sync.Mutex
	provider    bool
	roles       map[string]bool
	policies    map[string]bool
	attachments map[string]string
	actions     []string
}

func newBootstrapOpenAPIState(t *testing.T, desired BootstrapState) *bootstrapOpenAPIState {
	t.Helper()
	return &bootstrapOpenAPIState{
		t: t, desired: desired, roles: make(map[string]bool), policies: make(map[string]bool),
		attachments: make(map[string]string),
	}
}

func (s *bootstrapOpenAPIState) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	s.t.Helper()
	if err := request.ParseForm(); err != nil {
		s.t.Fatalf("ParseForm() error = %v", err)
	}
	action := request.Header.Get("x-acs-action")
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actions = append(s.actions, action)

	switch action {
	case "GetOIDCProvider":
		if !s.provider {
			writeBootstrapOpenAPIError(s.t, writer, "EntityNotExist.OIDCProvider", "OIDC provider does not exist")
			return
		}
		writeBootstrapOpenAPIValue(s.t, writer, map[string]any{
			"RequestId": "test-request",
			"OIDCProvider": map[string]any{
				"OIDCProviderName": s.desired.OIDCProvider.Name,
				"Arn":              s.desired.OIDCProvider.ARN,
				"IssuerUrl":        s.desired.OIDCProvider.IssuerURL,
				"ClientIds":        strings.Join(s.desired.OIDCProvider.Audiences, ","),
				"Fingerprints":     strings.Join(s.desired.OIDCProvider.Fingerprints, ","),
			},
		})
	case "CreateOIDCProvider":
		s.provider = true
		writeBootstrapOpenAPIJSON(s.t, writer, `{"RequestId":"test-request"}`)
	case "DeleteOIDCProvider":
		if !s.provider {
			writeBootstrapOpenAPIError(s.t, writer, "EntityNotExist.OIDCProvider", "OIDC provider does not exist")
			return
		}
		s.provider = false
		writeBootstrapOpenAPIJSON(s.t, writer, `{"RequestId":"test-request"}`)
	case "GetRole":
		role, ok := s.role(request.Form.Get("RoleName"))
		if !ok || !s.roles[role.Name] {
			writeBootstrapOpenAPIError(s.t, writer, "EntityNotExist.Role", "role does not exist")
			return
		}
		writeBootstrapOpenAPIValue(s.t, writer, map[string]any{
			"RequestId": "test-request",
			"Role": map[string]any{
				"RoleName": role.Name, "Arn": role.ARN, "AssumeRolePolicyDocument": role.TrustPolicy,
			},
		})
	case "CreateRole", "UpdateRole":
		name := request.Form.Get("RoleName")
		s.roles[name] = true
		writeBootstrapOpenAPIJSON(s.t, writer, `{"RequestId":"test-request"}`)
	case "DeleteRole":
		name := request.Form.Get("RoleName")
		if !s.roles[name] {
			writeBootstrapOpenAPIError(s.t, writer, "EntityNotExist.Role", "role does not exist")
			return
		}
		delete(s.roles, name)
		writeBootstrapOpenAPIJSON(s.t, writer, `{"RequestId":"test-request"}`)
	case "GetPolicy":
		policy, ok := s.policy(request.Form.Get("PolicyName"))
		if !ok || !s.policies[policy.Name] {
			writeBootstrapOpenAPIError(s.t, writer, "EntityNotExist.Policy", "policy does not exist")
			return
		}
		writeBootstrapOpenAPIValue(s.t, writer, map[string]any{
			"RequestId":            "test-request",
			"Policy":               map[string]any{"PolicyName": policy.Name},
			"DefaultPolicyVersion": map[string]any{"PolicyDocument": policy.Document},
		})
	case "ListPoliciesForRole":
		role := request.Form.Get("RoleName")
		items := make([]map[string]any, 0, 1)
		for policy, attachedRole := range s.attachments {
			if attachedRole == role {
				items = append(items, map[string]any{"PolicyName": policy, "PolicyType": "Custom"})
			}
		}
		writeBootstrapOpenAPIValue(s.t, writer, map[string]any{
			"RequestId": "test-request", "Policies": map[string]any{"Policy": items},
		})
	case "CreatePolicy", "CreatePolicyVersion":
		name := request.Form.Get("PolicyName")
		s.policies[name] = true
		writeBootstrapOpenAPIJSON(s.t, writer, `{"RequestId":"test-request"}`)
	case "AttachPolicyToRole":
		s.attachments[request.Form.Get("PolicyName")] = request.Form.Get("RoleName")
		writeBootstrapOpenAPIJSON(s.t, writer, `{"RequestId":"test-request"}`)
	case "DetachPolicyFromRole":
		name := request.Form.Get("PolicyName")
		if _, ok := s.attachments[name]; !ok {
			writeBootstrapOpenAPIError(s.t, writer, "EntityNotExist.Attachment", "attachment does not exist")
			return
		}
		delete(s.attachments, name)
		writeBootstrapOpenAPIJSON(s.t, writer, `{"RequestId":"test-request"}`)
	case "DeletePolicy":
		name := request.Form.Get("PolicyName")
		if !s.policies[name] {
			writeBootstrapOpenAPIError(s.t, writer, "EntityNotExist.Policy", "policy does not exist")
			return
		}
		delete(s.policies, name)
		writeBootstrapOpenAPIJSON(s.t, writer, `{"RequestId":"test-request"}`)
	default:
		s.t.Fatalf("unexpected bootstrap action %q", action)
	}
}

func (s *bootstrapOpenAPIState) role(name string) (RoleSpec, bool) {
	for _, role := range []RoleSpec{s.desired.ProvisionerRole, s.desired.AnalyzerRole} {
		if role.Name == name {
			return role, true
		}
	}
	return RoleSpec{}, false
}

func (s *bootstrapOpenAPIState) policy(name string) (PolicySpec, bool) {
	for _, policy := range []PolicySpec{s.desired.ProvisionerPolicy, s.desired.AnalyzerPolicy} {
		if policy.Name == name {
			return policy, true
		}
	}
	return PolicySpec{}, false
}

func (s *bootstrapOpenAPIState) actionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.actions)
}

func (s *bootstrapOpenAPIState) writeActions() []string {
	return s.writeActionsSince(0)
}

func (s *bootstrapOpenAPIState) writeActionsSince(start int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, 0)
	for _, action := range s.actions[start:] {
		if strings.HasPrefix(action, "Create") || strings.HasPrefix(action, "Update") ||
			strings.HasPrefix(action, "Attach") || strings.HasPrefix(action, "Detach") ||
			strings.HasPrefix(action, "Delete") {
			result = append(result, action)
		}
	}
	return result
}

func (s *bootstrapOpenAPIState) resourceCounts() (bool, int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.provider, len(s.roles), len(s.policies), len(s.attachments)
}

func writeBootstrapOpenAPIValue(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Fatalf("encode bootstrap response: %v", err)
	}
}
