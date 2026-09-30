// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
	"github.com/schotek/malachi/ui/internal/widget"
)

// The Change Status menu of a Jira issue without the widgets
// (ui/internal/jira/transitions.go has the texts and the items;
// issue_card.go the menu): load asks issue.transitions for the issue of a
// message and hands the menu its items, perform calls issue.transition for
// the chosen one, shows the toast and hands every view the refreshed issue
// (onIssue), so the card changes at once, before the daemon's refresh
// reaches the list and the pane through the usual notifications. Only an
// account with api.CapabilityTransition gets either call. macOS has it as
// IssueActionsController.
//
// Stale replies: a menu opens, closes and opens again while the site is
// slow; only the newest load's reply reaches its callback (cancelLoad
// drops the running one's when the menu closes). A transition runs at most
// once per issue at a time (busy): the pill shows a spinner meanwhile
// (onBusy) and the menu refuses a second choice.

// caller is the RPC client as the issue actions use it: client.Client, or
// a fake in the tests.
type caller interface {
	Call(ctx context.Context, method string, params, result any) error
}

// issueSubject is what the menu acts on: the issue of a message (any
// message of it; the message's thread is the issue).
type issueSubject struct {
	Account api.AccountID
	Message api.MessageID
}

// subjectOf is the subject of message s: its issue when it has one.
func subjectOf(s api.MessageSummary) (issueSubject, bool) {
	if s.Issue == nil || s.ID == "" {
		return issueSubject{}, false
	}
	return issueSubject{Account: s.AccountID, Message: s.ID}, true
}

// loadedTransitions is what issue.transitions answered, as the menu shows
// it: the issue as the daemon last synchronised it, and the items.
type loadedTransitions struct {
	Issue api.IssueInfo
	Items []jira.TransitionItem
}

// runningIssue is an issue a transition runs on.
type runningIssue struct {
	account api.AccountID
	key     string
}

// issueTimeout bounds issue.transitions and issue.transition: the daemon
// asks the site, and after a transition waits up to 30 s for its refresh
// of the issue (docs/api.md §4.12).
const issueTimeout = 45 * time.Second

// issueActions is the controller of the Change Status menus of a window.
type issueActions struct {
	client caller
	log    *slog.Logger
	// post runs fn on the main loop (glib.IdleAdd; the tests run it
	// inline or on their goroutine).
	post func(fn func())
	// account is the account of an id as the window knows it; false
	// (unknown) offers nothing.
	account func(api.AccountID) (api.Account, bool)
	// toast shows a sentence in the window.
	toast func(string)
	// onBusy is called when a transition starts (busy true) and when it
	// ended either way, with the issue's key as the daemon knows it.
	onBusy func(acc api.AccountID, key string, busy bool)
	// onIssue is called with the issue issue.transition returned: the
	// views showing it apply it to their cards.
	onIssue func(acc api.AccountID, issue api.IssueInfo)

	// loadGen is the load whose reply is still wanted; older ones are
	// dropped. running are the issues a transition runs on. Main loop only.
	loadGen int
	running map[runningIssue]bool
}

// canTransition reports whether the account offers the menu
// (jira.CanTransition).
func (c *issueActions) canTransition(acc api.AccountID) bool {
	a, ok := c.account(acc)
	return ok && jira.CanTransition(a)
}

// busy reports whether a transition runs on the issue key of acc now.
func (c *issueActions) busy(acc api.AccountID, key string) bool {
	return c.running[runningIssue{account: acc, key: key}]
}

// load asks issue.transitions for the issue of subject and gives done the
// items, or the error for the menu's only item
// (widget.RPCErrorText(jira.LoadTransitionsAction(…), err)), unless a newer
// load started or cancelLoad was called meanwhile. It returns false, and
// asks nothing, when the account cannot change statuses.
func (c *issueActions) load(subject issueSubject, done func(loadedTransitions, error)) bool {
	if !c.canTransition(subject.Account) {
		return false
	}
	c.loadGen++
	gen := c.loadGen
	params := api.IssueTransitionsParams{AccountID: subject.Account, MessageID: subject.Message}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), issueTimeout)
		defer cancel()
		var res api.IssueTransitionsResult
		err := c.client.Call(ctx, api.MethodIssueTransitions, params, &res)
		c.post(func() {
			if gen != c.loadGen {
				return
			}
			if err != nil {
				c.log.Warn("issue.transitions", "err", err)
				done(loadedTransitions{}, err)
				return
			}
			done(loadedTransitions{Issue: res.Issue, Items: jira.Transitions(res, i18n.Tr)}, nil)
		})
	}()
	return true
}

// cancelLoad drops the reply of the load that runs, if any (the menu
// closed).
func (c *issueActions) cancelLoad() {
	c.loadGen++
}

// perform performs item on the issue of subject (issue is what
// issue.transitions returned with it: its key names the spinner). Nothing
// happens for a disabled item, for an account without the capability, or
// while a transition already runs on the issue. On success the toast says
// the new status and onIssue carries the refreshed issue; on failure the
// toast says why. It returns whether the call was made.
func (c *issueActions) perform(subject issueSubject, item jira.TransitionItem, issue api.IssueInfo) bool {
	if !item.Enabled || item.ID == "" || !c.canTransition(subject.Account) {
		return false
	}
	key := runningIssue{account: subject.Account, key: issue.Key}
	if c.running[key] {
		return false
	}
	if c.running == nil {
		c.running = make(map[runningIssue]bool)
	}
	c.running[key] = true
	if c.onBusy != nil {
		c.onBusy(subject.Account, issue.Key, true)
	}
	params := api.IssueTransitionParams{AccountID: subject.Account, MessageID: subject.Message, TransitionID: item.ID}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), issueTimeout)
		defer cancel()
		var res api.IssueTransitionResult
		err := c.client.Call(ctx, api.MethodIssueTransition, params, &res)
		c.post(func() {
			delete(c.running, key)
			if err != nil {
				c.log.Warn("issue.transition", "err", err)
				c.toast(transitionFailedText(err))
			} else {
				if c.onIssue != nil {
					c.onIssue(subject.Account, res.Issue)
				}
				c.toast(jira.StatusChanged(item, res.Issue, i18n.Tr))
			}
			if c.onBusy != nil {
				c.onBusy(subject.Account, issue.Key, false)
			}
		})
	}()
	return true
}

// transitionFailedText is the toast of a failed issue.transition: the
// site's own reason when it refused the change, the usual sentence
// otherwise (jira.TransitionFailed).
func transitionFailedText(err error) string {
	fallback := widget.RPCErrorText(jira.TransitionAction(i18n.Tr), err)
	var e *api.Error
	if errors.As(err, &e) {
		return jira.TransitionFailed(e.Code, e.Message, fallback, i18n.Tr)
	}
	return fallback
}
