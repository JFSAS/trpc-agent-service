package bootstrap

import "testing"

func TestAuthorizationRefreshBootstrapRealRuntime(t *testing.T) {
	t.Setenv("GATEWAY_TEST_AUTHORIZATION_BOOTSTRAP", "1")
	TestControlRuntimeMTLSRealPGNATSRotationAndInbound(t)
}
func TestAuthorizationRefreshConfigAndDisabledConstruction(t *testing.T) {
	c := configWithAccounts(t, nil)
	c.AuthorizationRefreshEnabled = true
	if c.Validate() == nil {
		t.Fatal("fixture mode accepted")
	}
	c.AuthorizationRefreshEnabled = false
	if r, err := newAuthorizationRefresh(c, nil, nil); err != nil || r != nil {
		t.Fatal("disabled acquired dependencies", r, err)
	}
	c.AuthorizationRefreshEnabled = true
	c.AccountSource = "control"
	if _, err := newAuthorizationRefresh(c, nil, nil); err == nil {
		t.Fatal("missing dependencies")
	}
	t.Setenv("GATEWAY_AUTHORIZATION_REFRESH_ENABLED", "yes")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("invalid flag")
	}
}
