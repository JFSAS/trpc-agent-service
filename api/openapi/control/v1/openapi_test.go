package controlv1_test

import (
	"context"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestControlOpenAPIIsValidAndContainsAgentV1Routes(t *testing.T) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	document, err := loader.LoadFromFile("openapi.yaml")
	if err != nil {
		t.Fatalf("load OpenAPI: %v", err)
	}
	if err := document.Validate(context.Background(), openapi3.AllowExtraSiblingFields("const", "$schema", "$id", "$defs", "propertyNames")); err != nil {
		t.Fatalf("validate OpenAPI: %v", err)
	}
	var got []string
	for path, item := range document.Paths.Map() {
		if len(path) < len("/v1/tenants/{tenant_id}/agents") || path[:len("/v1/tenants/{tenant_id}/agents")] != "/v1/tenants/{tenant_id}/agents" {
			continue
		}
		for method := range item.Operations() {
			got = append(got, method+" "+path)
		}
	}
	sort.Strings(got)
	want := []string{
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
	}
	if len(got) != len(want) {
		t.Fatalf("Agent OpenAPI routes = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Agent OpenAPI routes = %#v, want %#v", got, want)
		}
	}
}
