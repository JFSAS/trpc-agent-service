package bootstrap

import (
	"testing"
)

func TestControlConfigRejectsMixingAndUntrustedOrigins(t *testing.T) {
	c := configWithAccounts(t, nil)
	c.AccountSource = "control"
	c.InstanceID = "gw"
	c.Control = ControlConfig{URL: "https://control.example", CAFile: "ca.pem", CertificateFile: "client.pem", KeyFile: "key.pem", ScopeID: "pool", SourceEpoch: controlEpoch, PublicOrigin: "https://gateway.example"}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"http://control.example", "https://user:secret@control.example", "https://control.example/internal", "https://control.example?q=x", "https://control.example#x", ""} {
		bad := c
		bad.Control.URL = raw
		if bad.Validate() == nil {
			t.Fatal("invalid Control origin accepted")
		}
		bad = c
		bad.Control.PublicOrigin = raw
		if bad.Validate() == nil {
			t.Fatal("invalid public origin accepted")
		}
	}
	mixed := c
	mixed.Accounts = []Account{{ID: "account"}}
	if mixed.Validate() == nil {
		t.Fatal("mixed static Telegram accepted")
	}
	mixed = c
	mixed.WeComAccountsFile = "accounts.json"
	if mixed.Validate() == nil {
		t.Fatal("mixed file source accepted")
	}
	c.Control.SourceEpoch = ""
	if c.Validate() == nil {
		t.Fatal("unbootstrapped source epoch accepted")
	}
}
func TestControlLoaderDefaultsToControlNotFile(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://unused/db")
	t.Setenv("GATEWAY_NATS_URL", "nats://unused:4222")
	t.Setenv("GATEWAY_NATS_TOPOLOGY_FILE", "../../../../deploy/nats/streams.yaml")
	t.Setenv("GATEWAY_ACCOUNT_SOURCE", "")
	t.Setenv("GATEWAY_WECOM_ACCOUNTS_FILE", "")
	t.Setenv("GATEWAY_TELEGRAM_ACCOUNTS_FILE", "")
	t.Setenv("GATEWAY_CONTROL_URL", "https://control.example")
	t.Setenv("GATEWAY_CONTROL_SCOPE_ID", "pool")
	t.Setenv("GATEWAY_CONTROL_SOURCE_EPOCH", controlEpoch)
	t.Setenv("GATEWAY_CONTROL_CA_FILE", "ca.pem")
	t.Setenv("GATEWAY_CONTROL_CERT_FILE", "client.pem")
	t.Setenv("GATEWAY_CONTROL_KEY_FILE", "key.pem")
	t.Setenv("GATEWAY_PUBLIC_ORIGIN", "https://gateway.example")
	t.Setenv("GATEWAY_INSTANCE_ID", "gw")
	c, e := LoadConfig()
	if e != nil || c.AccountSource != "control" {
		t.Fatalf("default=%s err=%v", c.AccountSource, e)
	}
}
