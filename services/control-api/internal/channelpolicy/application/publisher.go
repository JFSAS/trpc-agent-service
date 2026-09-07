package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/gowebpki/jcs"
	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelpolicy/domain"
)

var (
	ErrPermissionDenied    = errors.New("CHANNEL_POLICY_DEFINITION_PERMISSION_DENIED")
	ErrRevisionConflict    = errors.New("CHANNEL_POLICY_DEFINITION_REVISION_CONFLICT")
	ErrIdempotencyConflict = errors.New("CHANNEL_POLICY_DEFINITION_IDEMPOTENCY_CONFLICT")
)

type Actor struct {
	TenantID string
	UserID   string
}
type TenantAccess interface {
	IsActiveOwner(context.Context, string, string) (bool, error)
}
type RequestSigner interface {
	SignRequest(context.Context, []byte) (string, string, error)
	VerifyRequest(context.Context, string, string, []byte) (bool, error)
}
type WriteScope struct {
	Actor    Actor
	Kind     domain.Kind
	PolicyID string
}
type Receipt struct {
	KeyHash, MACKeyID, RequestMAC string
	Result                        json.RawMessage
	CreatedBy                     string
	CreatedAt                     time.Time
}
type PublishTransaction interface {
	FindReceipt(context.Context, string) (Receipt, bool, error)
	LatestRevision(context.Context) (int64, error)
	Append(context.Context, domain.Revision, string, string) error
	SaveReceipt(context.Context, Receipt) error
}
type PublicationStore interface {
	WithPublish(context.Context, WriteScope, func(PublishTransaction) error) error
}
type PublishInput struct {
	ExpectedRevision int64             `json:"expected_revision"`
	Definition       domain.Definition `json:"definition"`
}
type PublishResult struct {
	TenantID     string      `json:"tenant_id"`
	Kind         domain.Kind `json:"kind"`
	PolicyID     string      `json:"policy_id"`
	Revision     int64       `json:"revision"`
	Digest       string      `json:"digest"`
	EventID      string      `json:"event_id"`
	AuditEventID string      `json:"audit_event_id"`
	Distribution string      `json:"distribution"`
}
type PublisherDependencies struct {
	Store  PublicationStore
	Access TenantAccess
	Signer RequestSigner
	NewID  func(string) (string, error)
	Now    func() time.Time
}
type Publisher struct{ deps PublisherDependencies }

func NewPublisher(deps PublisherDependencies) (*Publisher, error) {
	if deps.Store == nil || deps.Access == nil || deps.Signer == nil || deps.NewID == nil {
		return nil, ErrUnavailable
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Publisher{deps: deps}, nil
}
func (s *Publisher) Publish(ctx context.Context, actor Actor, kind domain.Kind, id, key string, in PublishInput) (PublishResult, error) {
	if err := s.AuthorizeWrite(ctx, actor); err != nil {
		return PublishResult{}, err
	}
	if !validKey(key) || in.ExpectedRevision < 0 || in.ExpectedRevision >= domain.MaxRevision {
		return PublishResult{}, domain.ErrInvalid
	}
	candidate, err := domain.NewRevision(actor.TenantID, id, actor.UserID, kind, in.ExpectedRevision+1, in.Definition, s.deps.Now())
	if err != nil {
		return PublishResult{}, err
	}
	// Bind the detached definition, not the caller's mutable pointers.
	in.Definition = candidate.Definition
	raw, err := json.Marshal(struct {
		Operation string
		Actor     Actor
		Kind      domain.Kind
		PolicyID  string
		Input     PublishInput
	}{"PublishChannelPolicyDefinition", actor, kind, id, in})
	if err != nil {
		return PublishResult{}, ErrIntegrity
	}
	canonical, err := jcs.Transform(raw)
	clear(raw)
	if err != nil {
		return PublishResult{}, ErrIntegrity
	}
	defer clear(canonical)
	sum := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(sum[:])
	var output PublishResult
	err = s.deps.Store.WithPublish(ctx, WriteScope{Actor: actor, Kind: kind, PolicyID: id}, func(tx PublishTransaction) error {
		receipt, found, err := tx.FindReceipt(ctx, keyHash)
		if err != nil {
			return err
		}
		if found {
			match, err := s.deps.Signer.VerifyRequest(ctx, receipt.MACKeyID, receipt.RequestMAC, canonical)
			if err != nil {
				return ErrUnavailable
			}
			if !match {
				return ErrIdempotencyConflict
			}
			if receipt.KeyHash != keyHash || receipt.CreatedBy != actor.UserID || json.Unmarshal(receipt.Result, &output) != nil || !output.ValidFor(actor.TenantID, kind, id, in.ExpectedRevision+1) {
				return ErrIntegrity
			}
			return nil
		}
		latest, err := tx.LatestRevision(ctx)
		if err != nil {
			return err
		}
		if latest != in.ExpectedRevision {
			return ErrRevisionConflict
		}
		eventID, err := s.deps.NewID("evt")
		if err != nil {
			return ErrUnavailable
		}
		auditID, err := s.deps.NewID("aud")
		if err != nil {
			return ErrUnavailable
		}
		if err = tx.Append(ctx, candidate, eventID, auditID); err != nil {
			return err
		}
		output = PublishResult{TenantID: actor.TenantID, Kind: kind, PolicyID: id, Revision: candidate.Revision, Digest: candidate.Digest, EventID: eventID, AuditEventID: auditID, Distribution: "PENDING"}
		if !output.ValidFor(actor.TenantID, kind, id, candidate.Revision) {
			return ErrIntegrity
		}
		result, err := json.Marshal(output)
		if err != nil {
			return ErrIntegrity
		}
		macKey, mac, err := s.deps.Signer.SignRequest(ctx, canonical)
		if err != nil {
			return ErrUnavailable
		}
		return tx.SaveReceipt(ctx, Receipt{KeyHash: keyHash, MACKeyID: macKey, RequestMAC: mac, Result: result, CreatedBy: actor.UserID, CreatedAt: s.deps.Now()})
	})
	if err != nil {
		return PublishResult{}, err
	}
	return output, nil
}
func (r PublishResult) ValidFor(tenant string, kind domain.Kind, id string, revision int64) bool {
	if !domain.ValidID(r.TenantID) || !domain.ValidID(r.PolicyID) || !domain.ValidRevision(r.Revision) || (r.Kind != domain.Session && r.Kind != domain.Quota) || r.TenantID != tenant || r.Kind != kind || r.PolicyID != id || r.Revision != revision || r.Distribution != "PENDING" || !domain.ValidID(r.EventID) || !domain.ValidID(r.AuditEventID) || r.EventID == r.AuditEventID {
		return false
	}
	if len(r.Digest) != 71 || r.Digest[:7] != "sha256:" {
		return false
	}
	for _, c := range r.Digest[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validKey(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

// AuthorizeWrite precedes transport parsing; storage rechecks OWNER at commit.
func (s *Publisher) AuthorizeWrite(ctx context.Context, actor Actor) error {
	if !domain.ValidID(actor.TenantID) || !domain.ValidID(actor.UserID) {
		return ErrPermissionDenied
	}
	allowed, err := s.deps.Access.IsActiveOwner(ctx, actor.TenantID, actor.UserID)
	if err != nil {
		return ErrUnavailable
	}
	if !allowed {
		return ErrPermissionDenied
	}
	return nil
}
