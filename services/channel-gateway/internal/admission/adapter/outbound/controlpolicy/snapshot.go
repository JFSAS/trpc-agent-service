package controlpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/admission/domain"
)

var ErrSnapshotChanged = errors.New("AUTHORIZATION_SNAPSHOT_CHANGED")
var ErrSnapshotExpired = errors.New("AUTHORIZATION_SNAPSHOT_EXPIRED")
var ErrSnapshotStage = errors.New("AUTHORIZATION_SNAPSHOT_STAGE_FAILED")

// AuthorizationTarget comes from the trusted account projection, not an inbound
// message or user-selected tenant. Scope and source epoch remain client-pinned.
type AuthorizationTarget = domain.AuthorizationTarget

// AuthorizationRead proves a complete fetched set, not an installed grant.
type AuthorizationRead = domain.AuthorizationRead

// ReadAuthorization invokes stage only for validated sequential pages. stage
// MUST write solely to an uncommitted/staging area; the owner must roll it back
// on ANY error, and publish nothing until this method and its own install fences
// succeed. Errors return a zero value, including failures after partial staging.
// No automatic retry renews an earlier manifest. The entire read (including the
// callback and exact policy lookup) must fit the original request-start age bound.
func (c *Client) ReadAuthorization(ctx context.Context, target AuthorizationTarget, stage func(context.Context, wire.AuthorizationSnapshotPage) error) (AuthorizationRead, error) {
	zero := AuthorizationRead{}
	if ctx == nil || stage == nil || !scopePattern.MatchString(target.TenantID) || !scopePattern.MatchString(target.AccountID) || (target.Provider != "telegram" && target.Provider != "wecom") {
		return zero, ErrInvalid
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	started := time.Now()
	call, cancel := context.WithDeadline(ctx, started.Add(30*time.Second))
	defer cancel()
	request := struct {
		SchemaVersion int    `json:"schema_version"`
		AccountID     string `json:"account_id"`
	}{1, target.AccountID}
	body, _ := json.Marshal(request)
	raw, err := c.post(call, c.origin+"/internal/v1/channel-authorizations:snapshot", body, wire.MaxAuthorizationManifestBytes, true)
	if err != nil {
		return zero, err
	}
	m, err := wire.DecodeAuthorizationSnapshotManifest(raw)
	if err != nil {
		return zero, ErrIntegrity
	}
	if m.ScopeID != c.scope || m.SourceEpoch != c.epoch || m.TenantID != target.TenantID || m.AccountID != target.AccountID || m.Provider != target.Provider {
		return zero, ErrIntegrity
	}
	// Control creates a new read transaction for this uncached authenticated POST.
	// Anchoring BEFORE that request consumes network/processing/page time without
	// relying on matching wall clocks or ever extending from CapturedAt/page time.
	expires := started.Add(time.Duration(m.AuthorizationMaxAgeMS) * time.Millisecond)
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	if !time.Now().Before(expires) {
		return zero, ErrSnapshotExpired
	}
	bounded, stop := context.WithDeadline(call, expires)
	defer stop()
	failure := func(err error) (AuthorizationRead, error) {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		if !time.Now().Before(expires) {
			return zero, ErrSnapshotExpired
		}
		return zero, err
	}
	policy, err := c.fetchReference(bounded, target.TenantID, target.AccountID, target.Provider, m.Policy)
	if err != nil {
		return failure(err)
	}
	if policy.Body.AuthorizationMaxAgeMS != m.AuthorizationMaxAgeMS {
		return zero, ErrIntegrity
	}
	proof, err := wire.NewAuthorizationSnapshotProof(m)
	if err != nil {
		return zero, ErrIntegrity
	}
	cursor := ""
	for {
		if bounded.Err() != nil {
			return failure(bounded.Err())
		}
		req := struct {
			SchemaVersion    int    `json:"schema_version"`
			AccountID        string `json:"account_id"`
			SourceEpoch      string `json:"source_epoch"`
			Generation       int64  `json:"generation"`
			AfterPrincipalID string `json:"after_principal_id"`
		}{1, target.AccountID, c.epoch, m.Generation, cursor}
		body, _ = json.Marshal(req)
		raw, err = c.post(bounded, c.origin+"/internal/v1/channel-authorizations:page", body, wire.MaxAuthorizationPageBytes, true)
		if err != nil {
			return failure(err)
		}
		page, e := wire.DecodeAuthorizationSnapshotPage(raw)
		if e != nil || proof.Add(page) != nil {
			return zero, ErrIntegrity
		}
		if err = stage(bounded, page); err != nil {
			return failure(ErrSnapshotStage)
		}
		if bounded.Err() != nil {
			return failure(bounded.Err())
		}
		if page.Complete {
			break
		}
		cursor = page.NextPrincipalID
	}
	if proof.Finish() != nil {
		return zero, ErrIntegrity
	}
	if !time.Now().Before(expires) {
		return zero, ErrSnapshotExpired
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	return AuthorizationRead{Manifest: m, Policy: policy, StartedAt: started, ExpiresAt: expires}, nil
}
