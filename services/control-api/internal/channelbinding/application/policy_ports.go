package application

import (
	"context"
	"errors"

	"github.com/liuzengh/trpc-agent-service/services/control-api/internal/channelbinding/domain"
)

var ErrPolicyRevisionConflict = errors.New("CHANNEL_POLICY_REVISION_CONFLICT")

// PolicyRevisionTransaction is the storage seam for the policy publisher, not
// an HTTP authorization surface. The application must resolve Session/Quota/tool
// owner references before Append. The adapter independently checks OWNER, scope,
// active listed principals, CAS and the atomic revision/head/outbox write.
type PolicyRevisionTransaction interface {
	// Publication commands can persist/replay their success receipt inside this
	// same owner-authorized transaction, rather than after outbox commit.
	FindReceipt(context.Context, ReceiptKey) (Receipt, bool, error)
	SaveReceipt(context.Context, Receipt) error
	LoadAccount(context.Context, string) (Aggregate, error)
	LoadAccessPolicy(context.Context, string) (domain.ChannelAccessPolicyRevision, bool, error)
	AppendAccessPolicy(context.Context, domain.ChannelAccessPolicyRevision, int64, string, string) error
}
type PolicyRevisionStore interface {
	WithPolicyWrite(context.Context, WriteScope, func(PolicyRevisionTransaction) error) error
}
