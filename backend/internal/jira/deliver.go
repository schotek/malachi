// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	imime "github.com/schotek/malachi/backend/internal/mime"
	"github.com/schotek/malachi/backend/internal/smtp"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/internal/transport"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Delivery of queued comments (docs/api.md §4.3 message.send). A comment
// draft is queued like mail: message.send builds a MIME message of it into
// the account's outbox and the outbox worker hands it here. The comment is
// posted to its issue in the site's format (BuildComment) with two entity
// properties: OutboxPropertyKey, naming the outbox message, which finds the
// comment again when a retry follows an attempt whose answer never came
// (a timeout after the site had taken it), and, for an internal comment of
// a service-desk issue, sd.public.comment. After the post the issue is
// refreshed, so the comment is stored before the outbox row goes.

const (
	// OutboxPropertyKey is the comment property every posted comment
	// carries: {"id": <outbox message id>}.
	OutboxPropertyKey = "io.github.schotek.malachi.outbox"
	// sdPublicCommentKey is Jira Service Management's visibility property.
	sdPublicCommentKey = "sd.public.comment"
	// deliverRefreshWait bounds the wait for the issue's refresh after a
	// comment was posted.
	deliverRefreshWait = 30 * time.Second
	// deliveredWindow is how many of the issue's newest comments a retry
	// searches for the comment an earlier attempt may have posted.
	deliveredWindow = 50
)

// Deliver posts the queued comment e, whose MIME message (size bytes) r
// reads, to its issue. The result follows the outbox worker's contract: nil
// when the comment is on the site (posted now, or found posted by an
// earlier attempt), else a *smtp.SendError — permanent for what a retry
// cannot change (a message that is no comment or not of a Jira account, a
// body that converts to nothing or too much, an internal comment on an
// issue that is no service-desk issue, and the site's 400, 403, 404, 413
// and other 4xx), transient otherwise (network, timeouts, 429, 5xx) —
// with the site's refusal of the token as authFailed, which the worker
// defers the account's queue on.
func (sv *Supervisor) Deliver(ctx context.Context, e store.OutboxEntry, r io.Reader, size int64) error {
	permanent := func(err error) error {
		return &smtp.SendError{Err: ToAPIError(err), Stage: smtp.StageData, Permanent: true}
	}
	if e.Comment == nil || e.Comment.IssueID == "" {
		return permanent(api.NewError(api.CodeInvalidArgument, "jira: the queued message is no comment"))
	}
	acc, err := sv.deps.Store.GetAccount(ctx, e.AccountID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return permanent(api.NewError(api.CodeAccountNotFound, "jira: account %s is gone", e.AccountID))
	case err != nil:
		return &smtp.SendError{Err: api.NewError(api.CodeStorageError, "%s", transport.CleanMessage(err.Error())), Stage: smtp.StageData}
	}
	cfg := acc.Config.Jira
	if acc.Config.Protocol() != api.AccountJira || cfg == nil {
		return permanent(api.NewError(api.CodeInvalidArgument, "jira: account %s is no jira account", acc.ID))
	}
	body, err := sv.commentBody(r, cfg.Deployment)
	if err != nil {
		return permanent(err)
	}

	issueID := e.Comment.IssueID
	issue, err := sv.deps.Store.GetIssue(ctx, acc.ID, issueID)
	known := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return &smtp.SendError{Err: api.NewError(api.CodeStorageError, "%s", transport.CleanMessage(err.Error())), Stage: smtp.StageData}
	}
	props := map[string]json.RawMessage{}
	id, err := json.Marshal(struct {
		ID string `json:"id"`
	}{e.MessageID})
	if err != nil {
		return permanent(api.NewError(api.CodeInvalidArgument, "jira: outbox id: %v", err))
	}
	props[OutboxPropertyKey] = id
	switch e.Comment.Visibility {
	case "", api.CommentPublic:
	case api.CommentInternal:
		if !known || !issue.ServiceDesk {
			return permanent(api.NewError(api.CodeInvalidArgument, "jira: an internal comment needs a service-desk issue"))
		}
		props[sdPublicCommentKey] = json.RawMessage(`{"internal":true}`)
	default:
		return permanent(api.NewError(api.CodeInvalidArgument, "jira: unknown comment visibility %q", e.Comment.Visibility))
	}

	ac, err := sv.clientFor(acc)
	if err != nil {
		return permanent(err)
	}
	log := sv.deps.Log.With("component", "jira", "account", acc.ID, "message", e.MessageID, "issue", issueID)
	if e.Attempts > 0 {
		posted, err := sv.alreadyPosted(ctx, ac, acc.ID, issueID, e.MessageID)
		if err != nil {
			return deliverError(ctx, ac, err)
		}
		if posted {
			log.Info("jira comment already posted by an earlier attempt", "attempts", e.Attempts)
			sv.refreshAfterDelivery(ctx, acc.ID, issue.Key, known, log)
			return nil
		}
	}
	if _, err := ac.remote.AddComment(ctx, issueID, body, props); err != nil {
		return deliverError(ctx, ac, err)
	}
	log.Info("jira comment posted", "visibility", e.Comment.Visibility, "attempts", e.Attempts+1)
	sv.refreshAfterDelivery(ctx, acc.ID, issue.Key, known, log)
	return nil
}

