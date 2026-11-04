// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// The issue projection of an issue-tracker account's messages and threads
// (docs/api.md §3 MessageSummary.issue, §4.4 ThreadSummary.issue): what
// internal/jira keeps in the issue tables, read per page in one query and
// set on the summaries the services built. Mail accounts are never looked
// up.

// issueView is what the projection of one account needs besides the
// issue rows: its site (IssueInfo.URL) and the site's id of the user
// (IssueInfo.AssignedToMe).
type issueView struct {
	site string
	me   string
}

// issueViewOf reads the account's site and user; a user the syncer has
// not asked the site for yet (or an unreadable record) is nobody.
func (b *Backend) issueViewOf(ctx context.Context, a store.Account) issueView {
	v := issueView{}
	if a.Config.Jira != nil {
		v.site = a.Config.Jira.SiteURL
	}
	raw, ok, err := b.store.GetMeta(ctx, store.MetaIssueMePrefix+a.ID)
	if err != nil || !ok {
		return v
	}
	var me struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(raw), &me) == nil {
		v.me = me.ID
	}
	return v
}

// info projects an issue row. A row whose key another issue took (a
// placeholder until its next refresh, store.PutIssue) shows no key.
func (v issueView) info(is store.Issue) api.IssueInfo {
	key := is.Key
	if strings.HasPrefix(key, "~") {
		key = ""
	}
	out := api.IssueInfo{
		Key:            key,
		URL:            issueURL(v.site, key),
		Summary:        is.Summary,
		Status:         is.Status,
		StatusCategory: is.StatusCategory,
		Type:           is.Type,
		Priority:       is.Priority,
		Assignee:       is.AssigneeName,
		Reporter:       is.ReporterName,
		AssignedToMe:   v.me != "" && is.AssigneeID == v.me,
		Watching:       is.Watching,
	}
	if is.ServiceDesk {
		out.CommentVisibilities = []api.CommentVisibility{api.CommentPublic, api.CommentInternal}
	}
	return out
}

// message projects an item and its issue.
func (v issueView) message(d store.IssueDecoration) *api.MessageIssue {
	return &api.MessageIssue{
		IssueInfo:  v.info(d.Issue),
		Item:       d.Item.Kind,
		Visibility: d.Item.Visibility,
		Changes:    d.Item.Changes,
		Via:        d.Item.Via,
		Edited:     d.Item.Edited,
		Mine:       v.me != "" && d.Item.AuthorID == v.me && d.Item.Via == "",
	}
}

// issueURL is <site>/browse/<key> for an http(s) site; "" otherwise (or
// without a key).
func issueURL(site, key string) string {
	if key == "" {
		return ""
	}
	u, err := url.Parse(site)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return strings.TrimRight(site, "/") + "/browse/" + url.PathEscape(key)
}

// isIssueAccount says whether the account's messages carry the issue
// projection.
func isIssueAccount(a store.Account) bool {
	return a.Config.Protocol() == api.AccountJira
}

// decorateIssues sets MessageSummary.issue on the summaries of an
// issue-tracker account: remoteIDs[i] is the remote id of out[i] ("" =
// none). One store query for the lot; a mail account is left alone.
func (b *Backend) decorateIssues(ctx context.Context, a store.Account, remoteIDs []string, out []api.MessageSummary) error {
	if !isIssueAccount(a) || len(out) == 0 {
		return nil
	}
	decos, err := b.store.IssueDecorations(ctx, a.ID, remoteIDs)
	if err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	if len(decos) == 0 {
		return nil
	}
	v := b.issueViewOf(ctx, a)
	for i := range out {
		if i < len(remoteIDs) {
			if d, ok := decos[remoteIDs[i]]; ok {
				out[i].Issue = v.message(d)
			}
		}
	}
	return nil
}

// decorateRows is decorateIssues over store rows and the summaries made
// of them, index for index.
func (b *Backend) decorateRows(ctx context.Context, a store.Account, rows []store.Message, out []api.MessageSummary) error {
	if !isIssueAccount(a) {
		return nil
	}
	ids := make([]string, len(rows))
	for i, m := range rows {
		ids[i] = m.RemoteID
	}
	return b.decorateIssues(ctx, a, ids, out)
}

// decorateThreads sets ThreadSummary.issue (and the issue of each latest
// member) on the threads of an issue-tracker account; latest[i] is the
// store row of out[i].Latest.
func (b *Backend) decorateThreads(ctx context.Context, a store.Account, latest []store.Message, out []api.ThreadSummary) error {
	if !isIssueAccount(a) || len(out) == 0 {
		return nil
	}
	ids := make([]string, len(out))
	for i := range out {
		ids[i] = string(out[i].ID)
	}
	issues, err := b.store.IssuesByThread(ctx, a.ID, ids)
	if err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	summaries := make([]api.MessageSummary, len(out))
	for i := range out {
		summaries[i] = out[i].Latest
	}
	if err := b.decorateRows(ctx, a, latest, summaries); err != nil {
		return err
	}
	v := b.issueViewOf(ctx, a)
	for i := range out {
		out[i].Latest = summaries[i]
		if is, ok := issues[string(out[i].ID)]; ok {
			info := v.info(is)
			out[i].Issue = &info
		}
	}
	return nil
}

// decorateNewMessage completes a notify.newMessage of an issue-tracker
// account with the message's issue. Anything that cannot be read leaves
// the notification as it is: it still announces the message.
func (b *Backend) decorateNewMessage(ev *api.NewMessageNotification) {
	if ev.Message.Issue != nil || !strings.HasPrefix(string(ev.Message.ThreadID), store.IssueThreadPrefix) {
		return
	}
	ctx := context.Background()
	a, err := b.store.GetAccount(ctx, string(ev.AccountID))
	if err != nil || !isIssueAccount(a) {
		return
	}
	m, err := b.store.GetMessage(ctx, a.ID, string(ev.Message.ID))
	if err != nil {
		return
	}
	if ev.Message.To == nil {
		ev.Message.To = []api.Address{} // as message.list has it
	}
	list := []api.MessageSummary{ev.Message}
	if err := b.decorateIssues(ctx, a, []string{m.RemoteID}, list); err != nil {
		b.log.Warn("issue of a new message", "account", a.ID, "message", m.ID, "err", err)
		return
	}
	ev.Message = list[0]
}
