// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"

	"github.com/schotek/malachi/backend/pkg/api"
)

// The backend's own OAuth2 sign-in (account.oauthStart / oauthWait /
// oauthCancel). The contract is in place; the flow itself follows.

func (s *accountService) OAuthStart(context.Context, api.AccountOAuthStartParams) (*api.AccountOAuthStartResult, error) {
	return nil, api.ErrNotImplemented
}

func (s *accountService) OAuthWait(context.Context, api.AccountOAuthWaitParams) (*api.AccountOAuthWaitResult, error) {
	return nil, api.ErrNotImplemented
}

func (s *accountService) OAuthCancel(context.Context, api.AccountOAuthCancelParams) (*api.AccountOAuthCancelResult, error) {
	return nil, api.ErrNotImplemented
}
