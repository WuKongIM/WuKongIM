package alibaba

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	openapiutil "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	ims "github.com/alibabacloud-go/ims-20190815/v4/client"
	ram "github.com/alibabacloud-go/ram-20150501/v2/client"
	sts "github.com/alibabacloud-go/sts-20150401/v2/client"
)

type identityOpenAPICall struct {
	action string
	form   url.Values
}

func TestIdentityBootstrapOpenAPIReadsCompleteProviderState(t *testing.T) {
	desired := identityOpenAPITestState()
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, _ := identityOpenAPIRequest(t, request)
		writer.Header().Set("Content-Type", "application/json")
		var response any
		switch action {
		case "GetCallerIdentity":
			response = map[string]any{
				"RequestId": "request-identity", "AccountId": "1234567890123456",
				"Arn": "acs:ram::1234567890123456:root", "IdentityType": "Account",
			}
		case "GetOIDCProvider":
			response = map[string]any{"RequestId": "request-oidc", "OIDCProvider": map[string]any{
				"OIDCProviderName": desired.OIDCProvider.Name,
				"Arn":              "acs:ram::1234567890123456:oidc-provider/" + desired.OIDCProvider.Name,
				"IssuerUrl":        desired.OIDCProvider.IssuerURL,
				"ClientIds":        " audience-b, audience-a ",
				"Fingerprints":     " fingerprint-b, fingerprint-a ",
			}}
		case "GetRole":
			response = map[string]any{"RequestId": "request-role", "Role": map[string]any{
				"RoleName": desired.Roles[0].Name,
				"Arn":      "acs:ram::1234567890123456:role/" + desired.Roles[0].Name,
				"AssumeRolePolicyDocument": `{
					"Statement":[{"Effect":"Allow","Action":"sts:AssumeRole"}],"Version":"1"
				}`,
				"MaxSessionDuration": 3600,
			}}
		case "GetPolicy":
			response = map[string]any{
				"RequestId": "request-policy",
				"Policy":    map[string]any{"PolicyName": desired.Policies[0].Name, "PolicyType": "Custom"},
				"DefaultPolicyVersion": map[string]any{
					"VersionId": "v1", "IsDefaultVersion": true,
					"PolicyDocument": `{ "Statement": [{"Resource":"*","Effect":"Allow","Action":["ecs:DescribeInstances"]}], "Version":"1" }`,
				},
			}
		case "ListPoliciesForRole":
			response = map[string]any{"RequestId": "request-attachments", "Policies": map[string]any{"Policy": []any{
				map[string]any{"PolicyName": desired.Policies[0].Name, "PolicyType": "Custom"},
			}}}
		default:
			t.Errorf("unexpected identity read action %q", action)
			response = map[string]any{"Code": "UnexpectedAction", "Message": action, "RequestId": "unexpected"}
		}
		_ = json.NewEncoder(writer).Encode(response)
	})
	api := newIdentityBootstrapOpenAPITestClient(t, handler)

	accountID, err := api.CallerAccountID(context.Background())
	if err != nil || accountID != "1234567890123456" {
		t.Fatalf("CallerAccountID() = %q, %v", accountID, err)
	}
	got, err := api.ReadIdentityBootstrapState(context.Background(), desired)
	if err != nil {
		t.Fatalf("ReadIdentityBootstrapState() error = %v", err)
	}
	if got.OIDCProvider.Name != desired.OIDCProvider.Name || got.OIDCProvider.ARN == "" ||
		!reflect.DeepEqual(got.OIDCProvider.Audiences, []string{"audience-a", "audience-b"}) ||
		!reflect.DeepEqual(got.OIDCProvider.Fingerprints, []string{"fingerprint-a", "fingerprint-b"}) {
		t.Fatalf("OIDC state = %#v", got.OIDCProvider)
	}
	if len(got.Roles) != 1 || got.Roles[0].Name != desired.Roles[0].Name || got.Roles[0].MaxSessionDuration != 3600 || got.Roles[0].TrustPolicy == "" {
		t.Fatalf("role state = %#v", got.Roles)
	}
	if len(got.Policies) != 1 || got.Policies[0].Name != desired.Policies[0].Name ||
		got.Policies[0].AttachedRole != desired.Policies[0].AttachedRole || got.Policies[0].Document == "" {
		t.Fatalf("policy state = %#v", got.Policies)
	}
}

