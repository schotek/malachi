// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"errors"

	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

type messageService struct{ b *Backend }

// List pages through a folder (docs/api.md §4.3). Only messages within
// the offlineDays window exist locally.
func (s *messageService) List(ctx context.Context, p api.MessageListParams) (*api.MessageListResult, error) {
	if p.AccountID == "" || p.FolderID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and folderId are required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	limit := p.Page.Limit
	if limit <= 0 {
		limit = api.DefaultPageLimit
	}
	if limit > api.MaxPageLimit {
		limit = api.MaxPageLimit
	}
	sortOrder := p.Sort
	switch sortOrder {
	case "":
		sortOrder = api.SortDateDesc
	case api.SortDateDesc, api.SortDateAsc:
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "sort must be dateDesc or dateAsc")
	}
	items, next, total, err := s.b.store.ListMessages(ctx, a.ID, string(p.FolderID), p.Page.Cursor, limit, sortOrder, p.UnreadOnly)
	switch {
	case errors.Is(err, store.ErrBadCursor):
		return nil, api.NewError(api.CodeInvalidArgument, "invalid page cursor")
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeFolderNotFound, "unknown folder %q", p.FolderID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	out := make([]api.MessageSummary, 0, len(items))
	for _, m := range items {
		out = append(out, toAPISummary(m))
	}
	if err := s.b.attachOutboxInfo(ctx, a.ID, string(p.FolderID), out); err != nil {
		return nil, err
	}
	return &api.MessageListResult{Messages: out, Page: api.PageInfo{NextCursor: next, Total: total}}, nil
}

// attachOutboxInfo fills MessageSummary.outbox for a listing of the
// account's outbox folder; any other folder is left alone.
func (b *Backend) attachOutboxInfo(ctx context.Context, accountID, folderID string, list []api.MessageSummary) error {
	if len(list) == 0 {
		return nil
	}
	f, err := b.store.GetFolder(ctx, accountID, folderID)
	if err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	if f.Role != api.RoleOutbox {
		return nil
	}
	ids := make([]string, 0, len(list))
	for _, m := range list {
		ids = append(ids, string(m.ID))
	}
	entries, err := b.store.OutboxEntries(ctx, accountID, ids)
	if err != nil {
		return api.NewError(api.CodeStorageError, "%v", err)
	}
	for i := range list {
		if e, ok := entries[string(list[i].ID)]; ok {
			list[i].Outbox = toAPIOutbox(e)
		}
	}
	return nil
}

