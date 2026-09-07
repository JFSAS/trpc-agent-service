package postgresadapter

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	d "github.com/liuzengh/trpc-agent-service/services/channel-gateway/internal/delivery/domain"
)

func (s *Store) FindTransportReceipt(ctx context.Context, p d.TransportPosition) (d.TransportReceipt, bool, error) {
	if p.Validate() != nil {
		return d.TransportReceipt{}, false, d.ErrInvalid
	}
	r := d.TransportReceipt{Position: p}
	var digest string
	err := s.pool.QueryRow(ctx, `SELECT raw_digest,outcome,reason,intent_id,run_id FROM gateway_reply_transport_receipts WHERE stream_name=$1 AND stream_id=$2 AND stream_sequence=$3`, p.StreamName, p.StreamID, int64(p.Sequence)).Scan(&digest, &r.Outcome, &r.Reason, &r.IntentID, &r.RunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return d.TransportReceipt{}, false, nil
	}
	if err != nil {
		return d.TransportReceipt{}, false, databaseError(ctx, err)
	}
	if digest != p.RawDigest {
		return d.TransportReceipt{}, false, d.ErrConflict
	}
	return r, true, nil
}
func (s *Store) RecordTransportReceipt(ctx context.Context, r d.TransportReceipt) error {
	if r.Validate() != nil {
		return d.ErrInvalid
	}
	p := r.Position
	_, err := s.pool.Exec(ctx, `INSERT INTO gateway_reply_transport_receipts(stream_name,stream_id,stream_sequence,raw_digest,outcome,reason,intent_id,run_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, p.StreamName, p.StreamID, int64(p.Sequence), p.RawDigest, r.Outcome, r.Reason, r.IntentID, r.RunID)
	if err != nil {
		return databaseError(ctx, err)
	}
	old, found, err := s.FindTransportReceipt(ctx, p)
	if err != nil {
		return err
	}
	if !found {
		return d.ErrUnavailable
	}
	if old != r {
		return d.ErrConflict
	}
	return nil
}
