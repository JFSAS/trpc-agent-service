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
		"RuntimeProfileSpec",
		"RuntimeProfileValidationDiagnostic",
		"RuntimeProfileValidationReport",
		"RuntimeProfileValidationErrorResponse",
	}
	for _, name := range wantSchemas {
		if document.Components.Schemas[name] == nil {
			t.Errorf("components.schemas.%s is missing", name)
		}
	}

	spec := document.Components.Schemas["RuntimeProfileSpec"]
	const wantRef = "../../../schemas/runtimeprofile/v1/runtime-profile-spec.schema.json"
	if spec == nil {
		t.Fatal("RuntimeProfileSpec is missing")
	}
	if spec.Ref != wantRef {
		t.Fatalf("RuntimeProfileSpec ref = %q, want %q", spec.Ref, wantRef)
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
	if revision.Value.Properties["spec"] == nil {
		t.Fatal("RuntimeProfileRevision must expose spec")
	}
	if !containsString(revision.Value.Required, "spec") {
		t.Fatal("RuntimeProfileRevision must require spec")
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
