package bootstrap

import (
	"context"
	"errors"

	identityapp "github.com/liuzengh/trpc-agent-service/services/control-api/internal/identity/application"
)

type activeAccountLookup struct {
	accounts *identityapp.AccountManagement
}

func (lookup activeAccountLookup) IsActiveAccount(ctx context.Context, userID string) (bool, error) {
	account, err := lookup.accounts.GetAccount(ctx, userID)
	if errors.Is(err, identityapp.ErrAccountNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return account.CanLogin(), nil
}
