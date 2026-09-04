package controlv1_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestControlOpenAPIIsValid(t *testing.T) {
	document := loadControlOpenAPI(t)
	if err := document.Validate(context.Background(), openapi3.AllowExtraSiblingFields("const", "$schema", "$id", "$defs", "propertyNames")); err != nil {
		t.Fatalf("validate OpenAPI: %v", err)
	}
}

func TestControlOpenAPIContainsAgentV1Routes(t *testing.T) {
	document := loadControlOpenAPI(t)
	assertRoutes(t, document, "/v1/tenants/{tenant_id}/agents", []string{
		"GET /v1/tenants/{tenant_id}/agents",
		"GET /v1/tenants/{tenant_id}/agents/{agent_id}",
		"GET /v1/tenants/{tenant_id}/agents/{agent_id}/draft",
		"GET /v1/tenants/{tenant_id}/agents/{agent_id}/versions",
		"GET /v1/tenants/{tenant_id}/agents/{agent_id}/versions/{version_number}",
		"PATCH /v1/tenants/{tenant_id}/agents/{agent_id}",
		"POST /v1/tenants/{tenant_id}/agents",
		"POST /v1/tenants/{tenant_id}/agents/{agent_id}/draft/validate",
		"POST /v1/tenants/{tenant_id}/agents/{agent_id}/versions",
		"PUT /v1/tenants/{tenant_id}/agents/{agent_id}/draft",
	})
}

func TestControlOpenAPIContainsRuntimeProfileV1Routes(t *testing.T) {
	document := loadControlOpenAPI(t)
	assertRoutes(t, document, "/v1/tenants/{tenant_id}/runtime-profiles", []string{
		"GET /v1/tenants/{tenant_id}/runtime-profiles",
		"GET /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}",
		"GET /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/draft",
		"GET /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions",
		"GET /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions/{revision_number}",
		"PATCH /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}",
		"POST /v1/tenants/{tenant_id}/runtime-profiles",
		"POST /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/credentials/update",
		"POST /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/draft/validate",
		"POST /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions",
		"PUT /v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/draft",
	})
}

func TestControlOpenAPIRuntimeProfileSchemasExposeFrozenContract(t *testing.T) {
	document := loadControlOpenAPI(t)
	wantSchemas := []string{
		"RuntimeProfile",
		"RuntimeProfileDraft",
		"RuntimeProfileRevision",
		"RuntimeProfileRevisionSummary",
		"RuntimeProfileConfig",
		"RuntimeProfilePublishedRevision",
		"RuntimeProfileWrite",
		"RuntimeProfileCredentialUpdate",
		"RuntimeProfileValidationDiagnostic",
		"RuntimeProfileValidationReport",
		"RuntimeProfileValidationErrorResponse",
	}
	for _, name := range wantSchemas {
		if document.Components.Schemas[name] == nil {
			t.Errorf("components.schemas.%s is missing", name)
		}
	}

	if document.Components.Schemas["RuntimeProfileSpec"] != nil {
		t.Fatal("internal Canonical Spec must not be a public HTTP schema")
	}

	report := document.Components.Schemas["RuntimeProfileValidationReport"]
	if report == nil || report.Value == nil {
		t.Fatal("RuntimeProfileValidationReport is unresolved")
	}
	schemaVersion := report.Value.Properties["schema_version"]
	if schemaVersion == nil || schemaVersion.Value == nil {
		t.Fatal("RuntimeProfileValidationReport.schema_version is missing")
	}
	if len(schemaVersion.Value.Enum) != 0 {
		t.Fatalf("RuntimeProfileValidationReport.schema_version enum = %#v, want unrestricted string", schemaVersion.Value.Enum)
	}

	summary := document.Components.Schemas["RuntimeProfileRevisionSummary"]
	if summary == nil || summary.Value == nil {
		t.Fatal("RuntimeProfileRevisionSummary is unresolved")
	}
	if _, hasSpec := summary.Value.Properties["spec"]; hasSpec {
		t.Fatal("RuntimeProfileRevisionSummary must not expose spec")
	}
	if containsString(summary.Value.Required, "spec") {
		t.Fatal("RuntimeProfileRevisionSummary must not require spec")
	}

	revision := document.Components.Schemas["RuntimeProfileRevision"]
	if revision == nil || revision.Value == nil {
		t.Fatal("RuntimeProfileRevision is unresolved")
	}
	if revision.Value.Properties["spec"] != nil || revision.Value.Properties["config"] == nil {
		t.Fatal("RuntimeProfileRevision must expose only redacted config, not Canonical spec")
	}
	if !containsString(revision.Value.Required, "config") {
		t.Fatal("RuntimeProfileRevision must require config")
	}

	page := document.Components.Schemas["RuntimeProfileRevisionPage"]
	if page == nil || page.Value == nil || page.Value.Properties["revisions"] == nil ||
		page.Value.Properties["revisions"].Value == nil ||
		page.Value.Properties["revisions"].Value.Items == nil {
		t.Fatal("RuntimeProfileRevisionPage.revisions items are unresolved")
	}
	const wantSummaryRef = "#/components/schemas/RuntimeProfileRevisionSummary"
	if got := page.Value.Properties["revisions"].Value.Items.Ref; got != wantSummaryRef {
		t.Fatalf("RuntimeProfileRevisionPage.revisions items ref = %q, want %q", got, wantSummaryRef)
	}

	publish := document.Paths.Value("/v1/tenants/{tenant_id}/runtime-profiles/{profile_id}/revisions")
	if publish == nil || publish.Post == nil {
		t.Fatal("publish Runtime Profile Revision operation is missing")
	}
	for _, status := range []string{"200", "201", "422"} {
		if publish.Post.Responses.Value(status) == nil {
			t.Errorf("publish Runtime Profile Revision response %s is missing", status)
		}
	}
}