func TestIdentityBootstrapOpenAPIConvergesMissingResourcesAndAttachments(t *testing.T) {
	desired := identityOpenAPITestState()
	desired.OIDCProvider.Fingerprints = []string{"fingerprint-keep", "fingerprint-new"}
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch action {
		case "GetOIDCProvider":
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-oidc", "OIDCProvider": map[string]any{
				"OIDCProviderName": desired.OIDCProvider.Name,
				"IssuerUrl":        desired.OIDCProvider.IssuerURL,
				"ClientIds":        "old-audience",
				"Fingerprints":     "fingerprint-keep,fingerprint-stale",
			}})
		case "GetRole":
			writeIdentityOpenAPIError(writer, http.StatusNotFound, "EntityNotExist.Role", "role absent")
		case "GetPolicy":
			writeIdentityOpenAPIError(writer, http.StatusNotFound, "EntityNotExist.Policy", "policy absent")
		case "ListPoliciesForRole":
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-attachments", "Policies": map[string]any{"Policy": []any{
				map[string]any{"PolicyName": "unexpected-policy", "PolicyType": "Custom"},
			}}})
		default:
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-mutation"})
		}
	})
	api := newIdentityBootstrapOpenAPITestClient(t, handler)

	if err := api.ApplyIdentityBootstrapState(context.Background(), desired); err != nil {
		t.Fatalf("ApplyIdentityBootstrapState() error = %v", err)
	}
	actions := identityOpenAPIActions(calls)
	for _, want := range []string{
		"GetOIDCProvider", "UpdateOIDCProvider", "AddFingerprintToOIDCProvider", "RemoveFingerprintFromOIDCProvider",
		"GetRole", "CreateRole", "GetPolicy", "CreatePolicy", "ListPoliciesForRole", "DetachPolicyFromRole", "AttachPolicyToRole",
	} {
		if actions[want] != 1 {
			t.Fatalf("action %s calls = %d, all actions %#v", want, actions[want], actions)
		}
	}
	if form := identityOpenAPIForm(calls, "DetachPolicyFromRole"); form.Get("PolicyName") != "unexpected-policy" || form.Get("RoleName") != desired.Roles[0].Name {
		t.Fatalf("detach form = %#v", form)
	}
	if form := identityOpenAPIForm(calls, "AttachPolicyToRole"); form.Get("PolicyName") != desired.Policies[0].Name || form.Get("PolicyType") != "Custom" {
		t.Fatalf("attach form = %#v", form)
	}
}

func TestIdentityBootstrapOpenAPIRepairsRoleAndPolicyDriftWithoutRecreating(t *testing.T) {
	desired := identityOpenAPITestState()
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch action {
		case "GetOIDCProvider":
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-oidc", "OIDCProvider": map[string]any{
				"OIDCProviderName": desired.OIDCProvider.Name, "IssuerUrl": desired.OIDCProvider.IssuerURL,
				"ClientIds": strings.Join(desired.OIDCProvider.Audiences, ","), "Fingerprints": strings.Join(desired.OIDCProvider.Fingerprints, ","),
			}})
		case "GetRole":
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-role", "Role": map[string]any{
				"RoleName": desired.Roles[0].Name, "AssumeRolePolicyDocument": `{"Version":"1","Statement":[]}`, "MaxSessionDuration": 1800,
			}})
		case "GetPolicy":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"RequestId": "request-policy", "Policy": map[string]any{"PolicyName": desired.Policies[0].Name},
				"DefaultPolicyVersion": map[string]any{"PolicyDocument": `{"Version":"1","Statement":[]}`},
			})
		case "ListPoliciesForRole":
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-list", "Policies": map[string]any{"Policy": []any{
				map[string]any{"PolicyName": desired.Policies[0].Name, "PolicyType": "Custom"},
			}}})
		default:
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-update"})
		}
	})
	api := newIdentityBootstrapOpenAPITestClient(t, handler)

	if err := api.ApplyIdentityBootstrapState(context.Background(), desired); err != nil {
		t.Fatalf("ApplyIdentityBootstrapState() error = %v", err)
	}
	actions := identityOpenAPIActions(calls)
	if actions["UpdateRole"] != 1 || actions["CreatePolicyVersion"] != 1 {
		t.Fatalf("drift repair actions = %#v", actions)
	}
	for _, forbidden := range []string{"CreateRole", "CreatePolicy", "AttachPolicyToRole", "DetachPolicyFromRole", "UpdateOIDCProvider"} {
		if actions[forbidden] != 0 {
			t.Fatalf("idempotent repair called %s: %#v", forbidden, actions)
		}
	}
}

func TestIdentityBootstrapOpenAPIRemovalContinuesAfterErrorsAndIgnoresAbsentResources(t *testing.T) {
	desired := identityOpenAPITestState()
	desired.Policies = append(desired.Policies, IdentityPolicySpec{Name: "policy-2", AttachedRole: "role-2"})
	desired.Roles = append(desired.Roles, IdentityRoleSpec{Name: "role-2"})
	var mutex sync.Mutex
	var calls []identityOpenAPICall
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		action, form := identityOpenAPIRequest(t, request)
		mutex.Lock()
		calls = append(calls, identityOpenAPICall{action: action, form: form})
		mutex.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case action == "DetachPolicyFromRole" && form.Get("PolicyName") == "policy-2":
			writeIdentityOpenAPIError(writer, http.StatusNotFound, "EntityNotExist.Policy", "already detached")
		case action == "DeleteRole" && form.Get("RoleName") == "role-2":
			writeIdentityOpenAPIError(writer, http.StatusInternalServerError, "InternalError", "provider failed")
		default:
			_ = json.NewEncoder(writer).Encode(map[string]any{"RequestId": "request-remove"})
		}
	})
	api := newIdentityBootstrapOpenAPITestClient(t, handler)

	err := api.RemoveIdentityBootstrapState(context.Background(), desired)
	if err == nil || !strings.Contains(err.Error(), "delete identity role role-2") {
		t.Fatalf("RemoveIdentityBootstrapState() error = %v", err)
	}
	actions := identityOpenAPIActions(calls)
	if actions["DetachPolicyFromRole"] != 2 || actions["DeletePolicy"] != 2 || actions["DeleteRole"] != 2 || actions["DeleteOIDCProvider"] != 1 {
		t.Fatalf("best-effort removal actions = %#v", actions)
	}
}

