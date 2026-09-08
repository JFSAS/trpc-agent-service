package postgresadapter

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/execution/domain"
	session "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
	"time"
)

// PendingTargetVerifier is supplied by bootstrap using the Manifest owner's
// transaction reader. This adapter never queries another module's tables.
type PendingTargetVerifier interface {
	VerifyPendingTarget(context.Context, pgx.Tx, domain.Route) error
}

// PendingRouteAuthority is bound to ONE persisted ingress event and source epoch.
// A pending message proves input provenance, never current permission or reset
// authority. CurrentAuthorizer must still check the current policy/session grant.
type PendingRouteAuthority struct {
	ledger              *Ledger
	event, scope, epoch string
	target              PendingTargetVerifier
}

func (l *Ledger) PendingRouteAuthority(event, scope, epoch string, target PendingTargetVerifier) (*PendingRouteAuthority, error) {
	if l == nil || !session.ValidActor(event) || !session.ValidActor(scope) || epoch == "" || target == nil {
		return nil, domain.ErrInvalid
	}
	return &PendingRouteAuthority{l, event, scope, epoch, target}, nil
}
func (p *PendingRouteAuthority) AuthorizeSessionRoute(ctx context.Context, tx pgx.Tx, s session.Scope, actor, operation string) error {
	if ctx == nil || tx == nil || s.Validate() != nil || !session.ValidActor(actor) {
		return domain.ErrInvalid
	}
	if operation != "message.send" {
		return session.ErrDenied
	}
	var raw []byte
	var expiry, now time.Time
	// Pending rows are immutable; SHARE also pins their existence through final
	// authorization. Promotion must keep this row until its final check completes.
	if err := tx.QueryRow(ctx, `SELECT request_json,expires_at FROM execution_pending_intakes WHERE event_id=$1 FOR SHARE`, p.event).Scan(&raw, &expiry); err != nil {
		return domain.ErrNotReady
	}
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil || !now.Before(expiry) {
		return domain.ErrNotReady
	}
	var req domain.Requested
	if json.Unmarshal(raw, &req) != nil || req.Validate() != nil || req.Authorization == nil || req.EventID != p.event {
		return domain.ErrNotReady
	}
	a := req.Authorization
	if a.ScopeID != p.scope || a.SourceEpoch != p.epoch {
		return domain.ErrNotReady
	}
	if actor != a.PrincipalID || s.TenantID != req.Route.TenantID || s.Provider != req.Route.Provider || s.AccountID != req.Route.AccountID || s.BindingID != req.Route.BindingID || s.DeploymentRevisionID != req.Route.DeploymentRevisionID || s.ConversationID != req.Input.ConversationID || s.ConversationKind != a.ConversationKind || s.ThreadID != req.Input.ThreadID || (s.Partition == session.PerUser && s.PrincipalID != actor) {
		return session.ErrDenied
	}
	// Includes current external-user -> principal mapping and revision/generation
	// floors from the historical AdmissionAuthorization, plus current freshness.
	if err := p.ledger.currentAuthorization(ctx, tx, req); err != nil {
		return err
	}
	if err := p.target.VerifyPendingTarget(ctx, tx, req.Route); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil || !now.Before(expiry) {
		return domain.ErrNotReady
	}
	return nil
}
