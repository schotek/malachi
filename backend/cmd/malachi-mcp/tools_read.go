// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/pkg/api"
)

// registerReadTools adds the tools that never change anything.
func (b *bridge) registerReadTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_accounts",
		Description: "List the configured mail accounts: id, name, address, kind and sync status. Call this first; every other tool needs an accountId.",
		Annotations: annRead(),
	}, b.listAccounts)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_folders",
		Description: "List the folders of an account: id, path, role (inbox, sent, drafts, trash, junk, archive, outbox or none), unread and total counts." + untrustedNote,
		Annotations: annRead(),
	}, b.listFolders)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_messages",
		Description: "List messages in a folder, newest first by default: one page of summaries (id, date, from, to, subject, snippet, flags) and a cursor for the next page. Use read_message for a body." + untrustedNote,
		Annotations: annRead(),
	}, b.listMessages)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_messages",
		Description: "Search the mail stored locally: the last offlineDays of every folder (older mail is on the server only and is not searched). " +
			"Scope: folderId (needs accountId) searches that folder; accountId alone every folder of the account except Trash and Junk; neither, every enabled account the same way. " +
			"Every word matches as a prefix, ignoring case and diacritics, and all words must match. " +
			`Syntax: "quoted phrase", from:, to: (To, Cc, Bcc), subject:, has:attachment, is:unread, is:flagged, before:YYYY-MM-DD, after:YYYY-MM-DD, ` +
			"in:<folder path, or inbox, sent, drafts, trash, junk, archive, outbox> (reaches Trash and Junk too). " +
			"Results are newest first, one page and a cursor; snippet is the text around the first match. Use read_message for a body." + untrustedNote,
		Annotations: annRead(),
	}, b.searchMessages)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_message",
		Description: "Read one message: headers, attachment list and the plain-text body (never HTML). Long bodies are paged with offset and maxChars. Reading never marks the message as seen." + untrustedNote,
		Annotations: annRead(),
	}, b.readMessage)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_attachment",
		Description: "Fetch one attachment of a message by the partId from read_message. Text attachments (text/plain, csv, markdown, calendar, json) come back as text, paged with offset and limit; PNG, JPEG, GIF and WebP images come back as an image; any other type (PDF, Office files, archives, HTML, SVG, attached messages) returns metadata only." + untrustedNote,
		Annotations: annRead(),
	}, b.getAttachment)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "sync_status",
		Description: "Report the synchronisation state of one or every account: status, progress, last successful sync, the outbox counts (pendingOutbox: messages still to be delivered; failedOutbox: messages whose delivery failed for good and wait in the outbox for the user) and the last error.",
		Annotations: annRead(),
	}, b.syncStatus)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "trigger_sync",
		Description: "Ask the daemon to synchronise now: every account, one account, or one folder. Returns at once; poll sync_status for progress.",
		Annotations: annTrigger(),
	}, b.triggerSync)
}

// --- list_accounts ---------------------------------------------------------

type noArgs struct{}

type accountOut struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName,omitempty"`
	Kind        string `json:"kind"`
	Enabled     bool   `json:"enabled"`
	Status      string `json:"status"`
}

func (b *bridge) listAccounts(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
	ctx, cancel := b.callCtx(ctx)
	defer cancel()
	res, err := callRPC[api.AccountListResult](ctx, b.rpc, api.MethodAccountList, api.AccountListParams{})
	if err != nil {
		return toolError(err), nil, nil
	}
	// A projection: server settings, token sources and everything else in
	// AccountConfig stay in the daemon.
	out := struct {
		Accounts []accountOut `json:"accounts"`
	}{Accounts: make([]accountOut, 0, len(res.Accounts))}
	for _, a := range res.Accounts {
		out.Accounts = append(out.Accounts, accountOut{
			ID:          string(a.ID),
			Name:        oneLine(a.Config.Name),
			Email:       oneLine(a.Config.Email),
			DisplayName: oneLine(a.Config.DisplayName),
			Kind:        string(a.Config.Protocol()),
			Enabled:     a.Enabled,
			Status:      string(a.State.Status),
		})
	}
	return jsonResult(out), nil, nil
}