func (s *messageService) Get(ctx context.Context, p api.MessageGetParams) (*api.MessageGetResult, error) {
	if p.AccountID == "" || p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and messageId are required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	m, err := s.b.getMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	out := toAPIMessage(m)
	e, err := s.b.store.GetOutbox(ctx, a.ID, m.ID)
	switch {
	case err == nil:
		out.Outbox = toAPIOutbox(e)
	case !errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	return &api.MessageGetResult{Message: out}, nil
}

// Body returns the message content: the plain text the MIME layer derived
// at sync time, and for a message with an HTML part the sanitiser's output
// over the raw file (store.OpenMessageRaw) under the resolved remote-content
// policy. That output is the only HTML that may ever cross the API
// (CLAUDE.md rule 2); nothing here reads stored HTML, because none is
// stored. An HTML part that cannot be shown safely is withheld, not an
// error: the text is still there and the caller learns why from
// htmlWithheld.
func (s *messageService) Body(ctx context.Context, p api.MessageBodyParams) (*api.MessageBodyResult, error) {
	if p.AccountID == "" || p.MessageID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "accountId and messageId are required")
	}
	switch p.RemoteContent {
	case "", api.RemoteBlock, api.RemoteAllow:
	default:
		return nil, api.NewError(api.CodeInvalidArgument, "remoteContent override must be block or allow")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	m, err := s.b.getMessage(ctx, a.ID, string(p.MessageID))
	if err != nil {
		return nil, err
	}
	// Resolve the policy now so an invalid override or a broken allow-list
	// fails the call the same way it will once the sanitiser consumes it.
	policy, err := s.b.RemoteContentFor(ctx, p.RemoteContent, m.From, false)
	if err != nil {
		return nil, err
	}
	text, hasHTML, state, err := s.b.store.GetMessageText(ctx, a.ID, m.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeMessageNotFound, "unknown message %q", p.MessageID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	res := &api.MessageBodyResult{
		MessageID:        api.MessageID(m.ID),
		BodyState:        toAPIBodyState(state),
		HasHTML:          hasHTML,
		Text:             text,
		Links:            []api.Link{},
		SanitizerVersion: sanitize.Version,
	}
	if state == store.BodyFetched && hasHTML {
		s.b.renderHTML(ctx, a.ID, m.ID, policy, res)
	}
	s.b.log.Debug("message body", "id", m.ID, "bodyState", state, "hasHtml", hasHTML,
		"remoteContent", policy, "htmlWithheld", res.HTMLWithheld, "blocked", res.Blocked)
	return res, nil
}

// Flag applies flag changes locally (all-or-nothing) and queues them for
// the syncer, which is nudged at once.
func (s *messageService) Flag(ctx context.Context, p api.MessageFlagParams) (*api.MessageFlagResult, error) {
	ids, err := validateMessageIDs(p.MessageIDs)
	if err != nil {
		return nil, err
	}
	if err := validateFlagLists(p.Set, p.Clear); err != nil {
		return nil, err
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	if err := s.b.store.FlagMessages(ctx, a.ID, ids, p.Set, p.Clear); err != nil {
		return nil, mutationError(err)
	}
	s.b.Supervisor.Trigger(a.ID, "", false)
	return &api.MessageFlagResult{}, nil
}

// Move relocates the messages locally (ids stay stable) and queues the
// server-side move.
func (s *messageService) Move(ctx context.Context, p api.MessageMoveParams) (*api.MessageMoveResult, error) {
	ids, err := validateMessageIDs(p.MessageIDs)
	if err != nil {
		return nil, err
	}
	if p.TargetFolderID == "" {
		return nil, api.NewError(api.CodeInvalidArgument, "targetFolderId is required")
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	target, err := s.b.store.GetFolder(ctx, a.ID, string(p.TargetFolderID))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, api.NewError(api.CodeFolderNotFound, "unknown folder %q", p.TargetFolderID)
	case err != nil:
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	if !target.Selectable {
		return nil, api.NewError(api.CodeInvalidArgument, "target folder is not selectable")
	}
	if err := s.b.store.MoveMessages(ctx, a.ID, ids, target.ID); err != nil {
		return nil, mutationError(err)
	}
	s.b.Supervisor.Trigger(a.ID, "", false)
	return &api.MessageMoveResult{}, nil
}

// Delete moves to the Trash role folder, or removes at once when
// permanent or already in Trash. An outbox message is removed for good
// either way (it has no server copy), which also cancels its delivery.
func (s *messageService) Delete(ctx context.Context, p api.MessageDeleteParams) (*api.MessageDeleteResult, error) {
	ids, err := validateMessageIDs(p.MessageIDs)
	if err != nil {
		return nil, err
	}
	a, err := s.b.requireAccount(ctx, string(p.AccountID))
	if err != nil {
		return nil, err
	}
	queued, err := s.b.store.OutboxEntries(ctx, a.ID, ids)
	if err != nil {
		return nil, api.NewError(api.CodeStorageError, "%v", err)
	}
	permanent := p.Permanent
	var trash store.Folder
	if !permanent {
		t, terr := s.b.store.FolderByRole(ctx, a.ID, api.RoleTrash)
		switch {
		case errors.Is(terr, store.ErrNotFound) && len(queued) == len(ids):
			permanent = true // only outbox messages: no Trash needed
		case errors.Is(terr, store.ErrNotFound):
			return nil, api.NewError(api.CodeFolderNotFound, "account has no trash folder")
		case terr != nil:
			return nil, api.NewError(api.CodeStorageError, "%v", terr)
		}
		trash = t
	}
	if permanent {
		err = s.b.store.DeleteMessages(ctx, a.ID, ids)
	} else {
		err = s.b.store.TrashMessages(ctx, a.ID, ids, trash.ID)
	}
	if err != nil {
		return nil, mutationError(err)
	}
	if len(queued) < len(ids) {
		s.b.Supervisor.Trigger(a.ID, "", false)
	}
	if len(queued) > 0 {
		s.b.outboxChanged(a.ID)
	}
	return &api.MessageDeleteResult{}, nil
}

// mutationError maps a store failure of message.flag/move/delete.
func mutationError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return api.NewError(api.CodeMessageNotFound, "unknown message")
	case errors.Is(err, store.ErrOutbox):
		return api.NewError(api.CodeInvalidArgument, "not allowed for an outbox message")
	case errors.Is(err, store.ErrOutboxBusy):
		return api.NewError(api.CodeConflict, "message is being sent")
	default:
		return api.NewError(api.CodeStorageError, "%v", err)
	}
}

// getMessage maps the store's lookup to the API error.
func (b *Backend) getMessage(ctx context.Context, accountID, id string) (store.Message, error) {
	m, err := b.store.GetMessage(ctx, accountID, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return store.Message{}, api.NewError(api.CodeMessageNotFound, "unknown message %q", id)
	case err != nil:
		return store.Message{}, api.NewError(api.CodeStorageError, "%v", err)
	}
	return m, nil
}

// validateMessageIDs de-duplicates and bounds the id list of a mutation.
func validateMessageIDs(ids []api.MessageID) ([]string, error) {
	if len(ids) == 0 {
		return nil, api.NewError(api.CodeInvalidArgument, "messageIds must not be empty")
	}
	raw := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, api.NewError(api.CodeInvalidArgument, "empty message id")
		}
		raw = append(raw, string(id))
	}
	raw = dedupe(raw)
	if len(raw) > api.MaxMessageIDsPerCall {
		return nil, api.NewError(api.CodeInvalidArgument, "too many message ids (%d, limit %d)", len(raw), api.MaxMessageIDsPerCall)
	}
	return raw, nil
}

