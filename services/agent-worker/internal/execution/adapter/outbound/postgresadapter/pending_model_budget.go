package postgresadapter

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	budget "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
	session "github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/session/domain"
)

// PendingModelBudget is policy evidence, not a consumption grant. The caller still
// needs an enforceable operation bound and an atomic reservation before dispatch.
type PendingModelBudget struct {
	TenantID string
	Policy   budget.QuotaReference
	Cap      int64
}

// ReadModelBudget binds the published cap to this persisted ingress event and its
// current authorized principal/route. It keeps current-head locks in the caller's
// transaction and performs no external I/O, reservation, Run or Attempt creation.
// It is not valid to cache this result as permission for later model calls.
func (p *PendingRouteAuthority) ReadModelBudget(ctx context.Context, tx pgx.Tx, scope session.Scope, actor string) (PendingModelBudget, error) {
	var zero PendingModelBudget
	if err := p.AuthorizeSessionRoute(ctx, tx, scope, actor, "message.send"); err != nil {
		return zero, err
	}
	// AuthorizeSessionRoute has verified the full access-policy/dependency envelope
	// and holds the current snapshot row FOR SHARE. Its quota reference cannot be
	// replaced between that check and this same-transaction read.
	var raw, sessionRaw []byte
	var start, until, now time.Time
	err := tx.QueryRow(ctx, `SELECT quota_policy_jsonb,session_policy_jsonb,read_started_at,fresh_until FROM worker_authorization_snapshots WHERE scope_id=$1 AND account_id=$2`, p.scope, scope.AccountID).Scan(&raw, &sessionRaw, &start, &until)
	if err != nil {
		return zero, err
	}
	doc, err := wire.DecodePolicyDefinitionDocument(raw)
	if err != nil || doc.TenantID != scope.TenantID || doc.Kind != "quota" || !doc.Definition.Enabled || doc.Definition.Quota == nil || doc.Definition.Quota.MaxTotalModelTokens == nil {
		return zero, budget.ErrNotReady
	}
	selected, err := wire.DecodePolicyDefinitionDocument(sessionRaw)
	if err != nil || selected.TenantID != scope.TenantID || selected.Kind != "session" || selected.PolicyID != scope.PolicyID || selected.Revision != scope.PolicyRevision || selected.Digest != scope.PolicyDigest || selected.Definition.Session == nil || selected.Definition.Session.Partition != scope.Partition {
		return zero, budget.ErrNotReady
	}
	// Recheck after target/Manifest resolution and document decoding; the source
	// may expire while this transaction holds its head lock.
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return zero, err
	}
	if now.Before(start) || !now.Before(until) {
		return zero, budget.ErrNotReady
	}
	return PendingModelBudget{TenantID: doc.TenantID, Policy: budget.QuotaReference{ID: doc.PolicyID, Revision: doc.Revision, Digest: doc.Digest}, Cap: *doc.Definition.Quota.MaxTotalModelTokens}, nil
}