// --- list_folders ----------------------------------------------------------

type listFoldersIn struct {
	AccountID           string `json:"accountId" jsonschema:"account id from list_accounts"`
	IncludeUnsubscribed bool   `json:"includeUnsubscribed,omitempty" jsonschema:"also list folders the user unsubscribed from"`
}

type folderOut struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Role       string `json:"role"`
	Unread     int    `json:"unread"`
	Total      int    `json:"total"`
	Subscribed bool   `json:"subscribed"`
	Selectable bool   `json:"selectable"`
	Synced     bool   `json:"synced"`
}

func (b *bridge) listFolders(ctx context.Context, _ *mcp.CallToolRequest, in listFoldersIn) (*mcp.CallToolResult, any, error) {
	if in.AccountID == "" {
		return toolErrorf("accountId is required"), nil, nil
	}
	ctx, cancel := b.callCtx(ctx)
	defer cancel()
	res, err := callRPC[api.FolderListResult](ctx, b.rpc, api.MethodFolderList, api.FolderListParams{
		AccountID: api.AccountID(in.AccountID), IncludeUnsubscribed: in.IncludeUnsubscribed,
	})
	if err != nil {
		return toolError(err), nil, nil
	}
	folders := make([]folderOut, 0, len(res.Folders))
	for _, f := range res.Folders {
		folders = append(folders, folderOut{
			ID: string(f.ID), Path: oneLine(f.Path), Role: string(f.Role),
			Unread: f.Unread, Total: f.Total,
			Subscribed: f.Subscribed, Selectable: f.Selectable, Synced: f.Synced,
		})
	}
	body, err := marshalIndent(folders)
	if err != nil {
		return toolErrorf("internal error: %v", err), nil, nil
	}
	head := fmt.Sprintf("account %s: %d folders (paths are server-supplied names)", in.AccountID, len(folders))
	return textResult(head + "\n" + fenced(newNonce(), body)), nil, nil
}

// --- list_messages ---------------------------------------------------------

type listMessagesIn struct {
	AccountID string `json:"accountId" jsonschema:"account id from list_accounts"`
	FolderID  string `json:"folderId" jsonschema:"folder id from list_folders"`
	Limit     int    `json:"limit,omitempty" jsonschema:"messages per page, 1-100, default 20"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"nextCursor of the previous page"`
	Filter    string `json:"filter,omitempty" jsonschema:"all (default), unread or flagged"`
	Sort      string `json:"sort,omitempty" jsonschema:"dateDesc (default) or dateAsc"`
}

type outboxOut struct {
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error,omitempty"`
}

type messageOut struct {
	ID             string     `json:"id"`
	Date           string     `json:"date"`
	From           []string   `json:"from"`
	To             []string   `json:"to,omitempty"`
	Subject        string     `json:"subject"`
	Snippet        string     `json:"snippet"`
	Flags          []string   `json:"flags"`
	HasAttachments bool       `json:"hasAttachments"`
	Size           int64      `json:"size"`
	Outbox         *outboxOut `json:"outbox,omitempty"`
}

