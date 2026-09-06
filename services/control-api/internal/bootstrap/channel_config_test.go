package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChannelConfigurationIsExplicitAndClosed(t *testing.T) {
	cfg, err := loadChannelConfig("")
	if err != nil || cfg != nil {
		t.Fatal("unset Channel must remain unregistered")
	}
	for _, body := range []string{`{}`, `{"scope_id":"one","scope_id":"two"}`, `{"unknown":true}`, `{} {}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadChannelConfig(path); err == nil {
			t.Fatal("invalid config silently accepted")
		}
	}
}
func TestChannelPrivateFilesRequireOwnerOnlyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(path, []byte(`{"field":"fixture"}`), 0644); err != nil {
		t.Fatal(err)
	}
	var shape struct {
		Field string `json:"field"`
	}
	if err := readConfigFile(path, true, &shape); err == nil {
		t.Fatal("world readable key file accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := readConfigFile(path, true, &shape); err != nil {
		t.Fatal(err)
	}
	if err := readConfigFile("relative.json", true, &shape); err == nil {
		t.Fatal("relative key path accepted")
	}
}
func TestChannelNATSRequiresAuthenticatedRestrictedTransport(t *testing.T) {
	for _, cfg := range []channelNATSConfig{{URL: "nats://127.0.0.1:4222"}, {URL: "nats://broker.internal:4222", User: "control", Password: "fixture"}, {URL: "nats://user:pass@127.0.0.1:4222", User: "control", Password: "fixture"}, {URL: "nats://127.0.0.1:4222/?token=fixture", User: "control", Password: "fixture"}} {
		if _, err := cfg.options(); err == nil {
			t.Fatal("invalid producer config accepted")
		}
	}
	for _, address := range []string{"nats://127.0.0.1:4222", "tls://broker.internal:4222"} {
		if _, err := (channelNATSConfig{URL: address, User: "control", Password: "fixture"}).options(); err != nil {
			t.Fatal(err)
		}
	}
}
