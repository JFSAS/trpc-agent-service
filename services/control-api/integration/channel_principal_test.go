package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	channelapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
)

// This exercises real module registration, Session middleware, OWNER checks,
// schemas, application commands, PostgreSQL locks and MAC idempotency receipts.
func testChannelPrincipalHTTP(t *testing.T, ctx context.Context, router http.Handler, pool *pgxpool.Pool, tenant, account string, owner, member *http.Cookie) {
	t.Helper()
	base := "/v1/tenants/" + tenant + "/channel-principals"
	body := fmt.Sprintf(`{"account_id":%q,"external_user_id":"000456"}`, account)
	unauth := deploymentRequest(t, router, "POST", base, nil, "principal-unauth", body, 401, nil)
	if unauth.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("auth failure cache policy")
	}
	deploymentRequest(t, router, "POST", base, member, "principal-member", "{", 403, nil)
	var created channelapp.CommandResult
	first := deploymentRequest(t, router, "POST", base, owner, "principal-register", body, 201, &created)
	replay := deploymentRequest(t, router, "POST", base, owner, "principal-register", body, 201, nil)
	if first.Body.String() != replay.Body.String() || created.Principal == nil || created.Principal.Identity.ExternalUserID != "456" || created.Distribution != "NOT_EMITTED" {
		t.Fatal("identity creation/replay", first.Body.String())
	}
	if err := channelv1.Validate("principal-result.schema.json", first.Body.Bytes()); err != nil {
		t.Fatal("response contract", err)
	}
	deploymentRequest(t, router, "POST", base, owner, "principal-duplicate", body, 409, nil)
	path := base + "/" + created.Principal.ID + "/state"
	change := fmt.Sprintf(`{"account_id":%q,"expected_principal_revision":1,"state":"REVOKED"}`, account)
	var revoked channelapp.CommandResult
	reply := deploymentRequest(t, router, "POST", path, owner, "principal-revoke", change, 200, &revoked)
	if err := channelv1.Validate("principal-result.schema.json", reply.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if revoked.Principal == nil || revoked.Principal.State != "REVOKED" || revoked.Principal.Revision != 2 || revoked.Distribution != "NOT_EMITTED" {
		t.Fatal("state result", reply.Body.String())
	}
	again := deploymentRequest(t, router, "POST", path, owner, "principal-revoke", change, 200, nil)
	if again.Body.String() != reply.Body.String() {
		t.Fatal("state replay changed")
	}
	deploymentRequest(t, router, "POST", path, owner, "principal-stale", change, 409, nil)
	deploymentRequest(t, router, "POST", base, owner, "principal-reregister", body, 409, nil)
	deploymentRequest(t, router, "POST", path, member, "principal-member-state", change, 403, nil)
	wrong := `{"account_id":"cha_missing","expected_principal_revision":2,"state":"ACTIVE"}`
	deploymentRequest(t, router, "POST", path, owner, "principal-wrong-account", wrong, 404, nil)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_command_receipts WHERE tenant_id=$1 AND operation IN ('RegisterChannelPrincipal','SetChannelPrincipalState')`, tenant).Scan(&count); err != nil || count != 2 {
		t.Fatal("atomic successful receipts", count, err)
	}
}