func (b *bridge) listMessages(ctx context.Context, _ *mcp.CallToolRequest, in listMessagesIn) (*mcp.CallToolResult, any, error) {
	if in.AccountID == "" || in.FolderID == "" {
		return toolErrorf("accountId and folderId are required"), nil, nil
	}
	ctx, cancel := b.callCtx(ctx)
	defer cancel()
	res, err := callRPC[api.MessageListResult](ctx, b.rpc, api.MethodMessageList, api.MessageListParams{
		AccountID: api.AccountID(in.AccountID),
		FolderID:  api.FolderID(in.FolderID),
		Page:      api.Page{Cursor: in.Cursor, Limit: clampLimit(in.Limit, defaultListLimit, maxListLimit)},
		Sort:      api.SortOrder(in.Sort),
		Filter:    api.MessageFilter(in.Filter),
	})
	if err != nil {
		return toolError(err), nil, nil
	}
	msgs := make([]messageOut, 0, len(res.Messages))
	for _, m := range res.Messages {
		msgs = append(msgs, summaryOut(m))
	}
	body, err := marshalIndent(msgs)
	if err != nil {
		return toolErrorf("internal error: %v", err), nil, nil
	}
	head := fmt.Sprintf("folder %s of account %s: %d messages on this page, %d in the folder",
		in.FolderID, in.AccountID, len(msgs), res.Page.Total)
	if res.Page.NextCursor != "" {
		head += "\nnext page: call again with cursor=" + res.Page.NextCursor
	} else {
		head += "\nlast page"
	}
	return textResult(head + "\n" + fenced(newNonce(), body)), nil, nil
}

// --- search_messages -------------------------------------------------------

type searchMessagesIn struct {
	Query     string `json:"query" jsonschema:"what to search for, in the syntax of the tool description"`
	AccountID string `json:"accountId,omitempty" jsonschema:"account id from list_accounts; empty searches every enabled account"`
	FolderID  string `json:"folderId,omitempty" jsonschema:"folder id from list_folders; needs accountId"`
	Limit     int    `json:"limit,omitempty" jsonschema:"results per page, 1-100, default 20"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"nextCursor of the previous page, with the same query and scope"`
}

// searchResultOut is a list_messages record plus where the message lies,
// since a search spans folders and accounts.
type searchResultOut struct {
	messageOut
	AccountID string `json:"accountId"`
	FolderID  string `json:"folderId"`
	Folder    string `json:"folder,omitempty"`
}

func (b *bridge) searchMessages(ctx context.Context, _ *mcp.CallToolRequest, in searchMessagesIn) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Query) == "" {
		return toolErrorf("query is required"), nil, nil
	}
	if in.FolderID != "" && in.AccountID == "" {
		return toolErrorf("folderId needs accountId"), nil, nil
	}
	ctx, cancel := b.callCtx(ctx)
	defer cancel()
	res, err := callRPC[api.SearchQueryResult](ctx, b.rpc, api.MethodSearchQuery, api.SearchQueryParams{
		AccountID: api.AccountID(in.AccountID),
		FolderID:  api.FolderID(in.FolderID),
		Query:     in.Query,
		Page:      api.Page{Cursor: in.Cursor, Limit: clampLimit(in.Limit, defaultListLimit, maxListLimit)},
	})
	if err != nil {
		return toolError(err), nil, nil
	}
	paths := b.folderPaths(ctx, res.Results)
	out := make([]searchResultOut, 0, len(res.Results))
	for _, r := range res.Results {
		m := summaryOut(r.Message)
		m.Snippet = oneLine(r.Snippet) // the excerpt; its match ranges mean nothing to a model
		out = append(out, searchResultOut{messageOut: m, AccountID: string(r.Message.AccountID),
			FolderID: string(r.Message.FolderID), Folder: paths[r.Message.FolderID]})
	}
	body, err := marshalIndent(out)
	if err != nil {
		return toolErrorf("internal error: %v", err), nil, nil
	}
	// The header is trusted text: it never repeats the query.
	total := fmt.Sprint(res.Page.Total)
	if res.Page.Total < 0 {
		total = fmt.Sprintf("more than %d", api.MaxSearchTotal)
	}
	head := fmt.Sprintf("search of the locally stored mail: %d results on this page, %s in all", len(out), total)
	if res.Page.NextCursor != "" {
		head += "\nnext page: call again with the same query and cursor=" + res.Page.NextCursor
	} else {
		head += "\nlast page"
	}
	return textResult(head + "\n" + fenced(newNonce(), body)), nil, nil
}