// commentBody reads the queued MIME message and converts its body (the
// HTML part, else the text) into the deployment's format.
func (sv *Supervisor) commentBody(r io.Reader, d api.JiraDeployment) (CommentBody, error) {
	raw, err := io.ReadAll(io.LimitReader(r, api.MaxOutgoingMessageBytes+1))
	if err != nil {
		return CommentBody{}, api.NewError(api.CodeStorageError, "jira: reading the queued comment: %s", transport.CleanMessage(err.Error()))
	}
	if int64(len(raw)) > api.MaxOutgoingMessageBytes {
		return CommentBody{}, api.NewError(api.CodeInvalidArgument, "jira: the queued comment is larger than %d bytes", api.MaxOutgoingMessageBytes)
	}
	parsed, err := imime.Parse(bytes.NewReader(raw), imime.DefaultLimits())
	if err != nil {
		return CommentBody{}, api.NewError(api.CodeStorageError, "jira: the queued comment does not parse: %s", transport.CleanMessage(err.Error()))
	}
	if parsed.HasHTML && parsed.RawHTML != "" {
		return BuildComment(parsed.RawHTML, "", d)
	}
	return BuildComment("", parsed.Text, d)
}

// alreadyPosted looks among the issue's newest comments for one of the
// user carrying the outbox property of message id.
func (sv *Supervisor) alreadyPosted(ctx context.Context, ac *accountClient, accountID, issueID, id string) (bool, error) {
	me, err := sv.meOf(ctx, ac, accountID)
	if err != nil {
		return false, err
	}
	cl, err := ac.remote.Comments(ctx, issueID, deliveredWindow)
	if err != nil {
		return false, err
	}
	for _, c := range cl.Comments {
		raw, ok := c.Properties[OutboxPropertyKey]
		if !ok || c.Author.ID != me {
			continue
		}
		var v struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &v) == nil && v.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// meOf is the site's id of the user: as the syncer stored it, else asked.
func (sv *Supervisor) meOf(ctx context.Context, ac *accountClient, accountID string) (string, error) {
	if raw, ok, err := sv.deps.Store.GetMeta(ctx, store.MetaIssueMePrefix+accountID); err == nil && ok {
		var me meMeta
		if json.Unmarshal([]byte(raw), &me) == nil && me.ID != "" {
			return me.ID, nil
		}
	}
	u, err := ac.remote.Myself(ctx)
	if err != nil {
		return "", err
	}
	return u.ID, nil
}

// refreshAfterDelivery refreshes the issue a comment went to and waits for
// it (at most deliverRefreshWait), so the syncer stores the comment before
// the worker drops the outbox row. Best effort: the comment is on the site
// whatever happens here, and the next pass brings it anyway.
func (sv *Supervisor) refreshAfterDelivery(ctx context.Context, accountID, key string, known bool, log *slog.Logger) {
	if !known || key == "" {
		return
	}
	if err := sv.RefreshIssueWait(ctx, accountID, key, deliverRefreshWait); err != nil {
		log.Info("issue not refreshed after the comment", "code", ToAPIError(err).Code)
	}
}

// deliverError maps a failed exchange to the outbox worker's contract (see
// Deliver). A refused token is dropped from the cache.
func deliverError(ctx context.Context, ac *accountClient, err error) error {
	if ctx.Err() != nil {
		return &smtp.SendError{Err: api.NewError(api.CodeCancelled, "cancelled"), Stage: smtp.StageData}
	}
	ae := ToAPIError(err)
	var se *StatusError
	if !errors.As(err, &se) {
		// Transport failures, the keyring's errors (the worker's
		// credential path) and cancellation are transient; a request this
		// side refused to make is not.
		return &smtp.SendError{Err: ae, Stage: smtp.StageConnect, Permanent: ae.Code == api.CodeInvalidArgument}
	}
	switch {
	case se.Status == http.StatusUnauthorized:
		ac.token.invalidate()
		return &smtp.SendError{Err: ae, Stage: smtp.StageAuth}
	case se.Status == http.StatusRequestTimeout, se.Status == http.StatusTooManyRequests, se.Status >= 500:
		return &smtp.SendError{Err: ae, Stage: smtp.StageData}
	case se.Status == http.StatusRequestEntityTooLarge:
		return &smtp.SendError{Err: ae, Stage: smtp.StageSize, Permanent: true}
	}
	// 400 (the site refused the comment), 403 (no permission to comment),
	// 404 (the issue is gone or hidden) and any other 4xx: a retry of the
	// same comment will not change the answer.
	return &smtp.SendError{Err: ae, Stage: smtp.StageData, Permanent: true}
}
