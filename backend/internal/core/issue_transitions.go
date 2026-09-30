// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Status transitions of an issue-tracker account's issues (docs/api.md
// §4.12): issue.transitions lists what the site offers on the issue a
// message belongs to, issue.transition performs one and answers with the
// issue as the refresh that followed stored it. The account needs
// CapabilityTransition; everything else is the jira supervisor's
// (jira.Supervisor.Transitions, Transition).

type issueService struct{ b *Backend }

func (b *Backend) Issues() api.IssueService { return &issueService{b} }

// issueOf resolves a request's message to the stored issue of its thread,
// on an account with the capability. Errors: invalidArgument (an account
// without the capability, a message of no issue), accountNotFound,
// messageNotFound, storageError.
func (s *issueService) issueOf(ctx context.Context, accountID api.AccountID, messageID api.MessageID) (store.Account, store.Issue, error) {
	a, err := s.b.requireAccount(ctx, string(accountID))
	if err != nil {
		return store.Account{}, store.Issue{}, err
	}
	if err := requireCapability(a, api.CapabilityTransition); err != nil {
		return store.Account{}, store.Issue{}, err
	}
	if messageID == "" {
		return store.Account{}, store.Issue{}, api.NewError(api.CodeInvalidArgument, "messageId is required")
	}
	m, err := s.b.getMessage(ctx, a.ID, string(messageID))
	if err != nil {
		return store.Account{}, store.Issue{}, err
	}
	is, err := s.b.issueOfMessage(ctx, a, m)
	if err != nil {
		return store.Account{}, store.Issue{}, err
	}
	return a, is, nil
}

func (s *issueService) Transitions(ctx context.Context, p api.IssueTransitionsParams) (*api.IssueTransitionsResult, error) {
	a, is, err := s.issueOf(ctx, p.AccountID, p.MessageID)
	if err != nil {
		return nil, err
	}
	ts, err := s.b.jiraSync.Transitions(ctx, a.ID, is.IssueID)
	if err != nil {
		return nil, apiError(err)
	}
	out := make([]api.IssueTransition, 0, len(ts))
	for _, t := range ts {
		out = append(out, api.IssueTransition{
			ID: t.ID, Name: t.Name, To: t.To.Name, ToCategory: t.To.Category, NeedsInput: t.NeedsInput,
		})
	}
	return &api.IssueTransitionsResult{Issue: s.b.issueViewOf(ctx, a).info(is), Transitions: out}, nil
}

func (s *issueService) Transition(ctx context.Context, p api.IssueTransitionParams) (*api.IssueTransitionResult, error) {
	if p.TransitionID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "transitionId is required")
	}
	a, is, err := s.issueOf(ctx, p.AccountID, p.MessageID)
	if err != nil {
		return nil, err
	}
	if err := s.b.jiraSync.Transition(ctx, a.ID, is.IssueID, p.TransitionID); err != nil {
		return nil, apiError(err)
	}
	s.b.log.Info("issue transitioned", "account", a.ID, "issue", is.IssueID, "transition", p.TransitionID)
	// The issue as the refresh stored it; as it was when the refresh did
	// not finish in time (the transition happened all the same).
	if fresh, err := s.b.store.GetIssue(ctx, a.ID, is.IssueID); err == nil {
		is = fresh
	}
	return &api.IssueTransitionResult{Issue: s.b.issueViewOf(ctx, a).info(is)}, nil
}
