// Package domain owns non-secret route projections and their monotonic generation
// rules. It contains no database, transport, provider SDK, or event DTO types.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

var (
	ErrUnavailable        = errors.New("route unavailable")
	ErrGenerationConflict = errors.New("route generation conflict")
	ErrInvalidEvent       = errors.New("invalid route event")
)

// MaxGeneration is the largest integer represented exactly by the JSON ecosystem.
const MaxGeneration int64 = 9007199254740991

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var manifestReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
var manifestDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// RouteSnapshot fixes the exact published target. Generation is scoped to
// (Provider, AccountID), never to a socket or Gateway process.
type RouteSnapshot struct {
	Provider             string `json:"provider"`
	AccountID            string `json:"account_id"`
	TenantID             string `json:"tenant_id,omitempty"`
	BindingID            string `json:"binding_id,omitempty"`
	Generation           int64  `json:"generation"`
	DeploymentRevisionID string `json:"deployment_revision_id,omitempty"`
	ManifestRef          string `json:"manifest_ref,omitempty"`
	ManifestDigest       string `json:"manifest_digest,omitempty"`
}

// RouteEvent contains a complete replacement, or a disabled tombstone. EventID
// identifies the producer event permanently, independent of route generation.
type RouteEvent struct {
	EventID       string        `json:"event_id"`
	SchemaVersion int           `json:"schema_version"`
	Route         RouteSnapshot `json:"route"`
	Enabled       bool          `json:"enabled"`
}

func ValidateAccount(provider, accountID string) error {
	if (provider != "telegram" && provider != "wecom") || !identifier.MatchString(accountID) {
		return fmt.Errorf("%w: provider or account identity", ErrInvalidEvent)
	}
	return nil
}

func (event RouteEvent) Validate() error {
	if event.SchemaVersion != 1 || !identifier.MatchString(event.EventID) {
		return fmt.Errorf("%w: envelope", ErrInvalidEvent)
	}
	return event.Route.Validate(event.Enabled)
}

func (route RouteSnapshot) Validate(enabled bool) error {
	if err := ValidateAccount(route.Provider, route.AccountID); err != nil {
		return err
	}
	if route.Generation < 1 || route.Generation > MaxGeneration {
		return fmt.Errorf("%w: generation", ErrInvalidEvent)
	}
	if !enabled {
		if route.TenantID != "" || route.BindingID != "" || route.DeploymentRevisionID != "" || route.ManifestRef != "" || route.ManifestDigest != "" {
			return fmt.Errorf("%w: tombstone includes target", ErrInvalidEvent)
		}
		return nil
	}
	if !identifier.MatchString(route.TenantID) || !identifier.MatchString(route.BindingID) || !identifier.MatchString(route.DeploymentRevisionID) || len(route.ManifestRef) > 2048 || !manifestReference.MatchString(route.ManifestRef) || !manifestDigest.MatchString(route.ManifestDigest) {
		return fmt.Errorf("%w: published target", ErrInvalidEvent)
	}
	return nil
}

// ProjectionDigest compares semantic content across event IDs. The immutable
// struct field order and restricted scalar values form the V1 local encoding.
func (event RouteEvent) ProjectionDigest() string {
	return digest(struct {
		Route   RouteSnapshot `json:"route"`
		Enabled bool          `json:"enabled"`
	}{event.Route, event.Enabled})
}

// ReceiptDigest rejects reuse of an EventID with different envelope content.
func (event RouteEvent) ReceiptDigest() string { return digest(event) }

func digest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic("routing scalar value is not JSON encodable: " + err.Error())
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Change is the outcome of comparing one valid event to an existing projection.
type Change uint8

const (
	Ignore Change = iota
	Replace
)

// Compare applies monotonic replacement without treating a new event ID as a new
// generation. A same-generation changed payload always conflicts.
func Compare(currentGeneration int64, currentDigest string, event RouteEvent) (Change, error) {
	if err := event.Validate(); err != nil {
		return Ignore, err
	}
	if event.Route.Generation < currentGeneration {
		return Ignore, nil
	}
	if event.Route.Generation == currentGeneration {
		if event.ProjectionDigest() != currentDigest {
			return Ignore, ErrGenerationConflict
		}
		return Ignore, nil
	}
	return Replace, nil
}
