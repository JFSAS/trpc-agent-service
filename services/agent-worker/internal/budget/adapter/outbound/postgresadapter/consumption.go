package postgresadapter

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/liuzengh/trpc-agent-service/services/agent-worker/internal/budget/domain"
)

// ConsumptionAuthority verifies current policy and the enforceable maximum for
// the exact requested operation using owner facts in this transaction. No network
// I/O or caller-provided default cap is allowed. Production wiring is mandatory.
type ConsumptionAuthority interface {
	AuthorizeConsumption(context.Context, pgx.Tx, domain.ConsumptionRequest) (domain.ConsumptionGrant, error)
}

// ConsumptionReader proves final usage, not terminal Run status or a timeout.
// Its owner must bind the report to the exact operation input and bound digest.
type ConsumptionReader interface {
	ReadConsumption(context.Context, pgx.Tx, domain.ConsumptionRequest) (domain.ConsumptionProof, error)
}
type Consumption struct {
	*ConsumptionSettler
	maxRetained int64
	authority   ConsumptionAuthority
}

func NewConsumption(pool *pgxpool.Pool, maxRetained int64, authority ConsumptionAuthority, reader ConsumptionReader) (*Consumption, error) {
	if pool == nil || maxRetained < 1 || maxRetained > domain.MaxAmount || authority == nil || reader == nil {
		return nil, domain.ErrInvalid
	}
	return &Consumption{ConsumptionSettler: &ConsumptionSettler{pool: pool, reader: reader}, maxRetained: maxRetained, authority: authority}, nil
}

