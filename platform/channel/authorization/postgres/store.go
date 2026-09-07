// Package authorizationpostgres stages and atomically installs complete current
// authorization source state. It does not itself decide Admission eligibility.
package authorizationpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/liuzengh/trpc-agent-service/api/schemas/channel/v1"
	domain "github.com/liuzengh/trpc-agent-service/platform/channel/authorization"
)

var ErrInvalid = errors.New("AUTHORIZATION_INSTALL_INVALID")
var ErrUnavailable = errors.New("AUTHORIZATION_INSTALL_UNAVAILABLE")
var ErrIntegrity = errors.New("AUTHORIZATION_INSTALL_INTEGRITY")
var ErrBlocked = errors.New("AUTHORIZATION_INSTALL_BLOCKED")
var ErrExpired = errors.New("AUTHORIZATION_INSTALL_EXPIRED")
var ErrSuperseded = errors.New("AUTHORIZATION_INSTALL_SUPERSEDED")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var epochPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Namespace is restricted to compiled owner table families.
type Namespace string

const (
	Gateway Namespace = "gateway"
	Worker  Namespace = "worker"
)

type Store struct {
	namespace    Namespace
	pool         *pgxpool.Pool
	scope, epoch string
}

func New(pool *pgxpool.Pool, scope, epoch string, namespace Namespace) (*Store, error) {
	if (namespace != Gateway && namespace != Worker) || pool == nil || !idPattern.MatchString(scope) || !epochPattern.MatchString(epoch) {
		return nil, ErrInvalid
	}
	return &Store{pool: pool, scope: scope, epoch: epoch, namespace: namespace}, nil
}
func (s *Store) query(sql string) string {
	return strings.ReplaceAll(sql, "gateway_authorization_", string(s.namespace)+"_authorization_")
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// Refresh captures PostgreSQL time BEFORE invoking the network reader. It stages
// privately in this transaction, holding no active head lock during HTTP I/O.
// Only a complete verified set reaches the short account-local install section.
func (s *Store) Refresh(ctx context.Context, target domain.AuthorizationTarget, reader domain.AuthorizationReader) error {
	if ctx == nil || reader == nil || !idPattern.MatchString(target.TenantID) || !idPattern.MatchString(target.AccountID) || (target.Provider != "telegram" && target.Provider != "wecom") {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ErrUnavailable
	}
	defer rollback(tx)
	var anchor time.Time
	var stageID pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp(),gen_random_uuid()`).Scan(&anchor, &stageID); err != nil {
		return ErrUnavailable
	}
	digest := wire.NewPrincipalSetDigest()
	var identity wire.AuthorizationSnapshotIdentity
	cursor := ""
	staged, complete, failed := false, false, false
	result, err := reader.ReadAuthorization(ctx, target, func(call context.Context, page wire.AuthorizationSnapshotPage) error {
		if failed || complete {
			failed = true
			return ErrIntegrity
		}
		failed = true
		raw, _ := json.Marshal(page)
		if _, e := wire.DecodeAuthorizationSnapshotPage(raw); e != nil {
			return ErrIntegrity
		}
		id := page.AuthorizationSnapshotIdentity
		if id.ScopeID != s.scope || id.SourceEpoch != s.epoch || id.TenantID != target.TenantID || id.AccountID != target.AccountID || id.Provider != target.Provider || page.AfterPrincipalID != cursor || staged && id != identity {
			return ErrIntegrity
		}
		rows := make([][]any, 0, len(page.Principals))
		for _, p := range page.Principals {
			if digest.Add(p) != nil {
				return ErrIntegrity
			}
			rows = append(rows, []any{stageID, p.PrincipalID, p.ExternalUserID, p.State, p.Revision})
		}
		n, e := tx.CopyFrom(call, pgx.Identifier{string(s.namespace) + "_authorization_staging"}, []string{"stage_id", "principal_id", "external_user_id", "state", "revision"}, pgx.CopyFromRows(rows))
		if e != nil || n != int64(len(rows)) {
			return ErrIntegrity
		}
		staged = true
		identity = id
		cursor = page.NextPrincipalID
		complete = page.Complete
		failed = false
		return nil
	})
	if err != nil {
		return err
	}
	if failed || !staged || !complete {
		return ErrIntegrity
	}
	m := result.Manifest
	raw, _ := json.Marshal(m)
	if _, err = wire.DecodeAuthorizationSnapshotManifest(raw); err != nil || m.AuthorizationSnapshotIdentity != identity {
		return ErrIntegrity
	}
	count, root := digest.Result()
	if m.PrincipalCount != count || m.PrincipalDigest != root {
		return ErrIntegrity
	}
	if err = verifyRows(ctx, tx, s.query(`SELECT principal_id,external_user_id,state,revision FROM gateway_authorization_staging WHERE stage_id=$1 ORDER BY principal_id COLLATE "C"`), []any{stageID}, count, root); err != nil {
		return err
	}
	policyRaw, _ := json.Marshal(wire.AccessPolicyResolveResponse{SchemaVersion: 1, ScopeID: s.scope, SourceEpoch: s.epoch, Policy: result.Policy})
	decoded, e := wire.DecodeAccessPolicyResolveResponse(policyRaw)
	if e != nil {
		return ErrIntegrity
	}
	p := decoded.Policy
	if p.TenantID != target.TenantID || p.AccountID != target.AccountID || p.Provider != target.Provider || p.PolicyID != m.Policy.ID || p.Revision != m.Policy.Revision || p.Digest != m.Policy.Digest || p.Body.AuthorizationMaxAgeMS != m.AuthorizationMaxAgeMS {
		return ErrIntegrity
	}
	var sessionJSON, quotaJSON []byte
	if result.Dependencies != nil {
		if p.Body.AccessMode == "DENY_ALL" {
			return ErrIntegrity
		}
		pair, e := domain.MatchPolicyDependencies(p, result.Dependencies.Session, result.Dependencies.Quota)
		if e != nil {
			return ErrIntegrity
		}
		sessionJSON, _ = json.Marshal(pair.Session)
		quotaJSON, _ = json.Marshal(pair.Quota)
	}
	// Lock only after staging, so a slow fetch cannot monopolize the hot-path head.
	_, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, string(s.namespace)+"-auth/"+s.scope+"/"+target.AccountID)
	if err != nil {
		return ErrUnavailable
	}
	var old wire.AuthorizationSnapshotManifest
	var blocked string
	var previousAnchor time.Time
	var oldPolicy []byte
	err = tx.QueryRow(ctx, s.query(`SELECT source_epoch,tenant_id,provider,generation,account_revision,account_enabled,policy_id,policy_revision,policy_digest,principal_count,principal_digest,read_started_at,blocked_reason,policy_jsonb FROM gateway_authorization_snapshots WHERE scope_id=$1 AND account_id=$2 FOR UPDATE`), s.scope, target.AccountID).Scan(&old.SourceEpoch, &old.TenantID, &old.Provider, &old.Generation, &old.AccountRevision, &old.AccountEnabled, &old.Policy.ID, &old.Policy.Revision, &old.Policy.Digest, &old.PrincipalCount, &old.PrincipalDigest, &previousAnchor, &blocked, &oldPolicy)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ErrUnavailable
	}
	if err == nil {
		if blocked != "" {
			return ErrBlocked
		}
		// An older concurrently-started refresh must not overwrite or block a newer
		// installed observation. No source-state judgement is made from such a read.
		if anchor.Before(previousAnchor) {
			return ErrSuperseded
		}
		reason := ""
		if old.SourceEpoch != m.SourceEpoch || old.TenantID != m.TenantID || old.Provider != m.Provider {
			reason = "SOURCE_CHANGED"
		} else if m.Generation < old.Generation || m.AccountRevision < old.AccountRevision || m.Policy.ID != old.Policy.ID || m.Policy.Revision < old.Policy.Revision {
			reason = "SNAPSHOT_REGRESSION"
		} else if m.Policy.Revision == old.Policy.Revision && m.Policy.Digest != old.Policy.Digest {
			reason = "SNAPSHOT_CONFLICT"
		} else if m.Generation == old.Generation && (m.AccountRevision != old.AccountRevision || m.AccountEnabled != old.AccountEnabled || m.Policy != old.Policy || m.PrincipalCount != old.PrincipalCount || m.PrincipalDigest != old.PrincipalDigest) {
			reason = "SNAPSHOT_CONFLICT"
		}
		if reason != "" {
			if _, err = tx.Exec(ctx, s.query(`UPDATE gateway_authorization_snapshots SET blocked_reason=$3 WHERE scope_id=$1 AND account_id=$2`), s.scope, target.AccountID, reason); err != nil {
				return ErrUnavailable
			}
			if _, err = tx.Exec(ctx, s.query(`DELETE FROM gateway_authorization_staging WHERE stage_id=$1`), stageID); err != nil {
				return ErrUnavailable
			}
			if tx.Commit(ctx) != nil {
				return ErrUnavailable
			}
			return ErrBlocked
		}
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return ErrUnavailable
	}
	expires := anchor.Add(time.Duration(m.AuthorizationMaxAgeMS) * time.Millisecond)
	if !now.Before(expires) || now.Before(anchor) {
		return ErrExpired
	}
	policyJSON, _ := json.Marshal(p)
	if _, err = tx.Exec(ctx, s.query(`DELETE FROM gateway_authorization_principals WHERE scope_id=$1 AND account_id=$2`), s.scope, target.AccountID); err != nil {
		return ErrUnavailable
	}
	_, err = tx.Exec(ctx, s.query(`INSERT INTO gateway_authorization_snapshots(scope_id,account_id,source_epoch,tenant_id,provider,generation,account_revision,account_enabled,policy_id,policy_revision,policy_digest,policy_jsonb,principal_count,principal_digest,captured_at,read_started_at,fresh_until,session_policy_jsonb,quota_policy_jsonb) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19) ON CONFLICT(scope_id,account_id) DO UPDATE SET generation=EXCLUDED.generation,account_revision=EXCLUDED.account_revision,account_enabled=EXCLUDED.account_enabled,policy_revision=EXCLUDED.policy_revision,policy_digest=EXCLUDED.policy_digest,policy_jsonb=EXCLUDED.policy_jsonb,principal_count=EXCLUDED.principal_count,principal_digest=EXCLUDED.principal_digest,captured_at=EXCLUDED.captured_at,read_started_at=EXCLUDED.read_started_at,fresh_until=EXCLUDED.fresh_until,session_policy_jsonb=EXCLUDED.session_policy_jsonb,quota_policy_jsonb=EXCLUDED.quota_policy_jsonb`), s.scope, target.AccountID, s.epoch, target.TenantID, target.Provider, m.Generation, m.AccountRevision, m.AccountEnabled, p.PolicyID, p.Revision, p.Digest, policyJSON, count, root, m.CapturedAt, anchor, expires, sessionJSON, quotaJSON)
	if err != nil {
		return ErrUnavailable
	}
	// Read back actual JSONB: triggers and DB coercion are not proof of content.
	var storedSession, storedQuota []byte
	if err = tx.QueryRow(ctx, s.query(`SELECT session_policy_jsonb,quota_policy_jsonb FROM gateway_authorization_snapshots WHERE scope_id=$1 AND account_id=$2`), s.scope, target.AccountID).Scan(&storedSession, &storedQuota); err != nil {
		return ErrUnavailable
	}
	if sessionJSON == nil {
		if storedSession != nil || storedQuota != nil {
			return ErrIntegrity
		}
	} else {
		sd, e := wire.DecodePolicyDefinitionDocument(storedSession)
		if e != nil {
			return ErrIntegrity
		}
		qd, e := wire.DecodePolicyDefinitionDocument(storedQuota)
		if e != nil {
			return ErrIntegrity
		}
		if _, e = domain.MatchPolicyDependencies(p, sd, qd); e != nil {
			return ErrIntegrity
		}
	}
	_, err = tx.Exec(ctx, s.query(`INSERT INTO gateway_authorization_principals(scope_id,account_id,generation,tenant_id,provider,principal_id,external_user_id,state,revision) SELECT $1,$2,$3,$4,$5,principal_id,external_user_id,state,revision FROM gateway_authorization_staging WHERE stage_id=$6`), s.scope, target.AccountID, m.Generation, target.TenantID, target.Provider, stageID)
	if err != nil {
		return ErrUnavailable
	}
	if err = verifyRows(ctx, tx, s.query(`SELECT principal_id,external_user_id,state,revision FROM gateway_authorization_principals WHERE scope_id=$1 AND account_id=$2 ORDER BY principal_id COLLATE "C"`), []any{s.scope, target.AccountID}, count, root); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, s.query(`DELETE FROM gateway_authorization_staging WHERE stage_id=$1`), stageID); err != nil {
		return ErrUnavailable
	}
	// The DB clock check also includes lock wait and bulk installation time. The
	// fixed deadline is never extended, even if commit/acknowledgement is delayed.
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return ErrUnavailable
	}
	if !now.Before(expires) || now.Before(anchor) {
		return ErrExpired
	}
	if err = tx.Commit(ctx); err != nil {
		return ErrUnavailable
	}
	return nil
}

// Recompute from actual stored rows, not just the callback input or CopyFrom
// row count. Trigger/copy corruption must roll back before any head is visible.
func verifyRows(ctx context.Context, tx pgx.Tx, query string, args []any, count int64, root string) error {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return ErrUnavailable
	}
	defer rows.Close()
	digest := wire.NewPrincipalSetDigest()
	for rows.Next() {
		var p wire.AuthorizationPrincipal
		if err = rows.Scan(&p.PrincipalID, &p.ExternalUserID, &p.State, &p.Revision); err != nil {
			return ErrUnavailable
		}
		if digest.Add(p) != nil {
			return ErrIntegrity
		}
	}
	if rows.Err() != nil {
		return ErrUnavailable
	}
	n, d := digest.Result()
	if n != count || d != root {
		return ErrIntegrity
	}
	return nil
}
