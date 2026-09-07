package controlv1_test

import (
	"context"
	"github.com/getkin/kin-openapi/openapi3"
	"testing"
)

func TestPolicyResolveRemainsPrivateAndClosed(t *testing.T) {
	path := "/internal/v1/channel-access-policies:resolve"
	if loadControlOpenAPI(t).Paths.Value(path) != nil {
		t.Fatal("policy document on public session router")
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile("policy-internal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Components.SecuritySchemes["workloadMTLS"].Value.Type != "mutualTLS" {
		t.Fatal("missing workload mTLS")
	}
	if err = doc.Paths.Validate(context.Background(), openapi3.AllowExtraSiblingFields("const", "$schema", "$id", "$defs", "if", "then", "else")); err != nil {
		t.Fatal(err)
	}
	op := doc.Paths.Value(path).Post
	if op == nil || op.Security == nil || !op.RequestBody.Value.Required {
		t.Fatal("missing operation guards")
	}
}

func TestAuthorizationSnapshotPathsRemainPrivate(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile("policy-internal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/internal/v1/channel-authorizations:snapshot", "/internal/v1/channel-authorizations:page"} {
		if loadControlOpenAPI(t).Paths.Value(path) != nil {
			t.Fatal("snapshot leaked to public contract")
		}
		op := doc.Paths.Value(path).Post
		if op == nil || op.Security == nil || !op.RequestBody.Value.Required || op.Responses.Value("409") == nil {
			t.Fatal("incomplete snapshot contract")
		}
	}
}