func (c *Consumption) Reserve(ctx context.Context, r domain.ConsumptionRequest) (domain.ConsumptionReservation, error) {
	var zero domain.ConsumptionReservation
	if ctx == nil {
		return zero, domain.ErrInvalid
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer rollback(tx)
	out, err := c.ReserveInTransaction(ctx, tx, r)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return out, nil
}
func consumptionLock(ctx context.Context, tx pgx.Tx, r domain.ConsumptionRequest) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004291))`, r.OperationID)
	return err
}
func consumptionUnitLock(ctx context.Context, tx pgx.Tx, r domain.ConsumptionRequest) error {
	// JSON framing avoids tenant/unit concatenation collisions.
	key, _ := json.Marshal([]string{r.TenantID, string(r.Unit)})
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,731004292))`, string(key))
	return err
}
func readConsumptionReservation(ctx context.Context, tx pgx.Tx, operation string) (domain.ConsumptionReservation, error) {
	var out domain.ConsumptionReservation
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT tenant_id,run_id,operation_id,input_digest,bound_digest,unit,maximum,fingerprint,grant_jsonb,reserved_at FROM worker_consumption_reservations WHERE operation_id=$1`, operation).Scan(&out.Request.TenantID, &out.Request.RunID, &out.Request.OperationID, &out.Request.InputDigest, &out.Request.BoundDigest, &out.Request.Unit, &out.Request.Maximum, &out.Fingerprint, &raw, &out.ReservedAt)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.Grant); err != nil {
		return out, err
	}
	fingerprint, err := out.Request.Fingerprint()
	if err != nil || out.Grant.Validate() != nil || out.Grant.Fingerprint != fingerprint || out.Fingerprint != fingerprint {
		return out, domain.ErrConflict
	}
	return out, nil
}

// ReserveInTransaction leaves outer commit to the owner. Exact replay is a durable
// reservation receipt, NOT current permission to issue a physical external call.
// New physical calls must use new operation IDs and pass their current gates.
// Locks: operation -> tenant/unit -> retained capacity -> authority-owner proof.
func (c *Consumption) ReserveInTransaction(ctx context.Context, tx pgx.Tx, r domain.ConsumptionRequest) (domain.ConsumptionReservation, error) {
	var zero domain.ConsumptionReservation
	if ctx == nil || tx == nil || r.Validate() != nil {
		return zero, domain.ErrInvalid
	}
	fingerprint, _ := r.Fingerprint()
	save, err := tx.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer rollback(save)
	if err = consumptionLock(ctx, save, r); err != nil {
		return zero, err
	}
	old, err := readConsumptionReservation(ctx, save, r.OperationID)
	if err == nil {
		if old.Fingerprint != fingerprint {
			return zero, domain.ErrConflict
		}
		if err = save.Commit(ctx); err != nil {
			return zero, err
		}
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if err = consumptionUnitLock(ctx, save, r); err != nil {
		return zero, err
	}
	if _, err = save.Exec(ctx, `SELECT pg_advisory_xact_lock(731004293)`); err != nil {
		return zero, err
	}
	grant, err := c.authority.AuthorizeConsumption(ctx, save, r)
	if err != nil {
		return zero, err
	}
	if grant.Validate() != nil || grant.Fingerprint != fingerprint {
		return zero, domain.ErrNotReady
	}
	if !grant.Enabled || grant.Cap < r.Maximum {
		return zero, domain.ErrConsumption
	}
	// NUMERIC aggregation stays in PostgreSQL, avoiding int64 overflow if retained
	// usage exceeds the maximum individual amount. Unknown usage holds its maximum.
	var fits, violated bool
	if err = save.QueryRow(ctx, `SELECT COALESCE(sum(COALESCE(s.actual,r.maximum)),0)+$3::numeric<=$4::numeric, COALESCE(bool_or(s.bound_violated),false) FROM worker_consumption_reservations r LEFT JOIN worker_consumption_settlements s USING(operation_id) WHERE r.tenant_id=$1 AND r.unit=$2`, r.TenantID, r.Unit, r.Maximum, grant.Cap).Scan(&fits, &violated); err != nil {
		return zero, err
	}
	if violated {
		return zero, domain.ErrBoundViolation
	}
	if !fits {
		return zero, domain.ErrConsumption
	}
	var retained int64
	if err = save.QueryRow(ctx, `SELECT count(*) FROM worker_consumption_reservations`).Scan(&retained); err != nil {
		return zero, err
	}
	if retained >= c.maxRetained {
		return zero, domain.ErrCapacity
	}
	raw, err := json.Marshal(grant)
	if err != nil {
		return zero, err
	}
	out := domain.ConsumptionReservation{Request: r, Fingerprint: fingerprint, Grant: grant}
	if err = save.QueryRow(ctx, `INSERT INTO worker_consumption_reservations(operation_id,tenant_id,run_id,input_digest,bound_digest,unit,maximum,fingerprint,grant_jsonb) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING reserved_at`, r.OperationID, r.TenantID, r.RunID, r.InputDigest, r.BoundDigest, r.Unit, r.Maximum, fingerprint, raw).Scan(&out.ReservedAt); err != nil {
		return zero, err
	}
	if err = save.Commit(ctx); err != nil {
		return zero, err
	}
	return out, nil
}

func readConsumptionSettlement(ctx context.Context, tx pgx.Tx, operation string) (domain.ConsumptionSettlement, error) {
	var out domain.ConsumptionSettlement
	err := tx.QueryRow(ctx, `SELECT tenant_id,operation_id,fingerprint,unit,maximum,actual,evidence_id,evidence_digest,bound_violated,settled_at FROM worker_consumption_settlements WHERE operation_id=$1`, operation).Scan(&out.TenantID, &out.OperationID, &out.Fingerprint, &out.Unit, &out.Maximum, &out.Actual, &out.EvidenceID, &out.EvidenceDigest, &out.BoundViolated, &out.SettledAt)
	return out, err
}
func (c *ConsumptionSettler) Settle(ctx context.Context, r domain.ConsumptionRequest) (domain.ConsumptionSettlement, error) {
	var zero domain.ConsumptionSettlement
	if ctx == nil {
		return zero, domain.ErrInvalid
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer rollback(tx)
	out, err := c.SettleInTransaction(ctx, tx, r)
	if err != nil {
		return zero, err
	}
	if err = tx.Commit(ctx); err != nil {
		return zero, err
	}
	return out, nil
}

// SettleInTransaction never refunds from time or caller-supplied usage. Actual
// overrun is recorded, not rejected/rolled back: BoundViolated blocks subsequent
// reservations for the same tenant/unit until a future explicit recovery protocol.
func (c *ConsumptionSettler) SettleInTransaction(ctx context.Context, tx pgx.Tx, r domain.ConsumptionRequest) (domain.ConsumptionSettlement, error) {
	var zero domain.ConsumptionSettlement
	if ctx == nil || tx == nil || r.Validate() != nil {
		return zero, domain.ErrInvalid
	}
	fingerprint, _ := r.Fingerprint()
	save, err := tx.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer rollback(save)
	if err = consumptionLock(ctx, save, r); err != nil {
		return zero, err
	}
	reservation, err := readConsumptionReservation(ctx, save, r.OperationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return zero, domain.ErrNotReady
	}
	if err != nil {
		return zero, err
	}
	if reservation.Fingerprint != fingerprint {
		return zero, domain.ErrConflict
	}
	old, err := readConsumptionSettlement(ctx, save, r.OperationID)
	if err == nil {
		if old.Fingerprint != fingerprint {
			return zero, domain.ErrConflict
		}
		if err = save.Commit(ctx); err != nil {
			return zero, err
		}
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return zero, err
	}
	if err = consumptionUnitLock(ctx, save, r); err != nil {
		return zero, err
	}
	proof, err := c.reader.ReadConsumption(ctx, save, r)
	if err != nil {
		return zero, err
	}
	if proof.Validate() != nil || proof.Fingerprint != fingerprint {
		return zero, domain.ErrNotReady
	}
	out, err := readInsertedConsumptionSettlement(ctx, save, r, fingerprint, proof)
	if err != nil {
		return zero, err
	}
	if err = save.Commit(ctx); err != nil {
		return zero, err
	}
	return out, nil
}
func readInsertedConsumptionSettlement(ctx context.Context, tx pgx.Tx, r domain.ConsumptionRequest, fingerprint string, proof domain.ConsumptionProof) (domain.ConsumptionSettlement, error) {
	var zero domain.ConsumptionSettlement
	_, err := tx.Exec(ctx, `INSERT INTO worker_consumption_settlements(operation_id,tenant_id,fingerprint,unit,maximum,actual,evidence_id,evidence_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.OperationID, r.TenantID, fingerprint, r.Unit, r.Maximum, proof.Amount, proof.EvidenceID, proof.EvidenceDigest)
	if err != nil {
		var sql *pgconn.PgError
		if errors.As(err, &sql) && sql.Code == "23505" {
			return zero, domain.ErrConflict
		}
		return zero, err
	}
	return readConsumptionSettlement(ctx, tx, r.OperationID)
}

// VerifyUnsettledReservation is an owner port for atomic Execution linkage.
// Call before acquiring Execution locks. It proves an exact existing reservation,
// not current authorization or permission to repeat a physical external call.
func (c *Consumption) VerifyUnsettledReservation(ctx context.Context, tx pgx.Tx, r domain.ConsumptionRequest) error {
	if ctx == nil || tx == nil || r.Validate() != nil {
		return domain.ErrInvalid
	}
	if err := consumptionLock(ctx, tx, r); err != nil {
		return err
	}
	old, err := readConsumptionReservation(ctx, tx, r.OperationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotReady
	}
	if err != nil {
		return err
	}
	fingerprint, _ := r.Fingerprint()
	if old.Fingerprint != fingerprint {
		return domain.ErrConflict
	}
	var settled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_consumption_settlements WHERE operation_id=$1)`, r.OperationID).Scan(&settled); err != nil {
		return err
	}
	if settled {
		return domain.ErrNotReady
	}
	return nil
}