// folderPaths looks up the path of the folders the results lie in, one
// folder.list per account; an account whose list fails just goes without.
func (b *bridge) folderPaths(ctx context.Context, results []api.SearchResult) map[api.FolderID]string {
	paths := map[api.FolderID]string{}
	seen := map[api.AccountID]bool{}
	for _, r := range results {
		acc := r.Message.AccountID
		if seen[acc] {
			continue
		}
		seen[acc] = true
		res, err := callRPC[api.FolderListResult](ctx, b.rpc, api.MethodFolderList, api.FolderListParams{AccountID: acc})
		if err != nil {
			continue
		}
		for _, f := range res.Folders {
			paths[f.ID] = oneLine(f.Path)
		}
	}
	return paths
}

func summaryOut(m api.MessageSummary) messageOut {
	out := messageOut{
		ID:             string(m.ID),
		Date:           formatTime(m.Date),
		From:           formatAddresses(m.From),
		To:             formatAddresses(m.To),
		Subject:        oneLine(m.Subject),
		Snippet:        oneLine(m.Snippet),
		Flags:          flagNames(m.Flags),
		HasAttachments: m.HasAttachments,
		Size:           m.Size,
	}
	if m.Outbox != nil {
		out.Outbox = &outboxOut{State: string(m.Outbox.State), Attempts: m.Outbox.Attempts}
		if m.Outbox.Error != nil {
			out.Outbox.Error = m.Outbox.Error.Code.String()
		}
	}
	return out
}

// --- read_message ----------------------------------------------------------

type readMessageIn struct {
	AccountID      string `json:"accountId" jsonschema:"account id from list_accounts"`
	MessageID      string `json:"messageId" jsonschema:"message id from list_messages"`
	Offset         int    `json:"offset,omitempty" jsonschema:"character offset into the body for paging, default 0"`
	MaxChars       int    `json:"maxChars,omitempty" jsonschema:"characters of body to return, default 16000, max 64000"`
	IncludeLinks   bool   `json:"includeLinks,omitempty" jsonschema:"also list the real targets of the links in the message (at most 50)"`
	IncludeHeaders bool   `json:"includeHeaders,omitempty" jsonschema:"also list the curated extra headers (List-Unsubscribe and the like)"`
}

