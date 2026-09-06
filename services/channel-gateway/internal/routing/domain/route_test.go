package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/routing/domain"
)

func enabled() domain.RouteEvent {
	return domain.RouteEvent{EventID: "evt-1", SchemaVersion: 1, Enabled: true, Route: domain.RouteSnapshot{Provider: "telegram", AccountID: "account-1", TenantID: "tenant-1", BindingID: "binding-1", Generation: 7, DeploymentRevisionID: "revision-1", ManifestRef: "manifest/revision-1", ManifestDigest: "sha256:" + strings.Repeat("a", 64)}}
}

func TestValidateTargetsAndTombstones(t *testing.T) {
	valid := enabled()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*domain.RouteEvent){
		"empty-event":         func(v *domain.RouteEvent) { v.EventID = "" },
		"event-too-long":      func(v *domain.RouteEvent) { v.EventID = strings.Repeat("e", 129) },
		"version":             func(v *domain.RouteEvent) { v.SchemaVersion = 2 },
		"provider":            func(v *domain.RouteEvent) { v.Route.Provider = "slack" },
		"account":             func(v *domain.RouteEvent) { v.Route.AccountID = "account with spaces" },
		"generation-zero":     func(v *domain.RouteEvent) { v.Route.Generation = 0 },
		"generation-overflow": func(v *domain.RouteEvent) { v.Route.Generation = domain.MaxGeneration + 1 },
		"tenant":              func(v *domain.RouteEvent) { v.Route.TenantID = "" },
		"binding":             func(v *domain.RouteEvent) { v.Route.BindingID = "" },
		"revision":            func(v *domain.RouteEvent) { v.Route.DeploymentRevisionID = "" },
		"manifest":            func(v *domain.RouteEvent) { v.Route.ManifestRef = "https://example.invalid/m?credential=x" },
		"manifest-too-long":   func(v *domain.RouteEvent) { v.Route.ManifestRef = strings.Repeat("m", 2049) },
		"digest":              func(v *domain.RouteEvent) { v.Route.ManifestDigest = "sha256:abc" },
		"tombstone-target":    func(v *domain.RouteEvent) { v.Enabled = false },
	} {
		t.Run(name, func(t *testing.T) {
			v := valid
			change(&v)
			if err := v.Validate(); !errors.Is(err, domain.ErrInvalidEvent) {
				t.Fatalf("got %v", err)
			}
		})
	}
	tombstone := domain.RouteEvent{EventID: "evt-disabled", SchemaVersion: 1, Route: domain.RouteSnapshot{Provider: "wecom", AccountID: "account-2", Generation: 8}}
	if err := tombstone.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestMonotonicProjectionAndStableDigests(t *testing.T) {
	current := enabled()
	digest := current.ProjectionDigest()
	replay := current
	replay.EventID = "evt-second-id"
	if replay.ProjectionDigest() != digest || replay.ReceiptDigest() == current.ReceiptDigest() {
		t.Fatal("event and projection identities conflated")
	}
	tests := []struct {
		name     string
		event    domain.RouteEvent
		want     domain.Change
		conflict bool
	}{
		{"first", current, domain.Replace, false},
		{"exact-replay", current, domain.Ignore, false},
		{"equivalent-new-event", replay, domain.Ignore, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			generation := current.Route.Generation
			if tc.name == "first" {
				generation = 0
			}
			got, err := domain.Compare(generation, digest, tc.event)
			if err != nil || got != tc.want {
				t.Fatalf("got %v %v", got, err)
			}
		})
	}
	changed := current
	changed.Route.BindingID = "binding-2"
	if _, err := domain.Compare(current.Route.Generation, digest, changed); !errors.Is(err, domain.ErrGenerationConflict) {
		t.Fatalf("same-generation update: %v", err)
	}
	disabled := domain.RouteEvent{EventID: "evt-disabled", SchemaVersion: 1, Route: domain.RouteSnapshot{Provider: "telegram", AccountID: "account-1", Generation: 8}}
	if change, err := domain.Compare(7, digest, disabled); err != nil || change != domain.Replace {
		t.Fatalf("disable: %v %v", change, err)
	}
	if change, err := domain.Compare(8, disabled.ProjectionDigest(), current); err != nil || change != domain.Ignore {
		t.Fatalf("stale: %v %v", change, err)
	}
	changed.Route.Generation = 9
	if change, err := domain.Compare(8, disabled.ProjectionDigest(), changed); err != nil || change != domain.Replace {
		t.Fatalf("reenable: %v %v", change, err)
	}
}
