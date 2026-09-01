package bootstrap

import (
	"testing"
	"time"
)

func TestLoadConfigUsesV1Defaults(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://control:secret@localhost/control")
	t.Setenv("CONTROL_HTTP_ADDRESS", "")
	t.Setenv("CONTROL_SESSION_LIFETIME", "")
	t.Setenv("CONTROL_SESSION_COOKIE_NAME", "")
	t.Setenv("CONTROL_SESSION_COOKIE_DOMAIN", "")
	t.Setenv("CONTROL_SESSION_COOKIE_SECURE", "")
	t.Setenv("CONTROL_BOOTSTRAP_MODE", "")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.HTTPAddress != ":8080" {
		t.Fatalf("HTTPAddress = %q, want :8080", config.HTTPAddress)
	}
	if config.SessionLifetime != 24*time.Hour {
		t.Fatalf("SessionLifetime = %v, want 24h", config.SessionLifetime)
	}
	if config.SessionCookieName != "control_session" {
		t.Fatalf("SessionCookieName = %q, want control_session", config.SessionCookieName)
	}
	if !config.SessionCookieSecure {
		t.Fatal("SessionCookieSecure = false, want true")
	}
	if config.BootstrapMode != "disabled" {
		t.Fatalf("BootstrapMode = %q, want disabled", config.BootstrapMode)
	}
}

func TestLoadConfigRequiresDatabaseURL(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want missing database URL error")
	}
}

func TestLoadConfigRejectsInvalidSessionLifetime(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://control:secret@localhost/control")
	t.Setenv("CONTROL_SESSION_LIFETIME", "tomorrow")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want invalid lifetime error")
	}
}

func TestLoadConfigRequiresBootstrapPasswordInAutoMode(t *testing.T) {
	t.Setenv("CONTROL_DATABASE_URL", "postgres://control:secret@localhost/control")
	t.Setenv("CONTROL_BOOTSTRAP_MODE", "auto")
	t.Setenv("CONTROL_BOOTSTRAP_USERNAME", "root")
	t.Setenv("CONTROL_BOOTSTRAP_PASSWORD", "")

	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want bootstrap password error")
	}
}