func identityOpenAPITestState() IdentityBootstrapState {
	return IdentityBootstrapState{
		OIDCProvider: IdentityOIDCProviderSpec{
			Name: "wukongim-cloud-lease-github", IssuerURL: "https://token.actions.githubusercontent.com",
			Audiences: []string{"audience-a", "audience-b"}, Fingerprints: []string{"fingerprint-a", "fingerprint-b"},
		},
		Roles: []IdentityRoleSpec{{
			Name: "CloudLeaseProvisioner", TrustPolicy: `{"Version":"1","Statement":[{"Action":"sts:AssumeRole","Effect":"Allow"}]}`,
			MaxSessionDuration: 3600,
		}},
		Policies: []IdentityPolicySpec{{
			Name: "cloud-lease-provision", AttachedRole: "CloudLeaseProvisioner",
			Document: `{"Version":"1","Statement":[{"Action":["ecs:DescribeInstances"],"Effect":"Allow","Resource":"*"}]}`,
		}},
	}
}

func identityOpenAPIRequest(t *testing.T, request *http.Request) (string, url.Values) {
	t.Helper()
	if err := request.ParseForm(); err != nil {
		t.Errorf("ParseForm() error = %v", err)
		return "", nil
	}
	action := request.Header.Get("x-acs-action")
	if action == "" {
		action = request.Form.Get("Action")
	}
	form := make(url.Values, len(request.Form))
	for key, values := range request.Form {
		form[key] = append([]string(nil), values...)
	}
	return action, form
}

func writeIdentityOpenAPIError(writer http.ResponseWriter, status int, code, message string) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"Code": code, "Message": message, "RequestId": "request-error"})
}

func identityOpenAPIActions(calls []identityOpenAPICall) map[string]int {
	actions := make(map[string]int)
	for _, call := range calls {
		actions[call.action]++
	}
	return actions
}

func identityOpenAPIForm(calls []identityOpenAPICall, action string) url.Values {
	for _, call := range calls {
		if call.action == action {
			return call.form
		}
	}
	return nil
}

func newIdentityBootstrapOpenAPITestClient(t *testing.T, handler http.Handler) *IdentityBootstrapOpenAPI {
	t.Helper()
	newConfig := func() *openapiutil.Config {
		return (&openapiutil.Config{}).
			SetAccessKeyId("test-access-key").
			SetAccessKeySecret("test-access-secret").
			SetProtocol("http").
			SetEndpoint("openapi.test").
			SetHttpClient(openAPITestHTTPClient{handler: handler})
	}
	imsClient, err := ims.NewClient(newConfig())
	if err != nil {
		t.Fatalf("create IMS test client: %v", err)
	}
	ramClient, err := ram.NewClient(newConfig())
	if err != nil {
		t.Fatalf("create RAM test client: %v", err)
	}
	stsClient, err := sts.NewClient(newConfig())
	if err != nil {
		t.Fatalf("create STS test client: %v", err)
	}
	return &IdentityBootstrapOpenAPI{ims: imsClient, ram: ramClient, sts: stsClient}
}

func TestIdentityBootstrapOpenAPIRejectsInvalidReceiverAndCanceledCaller(t *testing.T) {
	var unavailable *IdentityBootstrapOpenAPI
	if _, err := unavailable.CallerAccountID(context.Background()); !errors.Is(err, ErrIdentityBootstrapConfig) {
		t.Fatalf("nil caller identity error = %v", err)
	}
	if _, err := unavailable.ReadIdentityBootstrapState(context.Background(), IdentityBootstrapState{}); !errors.Is(err, ErrIdentityBootstrapConfig) {
		t.Fatalf("nil identity state error = %v", err)
	}
	if _, err := unavailable.ListAssets(context.Background(), InventoryQuery{}); !errors.Is(err, ErrIdentityBootstrapConfig) {
		t.Fatalf("nil inventory error = %v", err)
	}

	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("canceled caller reached provider")
		writer.WriteHeader(http.StatusInternalServerError)
	})
	api := newIdentityBootstrapOpenAPITestClient(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := api.CallerAccountID(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller identity error = %v", err)
	}
}