var knownFlags = map[api.Flag]bool{
	api.FlagSeen:      true,
	api.FlagAnswered:  true,
	api.FlagFlagged:   true,
	api.FlagDraft:     true,
	api.FlagDeleted:   true,
	api.FlagJunk:      true,
	api.FlagForwarded: true,
}

// validateFlagLists checks message.flag's set/clear lists: known flags
// only, "deleted" never (message.delete), disjoint, and not both empty.
func validateFlagLists(set, clear []api.Flag) error {
	if len(set) == 0 && len(clear) == 0 {
		return api.NewError(api.CodeInvalidArgument, "nothing to change: set and clear are both empty")
	}
	inSet := make(map[api.Flag]bool, len(set))
	for _, list := range []struct {
		name  string
		flags []api.Flag
	}{{"set", set}, {"clear", clear}} {
		for _, f := range list.flags {
			if !knownFlags[f] {
				return api.NewError(api.CodeInvalidArgument, "%s: unknown flag %q", list.name, f)
			}
			if f == api.FlagDeleted {
				return api.NewError(api.CodeInvalidArgument, "%s: flag deleted is not accepted; use message.delete", list.name)
			}
			if list.name == "set" {
				inSet[f] = true
			} else if inSet[f] {
				return api.NewError(api.CodeInvalidArgument, "flag %q is in both set and clear", f)
			}
		}
	}
	return nil
}

func toAPISummary(m store.Message) api.MessageSummary {
	return api.MessageSummary{
		ID:             api.MessageID(m.ID),
		AccountID:      api.AccountID(m.AccountID),
		FolderID:       api.FolderID(m.FolderID),
		ThreadID:       api.ThreadID(m.ThreadID),
		From:           nonNilAddresses(m.From),
		To:             nonNilAddresses(m.To),
		Subject:        m.Subject,
		Date:           m.Date,
		Snippet:        m.Snippet,
		Flags:          nonNilFlags(m.Flags),
		HasAttachments: m.HasAttachments,
		Size:           m.Size,
	}
}

func toAPIMessage(m store.Message) api.Message {
	out := api.Message{
		MessageSummary: toAPISummary(m),
		CC:             nonNilAddresses(m.CC),
		BCC:            nonNilAddresses(m.BCC),
		ReplyTo:        nonNilAddresses(m.ReplyTo),
		RFCMessageID:   m.RFCMessageID,
		InReplyTo:      m.InReplyTo,
		References:     m.References,
		Attachments:    m.Attachments,
	}
	if out.References == nil {
		out.References = []string{}
	}
	if out.Attachments == nil {
		out.Attachments = []api.Attachment{}
	}
	if len(m.Headers) > 0 {
		out.Headers = m.Headers
	}
	return out
}

// toAPIBodyState maps the store's body state; "none" (not downloaded yet)
// is "pending" on the wire.
func toAPIBodyState(s store.BodyState) api.BodyState {
	switch s {
	case store.BodyFetched:
		return api.BodyFetched
	case store.BodyTooBig:
		return api.BodyTooBig
	case store.BodyFailed:
		return api.BodyFailed
	default:
		return api.BodyPending
	}
}

func nonNilAddresses(a []api.Address) []api.Address {
	if a == nil {
		return []api.Address{}
	}
	return a
}

func nonNilFlags(f []api.Flag) []api.Flag {
	if f == nil {
		return []api.Flag{}
	}
	return f
}