func (b *bridge) readMessage(ctx context.Context, _ *mcp.CallToolRequest, in readMessageIn) (*mcp.CallToolResult, any, error) {
	if in.AccountID == "" || in.MessageID == "" {
		return toolErrorf("accountId and messageId are required"), nil, nil
	}
	ctx, cancel := b.callCtx(ctx)
	defer cancel()
	acc, mid := api.AccountID(in.AccountID), api.MessageID(in.MessageID)
	got, err := callRPC[api.MessageGetResult](ctx, b.rpc, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: mid})
	if err != nil {
		return toolError(err), nil, nil
	}
	// Remote content is always blocked: the daemon must never fetch anything
	// because an agent read a message.
	body, err := callRPC[api.MessageBodyResult](ctx, b.rpc, api.MethodMessageBody, api.MessageBodyParams{
		AccountID: acc, MessageID: mid, RemoteContent: api.RemoteBlock,
	})
	if err != nil {
		return toolError(err), nil, nil
	}
	m := got.Message

	// Only Text is ever read from the body result. The sanitised HTML is for
	// the desktop webview; an agent gets plain text (CLAUDE.md rule 2,
	// docs/security.md).
	text := clean(body.Text)
	slice, total, end := truncateRunes(text, in.Offset, clampLimit(in.MaxChars, defaultBodyChars, maxBodyChars))

	var sb strings.Builder
	fmt.Fprintf(&sb, "id: %s\naccount: %s\nfolder: %s\ndate: %s\nflags: %s\nsize: %d\n",
		m.ID, m.AccountID, m.FolderID, formatTime(m.Date), strings.Join(flagNames(m.Flags), ", "), m.Size)
	switch body.BodyState {
	case api.BodyPending:
		sb.WriteString("body-state: pending (not downloaded yet; call trigger_sync and read again later)\n")
	case api.BodyTooBig:
		sb.WriteString("body-state: tooBig (over the daemon's size cap; the body is not available)\n")
	case api.BodyFailed:
		sb.WriteString("body-state: failed (the message could not be parsed; only headers are available)\n")
	default:
		sb.WriteString("body-state: fetched\n")
	}
	if body.HTMLWithheld {
		sb.WriteString("html-withheld: true (the HTML part could not be shown safely; the text rendering is shown instead)\n")
	}
	start := in.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	switch {
	case total == 0:
		sb.WriteString("body: empty\n")
	case end < total:
		fmt.Fprintf(&sb, "body: chars %d-%d of %d (truncated; call again with offset=%d)\n", start, end, total, end)
	default:
		fmt.Fprintf(&sb, "body: chars %d-%d of %d\n", start, end, total)
	}

	var u strings.Builder
	fmt.Fprintf(&u, "from: %s\n", joinAddresses(m.From))
	fmt.Fprintf(&u, "to: %s\n", joinAddresses(m.To))
	if len(m.CC) > 0 {
		fmt.Fprintf(&u, "cc: %s\n", joinAddresses(m.CC))
	}
	if len(m.BCC) > 0 {
		fmt.Fprintf(&u, "bcc: %s\n", joinAddresses(m.BCC))
	}
	if len(m.ReplyTo) > 0 {
		fmt.Fprintf(&u, "reply-to: %s\n", joinAddresses(m.ReplyTo))
	}
	fmt.Fprintf(&u, "subject: %s\n", oneLine(m.Subject))
	if len(m.Attachments) == 0 {
		u.WriteString("attachments: none\n")
	} else {
		u.WriteString("attachments:\n")
		for _, a := range m.Attachments {
			fmt.Fprintf(&u, "  - partId=%s filename=%q type=%s size=%d", a.PartID, oneLine(a.Filename), oneLine(a.ContentType), a.Size)
			if a.Inline {
				u.WriteString(" inline")
			}
			u.WriteString("\n")
		}
	}
	if in.IncludeHeaders && len(m.Headers) > 0 {
		u.WriteString("headers:\n")
		for k, v := range m.Headers {
			fmt.Fprintf(&u, "  %s: %s\n", oneLine(k), oneLine(v))
		}
	}
	if in.IncludeLinks && len(body.Links) > 0 {
		u.WriteString("links:\n")
		for i, l := range body.Links {
			if i == maxLinks {
				fmt.Fprintf(&u, "  (%d more not listed)\n", len(body.Links)-maxLinks)
				break
			}
			fmt.Fprintf(&u, "  - %s -> %s\n", oneLine(l.Text), oneLine(l.Href))
		}
	}
	u.WriteString("body:\n")
	u.WriteString(slice)

	sb.WriteString(fenced(newNonce(), u.String()))
	return textResult(sb.String()), nil, nil
}

// --- get_attachment --------------------------------------------------------

type getAttachmentIn struct {
	AccountID string `json:"accountId" jsonschema:"account id from list_accounts"`
	MessageID string `json:"messageId" jsonschema:"message id from list_messages"`
	PartID    string `json:"partId" jsonschema:"partId from the attachment list of read_message"`
	Offset    int    `json:"offset,omitempty" jsonschema:"byte offset into a text attachment for paging, default 0"`
	Limit     int    `json:"limit,omitempty" jsonschema:"bytes of a text attachment to return, default 65536, max 262144"`
}

var textAttachmentTypes = map[string]bool{
	"text/plain":                true,
	"text/csv":                  true,
	"text/tab-separated-values": true,
	"text/markdown":             true,
	"text/calendar":             true,
	"application/json":          true,
}

var imageAttachmentTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// mediaType lower-cases a content type and drops its parameters.
func mediaType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

