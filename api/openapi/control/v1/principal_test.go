package controlv1_test

import "testing"

func TestPrincipalManagementRoutesAreOwnerCommands(t *testing.T) {
	doc := loadControlOpenAPI(t)
	base := "/v1/tenants/{tenant_id}/channel-principals"
	for _, path := range []string{base, base + "/{principal_id}/state"} {
		p := doc.Paths.Value(path)
		if p == nil || p.Post == nil || p.Get != nil {
			t.Fatal("missing command or exposed enumeration", path)
		}
		if !hasRequiredHeader(p.Post, "Idempotency-Key") || p.Post.Security == nil {
			t.Fatal("missing command auth/idempotency", path)
		}
		status := "200"
		if path == base {
			status = "201"
		}
		assertResponseStatuses(t, p.Post, []string{status, "400", "401", "403", "404", "409", "413", "500", "503"})
		schema := p.Post.RequestBody.Value.Content["application/json"].Schema.Value
		if schema.AdditionalProperties.Has == nil || *schema.AdditionalProperties.Has {
			t.Fatal("request not closed")
		}
	}
}
