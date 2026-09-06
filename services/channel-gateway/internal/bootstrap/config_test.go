package bootstrap

import (
	"context"
	"fmt"
	telegram "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/adapter/inbound/telegramadapter"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
	transport "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/infra/nats"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://unused/unused")
	t.Setenv("GATEWAY_NATS_URL", "nats://unused:4222")
	t.Setenv("GATEWAY_NATS_TOPOLOGY_FILE", "../../../../deploy/nats/streams.yaml")
	t.Setenv("GATEWAY_HTTP_ADDRESS", ":8090")
	t.Setenv("GATEWAY_ADMIN_ADDRESS", ":8091")
	t.Setenv("GATEWAY_NATS_USER", "")
	t.Setenv("GATEWAY_NATS_PASSWORD", "")
	t.Setenv("TEST_WEBHOOK_SECRET", "test_secret_value_1234")
	for _, tt := range []struct {
		name, body string
		good       bool
	}{
		{"empty_accounts", "[]", true}, {"one", `[{"account_id":"account-1","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, true},
		{"duplicate", `[{"account_id":"a","webhook_secret_env":"TEST_WEBHOOK_SECRET"},{"account_id":"a","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"mux_path", `[{"account_id":"a/{path}","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false}, {"unknown", `[{"account_id":"a","token":"do-not-accept"}]`, false},
		{"colon_id", `[{"account_id":"account:1","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"duplicate_id_key", `[{"account_id":"a","account_id":"b","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"duplicate_same_id", `[{"account_id":"a","account_id":"a","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"duplicate_secret_ref", `[{"account_id":"a","webhook_secret_env":"TEST_WEBHOOK_SECRET","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"escaped_duplicate", `[{"account_id":"a","\u0061ccount_id":"b","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"case_alias_id", `[{"ACCOUNT_ID":"a","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"case_alias_ref", `[{"account_id":"a","WEBHOOK_SECRET_ENV":"TEST_WEBHOOK_SECRET"}]`, false},
		{"mixed_case_alias", `[{"account_id":"a","Account_Id":"b","webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"missing_id", `[{"webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"missing_ref", `[{"account_id":"a"}]`, false},
		{"null_ref", `[{"account_id":"a","webhook_secret_env":null}]`, false},
		{"nested_value", `[{"account_id":{"value":"a"},"webhook_secret_env":"TEST_WEBHOOK_SECRET"}]`, false},
		{"null_item", `[null]`, false}, {"array_item", `[[]]`, false},
		{"whitespace_tail", "  []\n\t", true},
		{"null", "null", false}, {"multiple", "[] []", false}, {"oversized", strings.Repeat(" ", 65537) + "[]", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "accounts.json")
			if err := os.WriteFile(p, []byte(tt.body), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GATEWAY_TELEGRAM_ACCOUNTS_FILE", p)
			t.Setenv("GATEWAY_ACCOUNT_SOURCE", "fixture")
			_, err := LoadConfig()
			if (err == nil) != tt.good {
				t.Fatalf("good=%v err=%v", tt.good, err)
			}
			if err != nil && strings.Contains(err.Error(), "test_secret_value") {
				t.Fatal("secret in error")
			}
		})
	}
}

type configAcceptor struct{}

func (configAcceptor) AcceptInbound(context.Context, domain.Inbound) (domain.Receipt, error) {
	return domain.Receipt{}, nil
}

func TestConfiguredTelegramIDsMatchHandler(t *testing.T) {
	for _, id := range []string{"a", "account-1", "Account_1.test", strings.Repeat("a", 128)} {
		t.Run(id, func(t *testing.T) {
			c := configWithAccounts(t, []Account{{ID: id, SecretEnv: "TEST_WEBHOOK_SECRET", Secret: "test_secret_value_1234"}})
			if err := c.Validate(); err != nil {
				t.Fatalf("valid config rejected: %v", err)
			}
			if _, err := telegram.NewHandler(id, c.Accounts[0].Secret, configAcceptor{}); err != nil {
				t.Fatalf("Config accepted ID rejected by Telegram handler: %v", err)
			}
		})
	}
	for _, id := range []string{"account:1", ":a", "_a", ".a", "-a", "a/b", strings.Repeat("a", 129)} {
		c := configWithAccounts(t, []Account{{ID: id, SecretEnv: "TEST_WEBHOOK_SECRET", Secret: "test_secret_value_1234"}})
		if c.Validate() == nil {
			t.Fatalf("invalid configured identity accepted: %q", id)
		}
	}
}
func configWithAccounts(t *testing.T, accounts []Account) Config {
	t.Helper()
	topology, err := transport.LoadTopology("../../../../deploy/nats/streams.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return Config{HTTPAddress: ":8090", AdminAddress: ":8091", DatabaseURL: "postgres://unused/unused", NATSURL: "nats://unused:4222", Topology: topology, Accounts: accounts}
}
func TestAccountConfigCountLimit(t *testing.T) {
	for _, count := range []int{0, 100, 101} {
		accounts := make([]Account, count)
		for i := range accounts {
			accounts[i] = Account{ID: fmt.Sprintf("account-%d", i), SecretEnv: "TEST_WEBHOOK_SECRET", Secret: "test_secret_value_1234"}
		}
		c := configWithAccounts(t, accounts)
		if (c.Validate() == nil) != (count <= 100) {
			t.Fatalf("account count %d validated incorrectly", count)
		}
	}
}