func (b *bridge) getAttachment(ctx context.Context, _ *mcp.CallToolRequest, in getAttachmentIn) (*mcp.CallToolResult, any, error) {
	if in.AccountID == "" || in.MessageID == "" || in.PartID == "" {
		return toolErrorf("accountId, messageId and partId are required"), nil, nil
	}
	ctx, cancel := b.callCtx(ctx)
	defer cancel()
	acc, mid := api.AccountID(in.AccountID), api.MessageID(in.MessageID)

	// The part must be one the message lists as an attachment, so that the
	// decision about fetching it can be taken from the declared type and
	// size before a byte is read.
	got, err := callRPC[api.MessageGetResult](ctx, b.rpc, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: mid})
	if err != nil {
		return toolError(err), nil, nil
	}
	var att *api.Attachment
	for i := range got.Message.Attachments {
		if got.Message.Attachments[i].PartID == in.PartID {
			att = &got.Message.Attachments[i]
			break
		}
	}
	if att == nil {
		return toolErrorf("no attachment with partId %q on message %s; see the attachment list of read_message", in.PartID, in.MessageID), nil, nil
	}
	declared := mediaType(att.ContentType)
	meta := fmt.Sprintf("partId=%s filename=%q contentType=%s size=%d (the name is untrusted mail content)",
		att.PartID, oneLine(att.Filename), declared, att.Size)
	withheld := func(reason string) *mcp.CallToolResult {
		return textResult(meta + "\ncontent not returned: " + reason + "; the user can open it in Malachi Mail")
	}

	var kind string
	switch {
	case textAttachmentTypes[declared]:
		kind = "text"
		if att.Size > maxAttachmentTextBytes {
			return withheld(fmt.Sprintf("too big: %d bytes, limit %d", att.Size, maxAttachmentTextBytes)), nil, nil
		}
	case imageAttachmentTypes[declared]:
		kind = "image"
		if att.Size > maxAttachmentImageBytes {
			return withheld(fmt.Sprintf("too big: %d bytes, limit %d", att.Size, maxAttachmentImageBytes)), nil, nil
		}
	case declared == "text/html":
		return withheld("HTML attachments are never returned"), nil, nil
	case declared == "image/svg+xml":
		return withheld("SVG is never returned"), nil, nil
	default:
		return withheld("unsupported type " + declared), nil, nil
	}

	res, err := callRPC[api.MessagePartResult](ctx, b.rpc, api.MethodMessagePart, api.MessagePartParams{
		AccountID: acc, MessageID: mid, PartID: in.PartID,
	})
	if err != nil {
		return toolError(err), nil, nil
	}
	// The daemon reports the declared type; the bytes decide here.
	sniffed := mediaType(http.DetectContentType(res.Data))

	if kind == "image" {
		if len(res.Data) > maxAttachmentImageBytes {
			return withheld(fmt.Sprintf("too big: %d bytes, limit %d", len(res.Data), maxAttachmentImageBytes)), nil, nil
		}
		if sniffed != declared {
			return withheld(fmt.Sprintf("content does not look like %s (detected %s)", declared, sniffed)), nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: meta},
			&mcp.ImageContent{Data: res.Data, MIMEType: sniffed},
		}}, nil, nil
	}

	if len(res.Data) > maxAttachmentTextBytes {
		return withheld(fmt.Sprintf("too big: %d bytes, limit %d", len(res.Data), maxAttachmentTextBytes)), nil, nil
	}
	if !strings.HasPrefix(sniffed, "text/") || sniffed == "text/html" || bytes.IndexByte(res.Data, 0) >= 0 {
		return withheld(fmt.Sprintf("content does not look like text (detected %s)", sniffed)), nil, nil
	}
	raw := string(res.Data)
	before := strings.Count(raw, string(utf8.RuneError))
	valid := strings.ToValidUTF8(raw, string(utf8.RuneError))
	replaced := strings.Count(valid, string(utf8.RuneError)) - before
	if runes := utf8.RuneCountInString(valid); runes > 0 && replaced*100 > runes*maxReplacedPercent {
		return withheld(fmt.Sprintf("not text: %d of %d characters are not valid UTF-8", replaced, runes)), nil, nil
	}
	text := clean(valid)
	slice, total, end := sliceBytes(text, in.Offset, clampLimit(in.Limit, defaultAttachmentTextBytes, maxAttachmentTextBytes))
	start := in.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	if end < total {
		meta += fmt.Sprintf("\ntext: bytes %d-%d of %d (truncated; call again with offset=%d)", start, end, total, end)
	} else {
		meta += fmt.Sprintf("\ntext: bytes %d-%d of %d", start, end, total)
	}
	if replaced > 0 {
		meta += fmt.Sprintf("\nreplacedBytes: %d (invalid UTF-8 shown as U+FFFD)", replaced)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: meta},
		&mcp.TextContent{Text: fenced(newNonce(), slice)},
	}}, nil, nil
}

