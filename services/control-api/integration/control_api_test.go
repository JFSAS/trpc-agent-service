package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/admin"
	adminapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/admin/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity"
	identityapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
	sharedpostgres "github.com/liuzengh/trpc-agent-service/services/control-api/internal/infra/postgres"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/tenant"
	"github.com/liuzengh/trpc-agent-service/services/control-api/migrations"
)

func TestIdentityAdminTenantV1AgainstPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("CONTROL_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CONTROL_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	adminPool, err := sharedpostgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	schema := fmt.Sprintf("control_v1_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA "+quotedSchema+" CASCADE")
	})

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := sharedpostgres.Migrate(ctx, pool, migrations.Files); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	identityModule, err := identity.NewModule(identity.Dependencies{
		DB: pool, Routes: router, SessionLifetime: time.Hour,
		CookieName: "control_session", CookieSecure: false,
		CookieSameSite: http.SameSiteLaxMode,
	})
	if err != nil {
		t.Fatal(err)
	}
	tenantModule, err := tenant.NewModule(tenant.Dependencies{
		DB: pool, Routes: router, Authenticate: identityModule.AuthenticationMiddleware(),
		Accounts: accountLookup{accounts: identityModule.Accounts},
	})
	if err != nil {
		t.Fatal(err)
	}
	adminModule, err := admin.NewModule(admin.Dependencies{
		DB: pool, Routes: router, Authenticate: identityModule.AuthenticationMiddleware(),
		Accounts: identityModule.Accounts, Tenants: tenantModule.Service,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adminModule.Service.EnsureInitialOperator(ctx, adminapp.EnsureInitialOperatorCommand{
		Username: "platform-admin", DisplayName: "Platform Admin",
		TemporaryPassword: "Initial-pass-1234",
	}); err != nil {
		t.Fatalf("bootstrap operator: %v", err)
	}

	adminCookie := login(t, router, "platform-admin", "Initial-pass-1234", true)
	request(t, router, http.MethodPost, "/v1/me/change-password", adminCookie,
		`{"current_password":"Initial-pass-1234","new_password":"Final-admin-pass-5678"}`,
		http.StatusNoContent, nil)
	request(t, router, http.MethodGet, "/v1/admin/capabilities", adminCookie, "",
		http.StatusOK, nil)
	var user struct {
		ID string `json:"id"`
	}
	request(t, router, http.MethodPost, "/v1/admin/users", adminCookie,
		`{"username":"alice","display_name":"Alice","temporary_password":"Alice-temp-pass-1234"}`,
		http.StatusCreated, &user)
	var provisioned struct {
		ID string `json:"id"`
	}
	request(t, router, http.MethodPost, "/v1/admin/tenants", adminCookie,
		fmt.Sprintf(`{"slug":"team-a","name":"Team A","owner_user_id":%q}`, user.ID),
		http.StatusCreated, &provisioned)

	aliceCookie := login(t, router, "alice", "Alice-temp-pass-1234", true)
	request(t, router, http.MethodPost, "/v1/me/change-password", aliceCookie,
		`{"current_password":"Alice-temp-pass-1234","new_password":"Alice-final-pass-5678"}`,
		http.StatusNoContent, nil)
	var tenants struct {
		Tenants []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"tenants"`
	}
	request(t, router, http.MethodGet, "/v1/me/tenants", aliceCookie, "",
		http.StatusOK, &tenants)
	if len(tenants.Tenants) != 1 || tenants.Tenants[0].ID != provisioned.ID ||
		tenants.Tenants[0].Role != "OWNER" {
		t.Fatalf("tenant memberships = %#v", tenants.Tenants)
	}
}

type accountLookup struct {
	accounts *identityapp.AccountManagement
}

func (lookup accountLookup) IsActiveAccount(ctx context.Context, userID string) (bool, error) {
	account, err := lookup.accounts.GetAccount(ctx, userID)
	if err != nil {
		return false, err
	}
	return account.CanLogin(), nil
}

func login(
	t *testing.T,
	handler http.Handler,
	username, password string,
	wantRestricted bool,
) *http.Cookie {
	t.Helper()
	var response struct {
		PasswordChangeRequired bool `json:"password_change_required"`
	}
	recorder := request(t, handler, http.MethodPost, "/v1/auth/login", nil,
		fmt.Sprintf(`{"username":%q,"password":%q}`, username, password),
		http.StatusOK, &response)
	if response.PasswordChangeRequired != wantRestricted {
		t.Fatalf("password_change_required = %v", response.PasswordChangeRequired)
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "control_session" {
			return cookie
		}
	}
	t.Fatal("session cookie was not set")
	return nil
}

func request(
	t *testing.T,
	handler http.Handler,
	method, path string,
	cookie *http.Cookie,
	body string,
	wantStatus int,
	destination any,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s: status = %d, body = %s", method, path, recorder.Code, recorder.Body.String())
	}
	if destination != nil {
		decoder := json.NewDecoder(bytes.NewReader(recorder.Body.Bytes()))
		if err := decoder.Decode(destination); err != nil {
			t.Fatalf("decode %s %s response: %v", method, path, err)
		}
	}
	return recorder
}