func TestRuntimeProfilePublicationRequestStaysAgentAndEnvironmentIndependent(t *testing.T) {
	document := loadControlOpenAPI(t)
	request := document.Components.Schemas["RuntimeProfileDraftRevisionRequest"]
	if request == nil || request.Value == nil {
		t.Fatal("RuntimeProfileDraftRevisionRequest is unresolved")
	}
	schema := request.Value
	if len(schema.Properties) != 1 || schema.Properties["expected_revision"] == nil ||
		len(schema.Required) != 1 || schema.Required[0] != "expected_revision" ||
		schema.AdditionalProperties.Has == nil || *schema.AdditionalProperties.Has {
		t.Fatal("publication must accept only expected_revision, not Environment, AgentVersion, or resource mappings")
	}
	for _, suffix := range []string{"/draft/validate", "/revisions"} {
		path := document.Paths.Value("/v1/tenants/{tenant_id}/runtime-profiles/{profile_id}" + suffix)
		if path == nil || path.Post == nil || path.Post.RequestBody == nil || path.Post.RequestBody.Value == nil {
			t.Fatalf("publication operation %s is unresolved", suffix)
		}
		body := path.Post.RequestBody.Value.Content["application/json"]
		if body == nil || body.Schema == nil || body.Schema.Ref != "#/components/schemas/RuntimeProfileDraftRevisionRequest" {
			t.Fatalf("publication operation %s must use the static revision request", suffix)
		}
	}
}

func loadControlOpenAPI(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatalf("load OpenAPI: %v", err)
	}
	return document
}

