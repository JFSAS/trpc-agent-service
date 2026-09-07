package postgresadapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	channelv1 "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/application"
)

// ClaimAccessPolicy changes only delivery metadata of this module's committed events.
// SKIP LOCKED permits multiple replicas. Each claim gets a unique fencing token.
func (s *Store) ClaimAccessPolicy(ctx context.Context) (application.AccessPolicyClaim, bool, error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return application.AccessPolicyClaim{}, false, application.ErrDependencyUnavailable
	}
	claim := application.AccessPolicyClaim{ClaimToken: hex.EncodeToString(token)}
	err := s.db.QueryRow(ctx, `WITH next AS (
 SELECT o.tenant_id,o.id,a.id AS account_id FROM control_outbox o
 JOIN channel_access_policy_revisions p ON p.tenant_id=o.tenant_id AND p.policy_id=o.aggregate_id AND p.revision=o.aggregate_revision
 JOIN channel_accounts a ON a.tenant_id=p.tenant_id AND a.id=p.account_id
 WHERE a.scope_id=$1 AND o.event_type=$2 AND o.aggregate_type='ChannelAccessPolicy'
 AND ((o.status='PENDING' AND o.available_at<=clock_timestamp()) OR (o.status='IN_FLIGHT' AND o.claimed_until<=clock_timestamp()))
 AND o.attempt_count<2147483647
 ORDER BY o.available_at,o.created_at,o.id LIMIT 1 FOR UPDATE OF o SKIP LOCKED
 ) UPDATE control_outbox o SET status='IN_FLIGHT',attempt_count=o.attempt_count+1,claimed_by=$3,claimed_until=clock_timestamp()+interval '15 seconds',updated_at=clock_timestamp()
 FROM next WHERE o.tenant_id=next.tenant_id AND o.id=next.id
 RETURNING o.tenant_id,o.id,o.aggregate_id,next.account_id,o.aggregate_revision,o.schema_version,o.payload_jsonb,o.payload_digest,o.attempt_count`, s.options.ScopeID, channelv1.AccessPolicyPublishedEvent, claim.ClaimToken).Scan(&claim.TenantID, &claim.EventID, &claim.PolicyID, &claim.AccountID, &claim.Revision, &claim.SchemaVersion, &claim.Payload, &claim.PayloadDigest, &claim.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.AccessPolicyClaim{}, false, nil
	}
	if err != nil {
		return application.AccessPolicyClaim{}, false, dbError(err)
	}
	return claim, true, nil
}
func (s *Store) FinishAccessPolicy(ctx context.Context, claim application.AccessPolicyClaim, published bool, code string, retry time.Duration) error {
	status := "PENDING"
	var errorCode any = code
	if published {
		if code != "" || retry != 0 {
			return integrity()
		}
		status = "PUBLISHED"
		errorCode = nil
	} else if code == application.PolicyOutboxIntegrity && retry == 0 {
		status = "FAILED"
	} else if code != application.PolicyPublishUnavailable || retry <= 0 || retry > time.Minute {
		return integrity()
	}
	tag, err := s.db.Exec(ctx, `UPDATE control_outbox SET status=$5,claimed_by=NULL,claimed_until=NULL,last_error=$6,
 published_at=CASE WHEN $5='PUBLISHED' THEN clock_timestamp() ELSE NULL END,
 available_at=CASE WHEN $5='PENDING' THEN clock_timestamp()+($7::bigint*interval '1 millisecond') ELSE available_at END,updated_at=clock_timestamp()
 WHERE tenant_id=$1 AND id=$2 AND claimed_by=$3 AND attempt_count=$4 AND status='IN_FLIGHT' AND claimed_until>clock_timestamp() AND event_type=$8 AND aggregate_type='ChannelAccessPolicy' AND aggregate_id=$9 AND aggregate_revision=$10`, claim.TenantID, claim.EventID, claim.ClaimToken, claim.Attempt, status, errorCode, retry.Milliseconds(), channelv1.AccessPolicyPublishedEvent, claim.PolicyID, claim.Revision)
	if err != nil {
		return dbError(err)
	}
	if tag.RowsAffected() != 1 {
		return application.ErrOutboxLeaseLost
	}
	return nil
}

var _ application.AccessPolicyOutbox = (*Store)(nil)
