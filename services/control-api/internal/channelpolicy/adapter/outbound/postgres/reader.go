package postgresadapter

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/application"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

type DB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type Reader struct{ db DB }

func NewReader(db DB) (*Reader, error) {
	if db == nil {
		return nil, application.ErrUnavailable
	}
	return &Reader{db: db}, nil
}
func (r *Reader) ReadExact(ctx context.Context, tenant string, kind domain.Kind, id string, revision int64) (domain.Revision, error) {
	if !domain.ValidID(tenant) || !domain.ValidID(id) || !domain.ValidRevision(revision) || (kind != domain.Session && kind != domain.Quota) {
		return domain.Revision{}, application.ErrNotFound
	}
	var raw []byte
	var digest string
	err := r.db.QueryRow(ctx, `SELECT document_jsonb,digest FROM channel_policy_definition_revisions WHERE tenant_id=$1 AND kind=$2 AND policy_id=$3 AND revision=$4`, tenant, kind, id, revision).Scan(&raw, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Revision{}, application.ErrNotFound
	}
	if err != nil {
		if ctx.Err() != nil {
			return domain.Revision{}, ctx.Err()
		}
		return domain.Revision{}, application.ErrUnavailable
	}
	doc, err := domain.Decode(raw)
	if err != nil || doc.TenantID != tenant || doc.PolicyID != id || doc.Kind != kind || doc.Revision != revision || doc.Digest != digest {
		return domain.Revision{}, application.ErrIntegrity
	}
	return doc, nil
}

var _ application.PublishedRevisionReader = (*Reader)(nil)