func assertRoutes(t *testing.T, document *openapi3.T, prefix string, want []string) {
	t.Helper()
	var got []string
	for path, item := range document.Paths.Map() {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		for method := range item.Operations() {
			got = append(got, method+" "+path)
		}
	}
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("OpenAPI routes under %s = %#v, want %#v", prefix, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("OpenAPI routes under %s = %#v, want %#v", prefix, got, want)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestRuntimeProfileCredentialOpenAPISeparatesWriteReadAndPublishedViews(t *testing.T) {
	document := loadControlOpenAPI(t)
	for _, route := range []struct{ path, method, schema string }{{"/draft", "PUT", "RuntimeProfileWrite"}, {"/credentials/update", "POST", "RuntimeProfileCredentialUpdate"}} {
		path := document.Paths.Value("/v1/tenants/{tenant_id}/runtime-profiles/{profile_id}" + route.path)
		if path == nil {
			t.Fatalf("missing credential route %s", route.path)
		}
		op := path.GetOperation(route.method)
		if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
			t.Fatal("credential request is missing")
		}
		media := op.RequestBody.Value.Content["application/json"]
		if media == nil || media.Schema == nil || media.Schema.Ref != "#/components/schemas/"+route.schema {
			t.Fatal("credential route uses wrong DTO")
		}
		hasKey := false
		for _, p := range op.Parameters {
			if p.Value != nil && p.Value.In == "header" && p.Value.Name == "Idempotency-Key" && p.Value.Required {
				hasKey = true
			}
		}
		if !hasKey {
			t.Fatal("credential mutation requires Idempotency-Key")
		}
	}
	write := document.Components.Schemas["RuntimeProfileWrite"].Value
	for _, field := range []string{"expected_draft_revision", "credential_protocol_version", "config"} {
		if !containsString(write.Required, field) {
			t.Errorf("write must require %s", field)
		}
	}
	for _, field := range []string{"expected_revision", "spec", "api_key_ref", "credential_id"} {
		if write.Properties[field] != nil {
			t.Errorf("write exposed legacy/internal %s", field)
		}
	}
	published := document.Components.Schemas["RuntimeProfilePublishedRevision"].Value
	if published.Properties["credential_states"] != nil || published.Properties["spec"] != nil || published.Properties["config"] == nil {
		t.Fatal("published retry must be static redacted config")
	}
	state := document.Components.Schemas["RuntimeProfileCredentialState"].Value
	for _, field := range []string{"value", "credential_id", "ciphertext", "secret_ref"} {
		if state.Properties[field] != nil {
			t.Errorf("credential state exposed %s", field)
		}
	}
	seen := map[*openapi3.Schema]bool{}
	var visit func(*openapi3.Schema)
	visit = func(schema *openapi3.Schema) {
		if schema == nil || seen[schema] {
			return
		}
		seen[schema] = true
		for name, p := range schema.Properties {
			if strings.HasSuffix(name, "_ref") || strings.HasSuffix(name, "credential_id") || name == "password" || name == "value" {
				t.Errorf("public config exposes %s", name)
			}
			if p != nil {
				visit(p.Value)
			}
		}
		if schema.AdditionalProperties.Schema != nil {
			visit(schema.AdditionalProperties.Schema.Value)
		}
		for _, p := range schema.OneOf {
			visit(p.Value)
		}
	}
	visit(document.Components.Schemas["RuntimeProfileConfig"].Value)
}

func TestRuntimeProfileDraftCredentialActionsUseOnlyZeroCredentialRevision(t *testing.T) {
	schema := loadControlOpenAPI(t).Components.Schemas["RuntimeProfileCredentialAction"].Value
	for _, action := range []string{"keep", "clear", "replace"} {
		t.Run(action, func(t *testing.T) {
			input := map[string]any{"action": action, "expected_credential_revision": float64(0)}
			if action == "replace" {
				input["value"] = "test-input"
			}
			if err := schema.VisitJSON(input); err != nil {
				t.Fatalf("zero revision rejected: %v", err)
			}
			input["expected_credential_revision"] = float64(1)
			if err := schema.VisitJSON(input); err == nil {
				t.Fatal("Draft credential action accepted live Credential CAS")
			}
		})
	}
}

func TestRuntimeProfilePublishedCredentialTargetMatchesCategoryPurpose(t *testing.T) {
	schema := loadControlOpenAPI(t).Components.Schemas["RuntimeProfilePublishedCredentialTarget"].Value
	for _, test := range []struct {
		category, purpose string
		valid             bool
	}{
		{"models", "api_key", true}, {"tools", "bearer_token", true},
		{"knowledge", "qdrant_api_key", true}, {"knowledge", "embedding_api_key", true},
		{"storage", "dsn", true}, {"models", "dsn", false}, {"tools", "api_key", false},
	} {
		t.Run(test.category+"/"+test.purpose, func(t *testing.T) {
			input := map[string]any{"profile_revision_number": float64(1), "category": test.category, "resource_name": "primary", "purpose_field": test.purpose, "association_token": strings.Repeat("a", 64)}
			err := schema.VisitJSON(input)
			if (err == nil) != test.valid {
				t.Fatalf("category/purpose validity = %t, want %t", err == nil, test.valid)
			}
		})
	}
}
