package bootstrap

import (
	"context"
	datav1 "github.com/liuzengh/trpc-agent-service/api/runtime/data/v1"
	deployment "github.com/liuzengh/trpc-agent-service/services/control-api/internal/deployment/domain"
	backend "github.com/liuzengh/trpc-agent-service/services/control-api/internal/platformbackend/domain"
	"reflect"
	"strings"
	"testing"
)

type managedMember bool

func (m managedMember) IsActiveMember(context.Context, string, string) (bool, error) {
	return bool(m), nil
}
func TestManagedBootstrapDoesNotEnableRuntimeAdapters(t *testing.T) {
	base, err := deploymentPlatformContract(Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{PlatformBackendCatalogSHA256: strings.Repeat("a", 64), PlatformBackendTargetsSHA256: strings.Repeat("b", 64)}
	bound, err := deploymentPlatformContract(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Digest == base.Digest || bound.ManagedCatalogDigest == "" || !reflect.DeepEqual(bound.RuntimeDataCapabilities, base.RuntimeDataCapabilities) || !reflect.DeepEqual(bound.StorageAdapters, base.StorageAdapters) {
		t.Fatal("catalog changed runtime capability")
	}
	cfg.PlatformBackendCatalogSHA256 = "bad"
	if _, err = deploymentPlatformContract(cfg); err == nil {
		t.Fatal("invalid pin")
	}
}
func TestManagedBootstrapPortsAuthorizeRolesAndTenant(t *testing.T) {
	c, err := backend.NewCatalog([]backend.Entry{{ID: "redis", Revision: 1, Label: "Redis", Kind: backend.Redis, Roles: []backend.Role{backend.Session, backend.Memory}, Enabled: true, TenantIDs: []string{"tenant-a"}}})
	if err != nil {
		t.Fatal(err)
	}
	target := backend.RuntimeTarget{BackendID: "redis", BackendRevision: 1, Kind: datav1.Redis, Adapter: "managed-redis-v1", Isolation: datav1.SessionIsolation, Limits: datav1.Limits{TimeoutMS: 1000, MaxConcurrency: 1, MaxBytes: 1024}, Redis: &datav1.RedisTarget{Host: "redis.internal", Port: 6379, Username: "runtime", TLS: true}}
	targets, err := backend.NewRuntimeCatalog(c, []backend.RuntimeTarget{target})
	if err != nil {
		t.Fatal(err)
	}
	request := deployment.BackendRequest{Category: "storage", Name: "memory", BackendID: "redis", Revision: 1, Role: "memory"}
	a := deploymentBackendAccess{targets: targets, tenants: managedMember(true)}
	s, err := a.ResolveDeploymentBackend(context.Background(), "tenant-a", "user", request)
	if err != nil || s.ValidateForRole("memory") != nil {
		t.Fatal(s, err)
	}
	a.tenants = managedMember(false)
	if _, err = a.ResolveDeploymentBackend(context.Background(), "tenant-a", "user", request); err == nil {
		t.Fatal("nonmember")
	}
	p := profileBackendAccess{catalog: c}
	if err = p.CheckBackend(context.Background(), "tenant-a", "redis", 1, "memory"); err != nil {
		t.Fatal(err)
	}
	if err = p.CheckBackend(context.Background(), "tenant-b", "redis", 1, "memory"); err == nil {
		t.Fatal("cross tenant")
	}
	if err = p.CheckBackend(context.Background(), "tenant-a", "redis", 1, "artifact"); err == nil {
		t.Fatal("wrong role")
	}
}
