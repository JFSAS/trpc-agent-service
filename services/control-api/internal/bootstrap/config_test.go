package bootstrap

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigUsesV1Defaults(t *testing.T) {
	key := configTestEnvironment(t)
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
	if !bytes.Equal(config.ProfileCredentialKey, key) {
		t.Fatal("credential key was not decoded exactly")
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
	if config.DeploymentAllowedEndpointHosts != nil {
		t.Fatalf("DeploymentAllowedEndpointHosts = %#v, want nil default", config.DeploymentAllowedEndpointHosts)
	}
}

func TestLoadConfigReadsDeploymentAllowedEndpointHosts(t *testing.T) {
	configTestEnvironment(t)
	t.Setenv("CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS", "models.example, search.example ,state.example")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	want := []string{"models.example", "search.example", "state.example"}
	if !reflect.DeepEqual(config.DeploymentAllowedEndpointHosts, want) {
		t.Fatalf("DeploymentAllowedEndpointHosts = %#v, want %#v", config.DeploymentAllowedEndpointHosts, want)
	}
}

func TestDeploymentPlatformContractUsesConfiguredHosts(t *testing.T) {
	configured := []string{"models.internal.example", "state.internal.example"}
	contract, err := deploymentPlatformContract(Config{DeploymentAllowedEndpointHosts: configured})
	if err != nil {
		t.Fatalf("deploymentPlatformContract() error = %v", err)
	}
	if !reflect.DeepEqual(contract.Execution.AllowedEndpointHosts, configured) {
		t.Fatalf("AllowedEndpointHosts = %#v, want %#v", contract.Execution.AllowedEndpointHosts, configured)
	}
	wantDigest, err := contract.CalculateDigest()
	if err != nil {
		t.Fatal(err)
	}
	if contract.Digest != wantDigest {
		t.Fatalf("Digest = %q, want %q", contract.Digest, wantDigest)
	}
}

func TestDeploymentPlatformContractRejectsInvalidConfiguredHosts(t *testing.T) {
	for _, hosts := range [][]string{{"UPPER.example"}, {"same.example", "same.example"}, {""}} {
		_, err := deploymentPlatformContract(Config{DeploymentAllowedEndpointHosts: hosts})
		if err == nil || !strings.Contains(err.Error(), "validate deployment platform contract") {
			t.Fatalf("deploymentPlatformContract(%#v) error = %v, want validation error", hosts, err)
		}
	}
}

func TestLoadConfigRequiresDatabaseURL(t *testing.T) {
	configTestEnvironment(t)
	t.Setenv("CONTROL_DATABASE_URL", "")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "CONTROL_DATABASE_URL") {
		t.Fatal("expected missing database URL error")
	}
}

func TestLoadConfigRejectsInvalidSessionLifetime(t *testing.T) {
	configTestEnvironment(t)
	t.Setenv("CONTROL_DATABASE_URL", "postgres://control:secret@localhost/control")
	t.Setenv("CONTROL_SESSION_LIFETIME", "tomorrow")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "CONTROL_SESSION_LIFETIME") {
		t.Fatal("expected invalid session lifetime error")
	}
}

func TestLoadConfigRequiresBootstrapPasswordInAutoMode(t *testing.T) {
	configTestEnvironment(t)
	t.Setenv("CONTROL_DATABASE_URL", "postgres://control:secret@localhost/control")
	t.Setenv("CONTROL_BOOTSTRAP_MODE", "auto")
	t.Setenv("CONTROL_BOOTSTRAP_USERNAME", "root")
	t.Setenv("CONTROL_BOOTSTRAP_PASSWORD", "")

	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "CONTROL_BOOTSTRAP_PASSWORD") {
		t.Fatal("expected missing bootstrap password error")
	}
}

func TestLoadConfigRequiresProfileCredentialKey(t *testing.T) {
	configTestEnvironment(t)
	t.Setenv("CONTROL_DATABASE_URL", "postgres://localhost/test")
	for _, value := range []string{"", "not-base64", "YWJj"} {
		t.Setenv("CONTROL_PROFILE_CREDENTIAL_KEY", value)
		if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "CONTROL_PROFILE_CREDENTIAL_KEY") {
			t.Fatal("expected missing or invalid credential key error")
		}
	}
}

func configTestEnvironment(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTROL_PROFILE_CREDENTIAL_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("CONTROL_DATABASE_URL", "postgres://localhost/control_test")
	t.Setenv("CONTROL_BOOTSTRAP_MODE", "disabled")
	t.Setenv("CONTROL_SESSION_LIFETIME", "")
	t.Setenv("CONTROL_SESSION_COOKIE_SECURE", "")
	t.Setenv("CONTROL_DEPLOYMENT_ALLOWED_ENDPOINT_HOSTS", "")
	digest, err := DeploymentContractDigestFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTROL_DEPLOYMENT_EXPECTED_CONTRACT_DIGEST", digest)
	return key
}
