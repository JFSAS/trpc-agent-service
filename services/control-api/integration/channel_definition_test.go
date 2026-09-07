package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	owner "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	definition "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

func testChannelDefinitionHTTP(t *testing.T, ctx context.Context, router http.Handler, pool *pgxpool.Pool, tenant string, admin, member *http.Cookie) {
	t.Helper()
	base := "/v1/tenants/" + tenant + "/channel-policy-definitions/"
	input := `{"expected_revision":0,"definition":{"enabled":true,"session":{"partition":"per_user_in_conversation"}}}`
	path := base + "session/session-http/revisions"
	unauth := deploymentRequest(t, router, "POST", path, nil, "def-unauth", input, 401, nil)
	if unauth.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authentication cache")
	}
	deploymentRequest(t, router, "POST", path, member, "def-member", "{", 403, nil)
	var first owner.PublishResult
	a := deploymentRequest(t, router, "POST", path, admin, "def-first", input, 201, &first)
	b := deploymentRequest(t, router, "POST", path, admin, "def-first", input, 201, nil)
	if a.Body.String() != b.Body.String() || first.Revision != 1 || first.Distribution != "PENDING" || first.Digest == "" {
		t.Fatal("definition publication/replay", a.Body)
	}
	var exact definition.Revision
	read := request(t, router, "GET", path+"/1", admin, "", 200, &exact)
	if exact.Digest != first.Digest || exact.PolicyID != "session-http" || exact.Revision != 1 || read.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("exact published definition", exact)
	}
	request(t, router, "GET", path+"/2", admin, "", 404, nil)
	request(t, router, "GET", path+"/1", member, "", 403, nil)
	request(t, router, "GET", path+"/1", nil, "", 401, nil)
	for _, suffix := range []string{"/01", "/+1", "/latest", "/0", "/9007199254740992", "/1?latest=true"} {
		request(t, router, "GET", path+suffix, admin, "", 400, nil)
	}
	request(t, router, "GET", base+"quota/session-http/revisions/1", admin, "", 404, nil)
	deploymentRequest(t, router, "POST", path, admin, "def-stale", input, 409, nil)
	deploymentRequest(t, router, "POST", path, admin, "def-first", `{"expected_revision":1,"definition":{"enabled":false,"session":{"partition":"shared_conversation"}}}`, 409, nil)
	for i, body := range []string{
		`{"expected_revision":0,"definition":{"session":{"partition":"per_user_in_conversation"}}}`,
		`{"expected_revision":0,"expected_revision":1,"definition":{"enabled":true,"session":{"partition":"per_user_in_conversation"}}}`,
		`{"expected_revision":0,"definition":{"enabled":true,"quota":{"public_limited":true,"max_concurrent_runs":1,"max_runs_per_minute":5}}}`,
		`{"expected_revision":0,"definition":{"enabled":true,"session":{"partition":"per_user_in_conversation"}},"tenant_id":"tnt_other"}`,
	} {
		deploymentRequest(t, router, "POST", path, admin, fmt.Sprintf("def-invalid-%d", i), body, 400, nil)
	}
	quota := `{"expected_revision":0,"definition":{"enabled":true,"quota":{"public_limited":true,"max_concurrent_runs":1,"max_runs_per_minute":5}}}`
	var q owner.PublishResult
	deploymentRequest(t, router, "POST", base+"quota/quota-http/revisions", admin, "def-quota", quota, 201, &q)
	if q.Kind != "quota" || q.Distribution != "PENDING" {
		t.Fatal(q)
	}
	var second owner.PublishResult
	deploymentRequest(t, router, "POST", path, admin, "def-second", `{"expected_revision":1,"definition":{"enabled":false,"session":{"partition":"shared_conversation"}}}`, 201, &second)
	request(t, router, "GET", path+"/1", admin, "", 200, &exact)
	if exact.Digest != first.Digest || !exact.Definition.Enabled {
		t.Fatal("historical definition changed")
	}
	request(t, router, "GET", path+"/2", admin, "", 200, &exact)
	if exact.Digest != second.Digest || exact.Definition.Enabled || exact.Revision != 2 {
		t.Fatal("exact second revision", exact)
	}
	request(t, router, "GET", "/v1/tenants/tnt_unowned/channel-policy-definitions/session/session-http/revisions/1", admin, "", 403, nil)
	request(t, router, "GET", path+"/1", admin, "{}", 400, nil)
	var docs, receipts, events int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM channel_policy_definition_revisions WHERE tenant_id=$1),(SELECT count(*) FROM channel_policy_definition_receipts WHERE tenant_id=$1),(SELECT count(*) FROM control_outbox WHERE tenant_id=$1 AND aggregate_type='ChannelPolicyDefinition')`, tenant).Scan(&docs, &receipts, &events); err != nil || docs != 3 || receipts != 3 || events != 6 {
		t.Fatal("definition atomic writes", docs, receipts, events, err)
	}
}
