// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/internal/jira"
	"github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Comment drafts of issue-tracker accounts (docs/api.md §4.5, §4.3). A
// comment draft is an ordinary draft of the account whose inReplyTo names
// a message of an issue (any of its items): draft.create reply makes it,
// draft.save keeps it free of what a comment cannot carry (recipients,
// attachments, a forward, a Drafts copy) and checks its visibility against
// the issue, draft.list tells the issue, and message.send queues it into
// the account's outbox as a MIME message like any other, marked with its
// issue and visibility, for the jira supervisor to post
// (jira.Supervisor.Deliver). The queued row joins the issue's thread by its
// In-Reply-To until the comment itself arrives with the next refresh.

// commentIssue is the issue a comment draft goes to: the issue of the
// thread of the message inReplyTo names. Every failure is invalidArgument
// (a draft that cannot be a comment) or storageError.
func (b *Backend) commentIssue(ctx context.Context, a store.Account, inReplyTo string) (store.Issue, error) {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	if inReplyTo == "" {
		return store.Issue{}, bad("a comment draft needs inReplyTo: the message of the issue it answers")
	}
	m, err := b.store.GetMessage(ctx, a.ID, inReplyTo)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Issue{}, bad("inReplyTo %s names no message of account %s", inReplyTo, a.ID)
	case err != nil:
		return store.Issue{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	return b.issueOfMessage(ctx, a, m)
}

// issueOfMessage is the stored issue of a message of the account.
func (b *Backend) issueOfMessage(ctx context.Context, a store.Account, m store.Message) (store.Issue, error) {
	issueID, ok := store.IssueIDOfThread(m.ThreadID)
	if !ok {
		return store.Issue{}, api.NewError(api.CodeInvalidArgument, "message %s belongs to no issue", m.ID)
	}
	is, err := b.store.GetIssue(ctx, a.ID, issueID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Issue{}, api.NewError(api.CodeInvalidArgument, "the issue of message %s is no longer stored", m.ID)
	case err != nil:
		return store.Issue{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	return is, nil
}

// checkCommentVisibility refuses a visibility the issue does not allow:
// internal exists on service-desk issues only.
func checkCommentVisibility(v api.CommentVisibility, is store.Issue) error {
	switch v {
	case "", api.CommentPublic:
		return nil
	case api.CommentInternal:
		if is.ServiceDesk {
			return nil
		}
		return api.NewError(api.CodeInvalidArgument, "issue %s takes no internal comment", is.Key)
	}
	return api.NewError(api.CodeInvalidArgument, "unknown comment visibility %q", v)
}

// commentSubject is the subject of every message of an issue ("KEY:
// Summary"); fallback when the issue has no usable key.
func commentSubject(is store.Issue, fallback string) string {
	if is.Key == "" || strings.HasPrefix(is.Key, "~") {
		return capSubject(cleanSubject(fallback))
	}
	return capSubject(cleanSubject(is.Key + ": " + is.Summary))
}

// createCommentDraft is draft.create reply on an account that comments:
// the template of a comment on the issue of message id — the issue as last
// synchronised, visibility "" (public), the issue's subject, no recipients
// and an empty body, nothing quoted.
func (b *Backend) createCommentDraft(ctx context.Context, a store.Account, id string) (*api.DraftCreateResult, error) {
	m, err := b.getMessage(ctx, a.ID, id)
	if err != nil {
		return nil, err
	}
	is, err := b.issueOfMessage(ctx, a, m)
	if err != nil {
		return nil, err
	}
	d := api.Draft{
		AccountID: api.AccountID(a.ID),
		Subject:   commentSubject(is, m.Subject),
		InReplyTo: api.MessageID(m.ID),
		Comment:   &api.DraftComment{Issue: b.issueViewOf(ctx, a).info(is)},
	}
	return &api.DraftCreateResult{Draft: d, Quoted: api.QuoteNone}, nil
}

// checkCommentDraft applies the rules of a comment draft to a draft.save of
// an issue-tracker account: no recipients, attachments, forward or Drafts
// copy; inReplyTo names a message of an issue; the visibility is one the
// issue allows. The subject becomes the issue's. It returns the
// visibility to store.
func (b *Backend) checkCommentDraft(ctx context.Context, a store.Account, d *api.Draft) (api.CommentVisibility, error) {
	bad := func(format string, args ...any) error {
		return api.NewError(api.CodeInvalidArgument, format, args...)
	}
	switch {
	case len(d.To)+len(d.CC)+len(d.BCC) > 0:
		return "", bad("a comment draft has no recipients")
	case len(d.Attachments) > 0:
		return "", bad("a comment draft has no attachments")
	case d.Forwarding != "":
		return "", bad("account %s forwards no message itself; forward it from a mail account", a.ID)
	case d.Replaces != "":
		return "", bad("a comment draft replaces no message")
	}
	is, err := b.commentIssue(ctx, a, string(d.InReplyTo))
	if err != nil {
		return "", err
	}
	var v api.CommentVisibility
	if d.Comment != nil {
		v = d.Comment.Visibility
	}
	if err := checkCommentVisibility(v, is); err != nil {
		return "", err
	}
	d.Subject = commentSubject(is, d.Subject)
	return v, nil
}

// decorateCommentDrafts sets Draft.comment on the drafts of an
// issue-tracker account (draft.list): the issue each answers, as last
// synchronised (empty when it is no longer stored), and its visibility.
// rows[i] is the stored draft of out[i]. Mail accounts are left alone.
func (b *Backend) decorateCommentDrafts(ctx context.Context, accountID string, rows []store.Draft, out []api.Draft) {
	a, err := b.store.GetAccount(ctx, accountID)
	if err != nil || !isIssueAccount(a) || len(rows) == 0 {
		return
	}
	v := b.issueViewOf(ctx, a)
	for i, d := range rows {
		c := &api.DraftComment{Visibility: d.CommentVisibility}
		if is, err := b.commentIssue(ctx, a, d.InReplyTo); err == nil {
			c.Issue = v.info(is)
		}
		out[i].Comment = c
	}
}

// jiraSender is the From of a queued comment: the user as the site names
// them (the syncer's record), else the account's display name, at the
// account's address.
func (b *Backend) jiraSender(ctx context.Context, a store.Account) api.Address {
	from := api.Address{Name: a.Config.DisplayName, Address: a.Config.Email}
	if raw, ok, err := b.store.GetMeta(ctx, store.MetaIssueMePrefix+a.ID); err == nil && ok {
		var me struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(raw), &me) == nil && strings.TrimSpace(me.Name) != "" && !hasHeaderBreak(me.Name) {
			from.Name = me.Name
		}
	}
	return from
}

// sendComment is message.send of a comment draft (d, at the version the
// client saw): the comment's rules checked again against what is stored
// now, its body converted once as the site will get it (jira.CheckComment:
// empty or too long is invalidArgument here, not a failed delivery later),
// and the MIME message queued with the issue and the visibility.
func (s *messageService) sendComment(ctx context.Context, a store.Account, p api.MessageSendParams, d store.Draft) (*api.MessageSendResult, error) {
	if len(d.To)+len(d.CC)+len(d.BCC) > 0 || len(d.Attachments) > 0 || d.Forwarding != "" {
		return nil, api.NewError(api.CodeInvalidArgument, "a comment draft with recipients, attachments or a forward")
	}
	if a.Config.Jira == nil {
		return nil, api.NewError(api.CodeInvalidArgument, "account %s has no jira settings", a.ID)
	}
	is, err := s.b.commentIssue(ctx, a, d.InReplyTo)
	if err != nil {
		return nil, err
	}
	if err := checkCommentVisibility(d.CommentVisibility, is); err != nil {
		return nil, err
	}
	src := d.HTMLBody
	if src == "" {
		src = jira.TextToHTML(d.TextBody)
	}
	if err := jira.CheckComment(src, a.Config.Jira.Deployment); err != nil {
		return nil, apiError(err)
	}

	now := time.Now()
	from := s.b.jiraSender(ctx, a)
	subject := commentSubject(is, d.Subject)
	inReplyTo, references := s.b.draftThreading(ctx, d)
	in := smtp.BuildInput{
		From:       from,
		Subject:    subject,
		Text:       d.TextBody,
		HTML:       d.HTMLBody, // the sanitiser's output, stored by draft.save
		InReplyTo:  inReplyTo,
		References: references,
		Date:       now,
		MessageID:  smtp.NewMessageID(a.Config.Email),
	}
	m, err := s.b.store.EnqueueOutbox(ctx, store.EnqueueInput{
		DraftID:      d.ID,
		DraftVersion: p.Version,
		Message: store.Message{
			AccountID:    a.ID,
			From:         []api.Address{from},
			Subject:      subject,
			Date:         now,
			InternalDate: now,
			RFCMessageID: in.MessageID,
			InReplyTo:    inReplyTo,
			References:   references,
			Snippet:      mime.Snippet(d.TextBody, snippetRunes),
			HasHTML:      d.HTMLBody != "",
		},
		Text:         d.TextBody,
		EnvelopeFrom: a.Config.Email,
		Comment:      &store.OutboxComment{IssueID: is.IssueID, Visibility: d.CommentVisibility},
		Build:        func(w io.Writer) error { return smtp.BuildMessage(w, in) },
		Limit:        outgoingLimit,
	})
	if err != nil {
		return nil, enqueueError(err, p.DraftID, d)
	}
	s.b.log.Info("comment queued", "account", a.ID, "message", m.ID, "issue", is.IssueID,
		"visibility", d.CommentVisibility, "size", m.Size)
	s.b.Delivery.Wake(a.ID)
	s.b.outboxChanged(a.ID)
	return &api.MessageSendResult{OutboxID: api.MessageID(m.ID)}, nil
}
