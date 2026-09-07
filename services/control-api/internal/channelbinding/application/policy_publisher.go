package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

var ErrPolicyReferenceDenied = errors.New("CHANNEL_POLICY_REFERENCE_DENIED")

// PublishedPolicyValidator is an injected trusted owner adapter, not client
// attestation. It must resolve the exact tenant-owned Session and Quota revisions
// and enforce PUBLIC_LIMITED isolation, low quotas and exclusion of sensitive
// tools. Missing/unknown owner state is an error, never approval. The publisher
// deliberately has no default permissive implementation.
type PublishedPolicyValidator interface {
	ValidatePublishedPolicy(context.Context, Actor, domain.Account, domain.AccessPolicyBody) error
}
type PolicyPublisherDependencies struct {
	Store      PolicyRevisionStore
	Access     TenantAccess
	Cipher     CredentialCipher
	References PublishedPolicyValidator
	ScopeID    string
	NewID      func(string) (string, error)
	Now        func() time.Time
}
type PolicyPublisher struct{ deps PolicyPublisherDependencies }

func NewPolicyPublisher(deps PolicyPublisherDependencies) (*PolicyPublisher, error) {
	if deps.Store == nil || deps.Access == nil || deps.Cipher == nil || deps.References == nil || deps.NewID == nil || !domain.ValidID(deps.ScopeID) {
		return nil, ErrDependencyUnavailable
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &PolicyPublisher{deps: deps}, nil
}

type PublishAccessPolicyInput struct {
	ExpectedRevision int64                   `json:"expected_policy_revision"`
	Body             domain.AccessPolicyBody `json:"body"`
}

// PolicyPublishResult is a compact receipt. The full allowlist stays in owner
// storage, not the command receipt or notification event.
type PolicyPublishResult struct {
	TenantID     string `json:"tenant_id"`
	AccountID    string `json:"account_id"`
	PolicyID     string `json:"policy_id"`
	Revision     int64  `json:"revision"`
	Digest       string `json:"digest"`
	EventID      string `json:"event_id"`
	AuditEventID string `json:"audit_event_id"`
	Distribution string `json:"distribution"`
}

func (s *PolicyPublisher) Publish(ctx context.Context, actor Actor, accountID, key string, in PublishAccessPolicyInput) (PolicyPublishResult, error) {
	if err := authorizeChannelActor(ctx, s.deps.Access, actor, true); err != nil {
		return PolicyPublishResult{}, err
	}
	if !domain.ValidID(accountID) {
		return PolicyPublishResult{}, invalid("/account_id")
	}
	if in.ExpectedRevision == domain.MaxVersion {
		return PolicyPublishResult{}, &domain.Error{Code: domain.VersionExhausted}
	}
	if in.ExpectedRevision < 0 || in.ExpectedRevision > domain.MaxVersion {
		return PolicyPublishResult{}, invalid("/expected_policy_revision")
	}
	if !validKey(key) {
		return PolicyPublishResult{}, invalid("/Idempotency-Key")
	}
	body, err := domain.NormalizeAccessPolicyBody(in.Body)
	if err != nil {
		return PolicyPublishResult{}, err
	}
	in.Body = body
	const operation = "PublishChannelAccessPolicy"
	canonical, _, err := domain.CanonicalJSON(struct {
		Operation string                   `json:"operation"`
		Actor     Actor                    `json:"actor"`
		ScopeID   string                   `json:"scope_id"`
		AccountID string                   `json:"account_id"`
		Input     PublishAccessPolicyInput `json:"input"`
	}{operation, actor, s.deps.ScopeID, accountID, in})
	if err != nil {
		return PolicyPublishResult{}, err
	}
	defer clear(canonical)
	keyHash := sha256.Sum256([]byte(key))
	receiptKey := ReceiptKey{TenantID: actor.TenantID, Operation: operation, ScopeID: accountID, KeyHash: hex.EncodeToString(keyHash[:])}
	var output PolicyPublishResult
	err = s.deps.Store.WithPolicyWrite(ctx, WriteScope{ScopeID: s.deps.ScopeID, Actor: actor}, func(tx PolicyRevisionTransaction) error {
		receipt, found, err := tx.FindReceipt(ctx, receiptKey)
		if err != nil {
			return err
		}
		if found {
			match, err := s.deps.Cipher.VerifyRequest(ctx, receipt.MACKeyID, receipt.RequestMAC, canonical)
			if err != nil {
				return ErrDependencyUnavailable
			}
			if !match {
				return ErrIdempotencyConflict
			}
			if receipt.CreatedBy != actor.UserID || receipt.Key != receiptKey || json.Unmarshal(receipt.Result, &output) != nil || !output.valid(actor.TenantID, accountID, in.ExpectedRevision+1) {
				return &domain.Error{Code: domain.SourceIntegrity}
			}
			return nil // Do not republish or resolve current dependencies on a replay.
		}
		account, err := tx.LoadAccount(ctx, accountID)
		if err != nil {
			return err
		}
		if account.Account.TenantID != actor.TenantID || account.Account.ID != accountID || account.Account.ScopeID != s.deps.ScopeID {
			return &domain.Error{Code: domain.SourceIntegrity}
		}
		if err = account.Account.Validate(); err != nil {
			return err
		}
		old, exists, err := tx.LoadAccessPolicy(ctx, accountID)
		if err != nil {
			return err
		}
		if exists && old.Revision != in.ExpectedRevision || !exists && in.ExpectedRevision != 0 {
			return ErrPolicyRevisionConflict
		}
		if body.AccessMode != domain.AccessDenyAll {
			// Owner adapters receive a detached copy, not the MAC-bound input.
			validationBody, err := domain.NormalizeAccessPolicyBody(body)
			if err != nil {
				return err
			}
			if err = s.deps.References.ValidatePublishedPolicy(ctx, actor, account.Account, validationBody); err != nil {
				if errors.Is(err, ErrPolicyReferenceDenied) {
					return ErrPolicyReferenceDenied
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return ErrDependencyUnavailable
			}
		}
		policyID := old.PolicyID
		if !exists {
			policyID, err = s.deps.NewID("cap")
			if err != nil {
				return ErrDependencyUnavailable
			}
		}
		eventID, err := s.deps.NewID("evt")
		if err != nil {
			return ErrDependencyUnavailable
		}
		auditID, err := s.deps.NewID("aud")
		if err != nil {
			return ErrDependencyUnavailable
		}
		candidate, err := domain.PrepareAccessPolicyRevision(account.Account, policyID, in.ExpectedRevision+1, actor.UserID, body, s.deps.Now())
		if err != nil {
			return err
		}
		if err = tx.AppendAccessPolicy(ctx, candidate, in.ExpectedRevision, eventID, auditID); err != nil {
			return err
		}
		output = PolicyPublishResult{TenantID: actor.TenantID, AccountID: accountID, PolicyID: policyID, Revision: candidate.Revision, Digest: candidate.Digest, EventID: eventID, AuditEventID: auditID, Distribution: "PENDING"}
		if !output.valid(actor.TenantID, accountID, candidate.Revision) {
			return &domain.Error{Code: domain.SourceIntegrity}
		}
		raw, _, err := domain.CanonicalJSON(output)
		if err != nil {
			return err
		}
		macKey, mac, err := s.deps.Cipher.SignRequest(ctx, canonical)
		if err != nil {
			return ErrDependencyUnavailable
		}
		return tx.SaveReceipt(ctx, Receipt{Key: receiptKey, MACKeyID: macKey, RequestMAC: mac, Result: raw, CreatedBy: actor.UserID, CreatedAt: s.deps.Now()})
	})
	if err != nil {
		return PolicyPublishResult{}, err
	}
	return output, nil
}
func (r PolicyPublishResult) valid(tenant, account string, revision int64) bool {
	return r.TenantID == tenant && r.AccountID == account && domain.ValidID(r.PolicyID) && r.Revision == revision && domain.ValidDigest(r.Digest) && domain.ValidID(r.EventID) && domain.ValidID(r.AuditEventID) && r.EventID != r.AuditEventID && r.Distribution == "PENDING"
}
