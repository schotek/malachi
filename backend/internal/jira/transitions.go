// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Status transitions (docs/api.md §4.12 issue.transitions and
// issue.transition). Both go straight to the site with the account's
// client: the list of what the site offers on the issue, and one
// transition performed by its id, followed by a refresh of the issue so
// that the new status and its event row are stored before the caller
// answers. A transition that needs input (a screen or required fields) is
// refused here, before the site is asked: the site would answer 400 with
// the fields it misses, and nothing here could fill them.

// transitionRefreshWait bounds the wait for the issue's refresh after a
// transition.
const transitionRefreshWait = 30 * time.Second

// Transitions lists the status changes the site offers the user on the
// issue, in the site's order. Errors are *api.Error: accountNotFound,
// invalidArgument (an account without jira settings, a bad issue id),
// authFailed (the token was refused and is dropped from the cache),
// messageGone (the issue is gone or hidden), serverError, serverTimeout,
// networkError, keyringError.
func (sv *Supervisor) Transitions(ctx context.Context, accountID, issueID string) ([]Transition, error) {
	ac, err := sv.accountClient(ctx, accountID)
	if err != nil {
		return nil, err
	}
	ts, err := ac.remote.Transitions(ctx, issueID)
	if err != nil {
		return nil, siteError(ac, err)
	}
	return ts, nil
}

// Transition performs the transition on the issue and refreshes the issue
// (at most transitionRefreshWait; a refresh that does not finish is logged,
// the transition having happened). Errors are Transitions' plus
// invalidArgument for a transition the issue does not offer or one that
// needs input, and serverError with the site's message when the site
// refuses the transition.
func (sv *Supervisor) Transition(ctx context.Context, accountID, issueID, transitionID string) error {
	if !validID(transitionID) {
		return api.NewError(api.CodeInvalidArgument, "jira: bad transition id")
	}
	ac, err := sv.accountClient(ctx, accountID)
	if err != nil {
		return err
	}
	ts, err := ac.remote.Transitions(ctx, issueID)
	if err != nil {
		return siteError(ac, err)
	}
	var chosen *Transition
	for i := range ts {
		if ts[i].ID == transitionID {
			chosen = &ts[i]
			break
		}
	}
	switch {
	case chosen == nil:
		return api.NewError(api.CodeInvalidArgument, "jira: the issue offers no transition %q", transitionID)
	case chosen.NeedsInput:
		return api.NewError(api.CodeInvalidArgument, "jira: transition %q needs fields filled in on the site", transitionID)
	}
	if err := ac.remote.Transition(ctx, issueID, transitionID); err != nil {
		return siteError(ac, err)
	}
	log := sv.deps.Log.With("component", "jira", "account", accountID, "issue", issueID)
	log.Info("jira issue transitioned", "transition", transitionID, "to", chosen.To.Name)
	is, err := sv.deps.Store.GetIssue(ctx, accountID, issueID)
	if err != nil || is.Key == "" || strings.HasPrefix(is.Key, "~") {
		return nil // not stored under a usable key: the next pass brings the change
	}
	if err := sv.RefreshIssueWait(ctx, accountID, is.Key, transitionRefreshWait); err != nil {
		log.Info("issue not refreshed after the transition", "code", ToAPIError(err).Code)
	}
	return nil
}

// accountClient is the client of the stored account. Errors are
// accountNotFound, storageError and invalidArgument (no jira settings).
func (sv *Supervisor) accountClient(ctx context.Context, accountID string) (*accountClient, error) {
	acc, err := sv.deps.Store.GetAccount(ctx, accountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeAccountNotFound, "jira: unknown account %q", accountID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%s", transport.CleanMessage(err.Error()))
	}
	if acc.Config.Protocol() != api.AccountJira || acc.Config.Jira == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "jira: account %s is no jira account", acc.ID)
	}
	return sv.clientFor(acc)
}

// siteError maps a failed exchange of a request made on the user's behalf:
// a refused token is dropped from the cache (authFailed), a 404 is
// messageGone (the issue is gone or hidden), the rest as ToAPIError (a 400
// is serverError with the site's cleaned message).
func siteError(ac *accountClient, err error) error {
	var se *StatusError
	if errors.As(err, &se) {
		switch se.Status {
		case http.StatusUnauthorized:
			ac.token.invalidate()
		case http.StatusNotFound:
			return api.NewError(api.CodeMessageGone, "jira: %s", transport.CleanMessage(se.Message))
		}
	}
	return ToAPIError(err)
}