// --- sync_status / trigger_sync -------------------------------------------

type syncStatusIn struct {
	AccountID string `json:"accountId,omitempty" jsonschema:"account id; omit for every account"`
}

type syncStateOut struct {
	AccountID     string `json:"accountId"`
	Status        string `json:"status"`
	FolderID      string `json:"folderId,omitempty"`
	Progress      int    `json:"progress"`
	LastSync      string `json:"lastSync,omitempty"`
	PendingOutbox int    `json:"pendingOutbox"`
	FailedOutbox  int    `json:"failedOutbox"`
	Error         string `json:"error,omitempty"`
}

func (b *bridge) syncStatus(ctx context.Context, _ *mcp.CallToolRequest, in syncStatusIn) (*mcp.CallToolResult, any, error) {
	ctx, cancel := b.callCtx(ctx)
	defer cancel()
	res, err := callRPC[api.SyncStatusResult](ctx, b.rpc, api.MethodSyncStatus, api.SyncStatusParams{AccountID: api.AccountID(in.AccountID)})
	if err != nil {
		return toolError(err), nil, nil
	}
	out := struct {
		Accounts []syncStateOut `json:"accounts"`
	}{Accounts: make([]syncStateOut, 0, len(res.Accounts))}
	for _, s := range res.Accounts {
		o := syncStateOut{
			AccountID: string(s.AccountID), Status: string(s.Status), FolderID: string(s.FolderID),
			Progress: s.Progress, LastSync: formatTimePtr(s.LastSync), PendingOutbox: s.PendingOutbox,
			FailedOutbox: s.FailedOutbox,
		}
		if s.Error != nil {
			o.Error = s.Error.Code.String() + ": " + truncateBytes(oneLine(s.Error.Message), maxErrorMessageBytes)
		}
		out.Accounts = append(out.Accounts, o)
	}
	return jsonResult(out), nil, nil
}

type triggerSyncIn struct {
	AccountID string `json:"accountId,omitempty" jsonschema:"account id; omit for every account"`
	FolderID  string `json:"folderId,omitempty" jsonschema:"folder id; needs accountId"`
	Full      bool   `json:"full,omitempty" jsonschema:"walk every folder even when nothing seems changed"`
}

func (b *bridge) triggerSync(ctx context.Context, _ *mcp.CallToolRequest, in triggerSyncIn) (*mcp.CallToolResult, any, error) {
	ctx, cancel := b.callCtx(ctx)
	defer cancel()
	_, err := callRPC[api.SyncTriggerResult](ctx, b.rpc, api.MethodSyncTrigger, api.SyncTriggerParams{
		AccountID: api.AccountID(in.AccountID), FolderID: api.FolderID(in.FolderID), Full: in.Full,
	})
	if err != nil {
		return toolError(err), nil, nil
	}
	scope := "every account"
	if in.AccountID != "" {
		scope = "account " + in.AccountID
		if in.FolderID != "" {
			scope += ", folder " + in.FolderID
		}
	}
	return textResult(fmt.Sprintf("sync triggered for %s (full=%t); poll sync_status for progress", scope, in.Full)), nil, nil
}
