// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/schotek/malachi/backend/cmd/malachi-mcp/internal/extract"
	"github.com/schotek/malachi/backend/pkg/api"
)

// registerReadTools adds the tools that never change anything.
func (b *bridge) registerReadTools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_accounts",
		Description: "List the configured mail accounts: id, name, address, kind, sync status and capabilities (what create_draft can do with the account: compose, reply, replyAll, forward; comment for an issue tracker, whose reply is a comment on the issue). Call this first; every other tool needs an accountId.",
		Annotations: annRead(),
	}, b.listAccounts)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_folders",
		Description: "List the folders of an account: id, path, role (inbox, sent, drafts, trash, junk, archive, outbox or none), unread and total counts." + untrustedNote,
		Annotations: annRead(),
	}, b.listFolders)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_messages",
		Description: "List messages in a folder, newest first by default: one page of summaries (id, date, from, to, subject, snippet, flags; for an issue-tracker account (kind jira) also issue: key, status, and item: description, comment or event) and a cursor for the next page. Use read_message for a body." + untrustedNote,
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
		Name: "read_message",
		Description: "Read one message: headers, attachment list and the plain-text body (never HTML); for an issue-tracker account (kind jira) also the issue's key and status and what part of the issue the message is. Long bodies are paged with offset and maxChars. Reading never marks the message as seen. " +
			"An attachment marked remote is kept on the mail server only; get_attachment downloads it." + untrustedNote,
		Annotations: annRead(),
	}, b.readMessage)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_attachment",
		Description: "Fetch one attachment of a message by the partId from read_message. Text attachments (text/plain, csv, markdown, calendar, json) come back as text, paged with offset and limit. " +
			"PDF, Word (.docx) and Excel (.xlsx) documents come back as their extracted text, paged the same way: PDF pages start with a '--- page N ---' line; " +
			"a spreadsheet gives a line per sheet and one line per row (the row number, then the cells from column A, tab-separated), formulas as their last calculated value and dates as YYYY-MM-DD; " +
			"Word comments, notes, headers and footers follow the body; hidden text is included. " +
			"A part sent as application/octet-stream counts as a document when its name ends in .pdf, .docx or .xlsx. " +
			"There is no OCR, so a scanned PDF has no text, and a password-protected document returns metadata only. " +
			"PNG, JPEG, GIF and WebP images come back as an image; any other type (older .doc and .xls, PowerPoint, archives, HTML, SVG, attached messages) returns metadata only. " +
			"An attachment of a type that is returned but kept on the mail server only (remote) is downloaded from the account's own mail server first, which can take up to two minutes." + untrustedNote,
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
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Email        string   `json:"email"`
	DisplayName  string   `json:"displayName,omitempty"`
	Kind         string   `json:"kind"`
	Enabled      bool     `json:"enabled"`
	Status       string   `json:"status"`
	Capabilities []string `json:"capabilities"`
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
			ID:           string(a.ID),
			Name:         oneLine(a.Config.Name),
			Email:        oneLine(a.Config.Email),
			DisplayName:  oneLine(a.Config.DisplayName),
			Kind:         string(a.Config.Protocol()),
			Enabled:      a.Enabled,
			Status:       string(a.State.Status),
			Capabilities: capabilitiesOf(a),
		})
	}
	return jsonResult(out), nil, nil
}

// capabilitiesOf lists the account's capabilities (a daemon that sends
// none means the mail set).
func capabilitiesOf(a api.Account) []string {
	caps := a.Capabilities
	if caps == nil {
		caps = api.MailCapabilities
	}
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, oneLine(string(c)))
	}
	return out
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

// issueOut is the issue of a message of an issue-tracker account: its key
// and status (display text of the site, cleaned like all mail data) and
// what part of the issue the message is.
type issueOut struct {
	Key    string `json:"key"`
	Status string `json:"status,omitempty"`
	Item   string `json:"item,omitempty"`
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
	Issue          *issueOut  `json:"issue,omitempty"`
	Bulk           *bulkOut   `json:"bulk,omitempty"`
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
	out.Issue = issueOf(m.Issue)
	out.Bulk = bulkOf(m.Bulk)
	return out
}

// issueOf projects MessageSummary.issue; nil for mail.
func issueOf(is *api.MessageIssue) *issueOut {
	if is == nil {
		return nil
	}
	return &issueOut{Key: oneLine(is.Key), Status: oneLine(is.Status), Item: oneLine(string(is.Item))}
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
	if n := remoteCount(m.Attachments); n > 0 {
		fmt.Fprintf(&sb, "remote-attachments: %d (on the mail server only; get_attachment downloads them first)\n", n)
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
	if is := issueOf(m.Issue); is != nil {
		fmt.Fprintf(&u, "issue: %s\nissue-status: %s\nissue-item: %s\n", is.Key, is.Status, is.Item)
	}
	bulkLines(&u, m)
	if len(m.Attachments) == 0 {
		u.WriteString("attachments: none\n")
	} else {
		u.WriteString("attachments:\n")
		for _, a := range m.Attachments {
			fmt.Fprintf(&u, "  - partId=%s filename=%q type=%s size=%d", a.PartID, oneLine(a.Filename), oneLine(a.ContentType), a.Size)
			if a.Inline {
				u.WriteString(" inline")
			}
			if a.Remote {
				u.WriteString(" remote")
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
	Offset    int    `json:"offset,omitempty" jsonschema:"byte offset into the text of a text attachment or document for paging, default 0"`
	Limit     int    `json:"limit,omitempty" jsonschema:"bytes of the text of a text attachment or document to return, default 65536, max 262144"`
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

// documentTypes are the declared types of the documents whose text is
// returned. Everything else of the kind (.doc and .xls, PowerPoint,
// OpenDocument, RTF, macro-enabled files, templates) is withheld.
var documentTypes = map[string]extract.Format{
	"application/pdf": extract.PDF,
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": extract.DOCX,
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":       extract.XLSX,
}

// documentByName are the declared types that mail programs put on a
// document they did not label properly: a generic type, or a known wrong
// one. Such a part counts as a document when the last extension of its
// name is one of its row here, which names exactly one format; the bytes
// then have to be that format (checkDocumentBytes, and the worker).
var documentByName = map[string]map[string]extract.Format{
	"application/octet-stream":     {".pdf": extract.PDF, ".docx": extract.DOCX, ".xlsx": extract.XLSX},
	"application/x-pdf":            {".pdf": extract.PDF},
	"application/msword":           {".docx": extract.DOCX},
	"application/vnd.ms-excel":     {".xlsx": extract.XLSX},
	"application/zip":              {".docx": extract.DOCX, ".xlsx": extract.XLSX},
	"application/x-zip-compressed": {".docx": extract.DOCX, ".xlsx": extract.XLSX},
}

// attachmentPlan is how an attachment is returned, decided before a byte
// of it is fetched.
type attachmentPlan struct {
	kind   string         // "text", "image" or "document"
	format extract.Format // of a document
	byName bool           // a document whose format comes from its file name
}

// attachmentKind decides from the declared type and size, and for a
// document in a generic or wrong type from its file name, how an
// attachment is returned; a plan without a kind comes with the reason it
// is withheld.
func attachmentKind(declared, filename string, size int64) (attachmentPlan, string) {
	tooBig := func(limit int) string { return fmt.Sprintf("too big: %d bytes, limit %d", size, limit) }
	switch {
	case textAttachmentTypes[declared]:
		if size > maxAttachmentTextBytes {
			return attachmentPlan{}, tooBig(maxAttachmentTextBytes)
		}
		return attachmentPlan{kind: "text"}, ""
	case imageAttachmentTypes[declared]:
		if size > maxAttachmentImageBytes {
			return attachmentPlan{}, tooBig(maxAttachmentImageBytes)
		}
		return attachmentPlan{kind: "image"}, ""
	case declared == "text/html":
		return attachmentPlan{}, "HTML attachments are never returned"
	case declared == "image/svg+xml":
		return attachmentPlan{}, "SVG is never returned"
	}
	plan := attachmentPlan{kind: "document"}
	if f, ok := documentTypes[declared]; ok {
		plan.format = f
	} else if f, ok := documentByName[declared][fileExtension(filename)]; ok {
		plan.format, plan.byName = f, true
	} else {
		return attachmentPlan{}, "unsupported type " + declared
	}
	if size > maxAttachmentDocumentBytes {
		return attachmentPlan{}, tooBig(maxAttachmentDocumentBytes)
	}
	return plan, ""
}

// fileExtension is the last extension of a file name as it is shown (made
// clean and one line), with only its ASCII letters lower-cased, so that no
// other character can turn into one of them (U+212A KELVIN SIGN is no "k",
// U+017F LATIN SMALL LETTER LONG S no "s", as they would be for
// strings.ToLower or EqualFold); "" for a name without one.
func fileExtension(name string) string {
	name = oneLine(name)
	i := strings.LastIndexByte(name, '.')
	if i < 0 || strings.ContainsAny(name[i:], `/\`) {
		return ""
	}
	return asciiLower(name[i:])
}

// asciiLower lower-cases the ASCII letters of s and nothing else.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// The signatures the bytes of a document must start with.
var (
	pdfSignature = []byte("%PDF-")
	zipSignature = []byte("PK\x03\x04")
	cfbSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
)

// checkDocumentBytes is the check of a fetched document's bytes before a
// worker reads them (the worker checks them again): "" when they may be
// format f, else why the document is withheld. A PDF starts with %PDF-,
// a DOCX or XLSX is a ZIP; an OLE2 compound file in their place is an
// Office file with a password or a .doc or .xls, and goes no further.
func checkDocumentBytes(f extract.Format, data []byte) string {
	switch f {
	case extract.PDF:
		if bytes.HasPrefix(data, pdfSignature) {
			return ""
		}
	case extract.DOCX, extract.XLSX:
		if bytes.HasPrefix(data, cfbSignature) {
			return officeCFBReason
		}
		if bytes.HasPrefix(data, zipSignature) {
			return ""
		}
	}
	return fmt.Sprintf("content does not look like %s (detected %s)", aFormat(f), mediaType(http.DetectContentType(data)))
}

// listedElsewhere reports whether a part other than want's id has want's
// file name, type and size.
func listedElsewhere(atts []api.Attachment, want api.Attachment) bool {
	for _, a := range atts {
		if a.PartID != want.PartID && a.Filename == want.Filename &&
			mediaType(a.ContentType) == mediaType(want.ContentType) && a.Size == want.Size {
			return true
		}
	}
	return false
}

// findAttachment returns the attachment with the part id, nil if none.
func findAttachment(atts []api.Attachment, partID string) *api.Attachment {
	for i := range atts {
		if atts[i].PartID == partID {
			return &atts[i]
		}
	}
	return nil
}

// remoteCount counts the attachments kept on the mail server only.
func remoteCount(atts []api.Attachment) int {
	n := 0
	for _, a := range atts {
		if a.Remote {
			n++
		}
	}
	return n
}

// codeOf is the daemon's error code of err, 0 for none.
func codeOf(err error) api.ErrorCode {
	var e *api.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// mediaType lower-cases a content type and drops its parameters.
func mediaType(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}

func (b *bridge) getAttachment(ctx context.Context, req *mcp.CallToolRequest, in getAttachmentIn) (*mcp.CallToolResult, any, error) {
	if in.AccountID == "" || in.MessageID == "" || in.PartID == "" {
		return toolErrorf("accountId, messageId and partId are required"), nil, nil
	}
	acc, mid := api.AccountID(in.AccountID), api.MessageID(in.MessageID)

	// The part must be one the message lists as an attachment, so that the
	// decision about fetching it can be taken from the declared type and
	// size before a byte is read or downloaded.
	getCtx, cancel := b.callCtx(ctx)
	got, err := callRPC[api.MessageGetResult](getCtx, b.rpc, api.MethodMessageGet, api.MessageGetParams{AccountID: acc, MessageID: mid})
	cancel()
	if err != nil {
		return toolError(err), nil, nil
	}
	att := findAttachment(got.Message.Attachments, in.PartID)
	if att == nil {
		return toolErrorf("no attachment with partId %q on message %s; see the attachment list of read_message", in.PartID, in.MessageID), nil, nil
	}
	declared := mediaType(att.ContentType)
	meta := fmt.Sprintf("partId=%s filename=%q contentType=%s size=%d (the name is untrusted mail content)",
		att.PartID, oneLine(att.Filename), declared, att.Size)
	withheld := func(reason string) *mcp.CallToolResult { return withheldResult(meta, reason) }
	plan, reason := attachmentKind(declared, att.Filename, att.Size)
	if plan.kind == "" {
		return withheld(reason), nil, nil
	}

	// A document read before is answered from the cache: no message.part,
	// no download, no worker. One that another call is reading is answered
	// with that call's outcome, and when too many are being read, busy
	// (doc_cache.go); when that reading ends without an outcome, this call
	// starts over. Else this call reads it, and the calls for it that come
	// meanwhile wait for its outcome: outcome, set once there is one. It
	// is cached under cacheKey: key, or after a download the part as the
	// message then lists it.
	key := docKey{acc: acc, msg: mid, part: in.PartID, filename: att.Filename, contentType: declared, size: att.Size}
	cacheKey := key
	var outcome *docEntry
	if plan.kind == "document" {
		e, r, again, err := b.awaitDocument(ctx, key, plan.format)
		switch {
		case err != nil:
			return toolErrorf("cancelled"), nil, nil
		case again:
			// The message may have been rebuilt meanwhile: its listing
			// is read anew.
			return b.getAttachment(ctx, req, in)
		case r == nil:
			return documentResult(meta, plan, e, in), nil, nil
		}
		defer func() { b.docs.end(key, r, outcome) }()
	}

	// The part is asked for first, also one message.get calls remote: the
	// daemon may hold its message in memory (neverStoreAttachments) and
	// serve it without a download. Only when it answers that the part is
	// on the mail server only (remote, or left there by the background
	// pass after message.get read the message) is the message downloaded,
	// and the part asked for once more.
	downloaded := false
	fetch := func() *mcp.CallToolResult {
		m, fail := b.download(ctx, acc, got.Message.MessageSummary)
		if fail != nil {
			return toolErrorf("%s", fail.text)
		}
		downloaded = true
		// Microsoft 365 rebuilds a message it serves again: the part must
		// still be the one the model asked for. Its size may change with
		// the rebuild (the daemon, too, takes the only part of a name and
		// type for it), but not when another part now has the name, type
		// and size it was listed with: then that one is it.
		now := findAttachment(m.Attachments, in.PartID)
		if now == nil || now.Filename != att.Filename || mediaType(now.ContentType) != declared ||
			(now.Size != att.Size && listedElsewhere(m.Attachments, *att)) {
			return toolErrorf("the mail server rebuilt message %s when it was downloaded and its part ids changed; call read_message again for the new ones", in.MessageID)
		}
		if p, reason := attachmentKind(declared, now.Filename, now.Size); p.kind == "" {
			return withheld(reason)
		}
		// What is read now is the part as the message lists it from now
		// on, which is what a later call's key will say.
		cacheKey.size = now.Size
		return nil
	}
	part := func() (*api.MessagePartResult, error) {
		partCtx, cancel := b.callCtx(ctx)
		defer cancel()
		return callRPC[api.MessagePartResult](partCtx, b.rpc, api.MethodMessagePart, api.MessagePartParams{
			AccountID: acc, MessageID: mid, PartID: in.PartID,
		})
	}
	res, err := part()
	if codeOf(err) == api.CodePartNotDownloaded {
		if res := fetch(); res != nil {
			return res, nil, nil
		}
		res, err = part()
	}
	if err != nil {
		return toolError(err), nil, nil
	}
	if downloaded {
		meta += "\ndownloaded: fetched from the mail server first"
	}

	switch plan.kind {
	case "document":
		e, cacheable, cancelled := b.readDocument(ctx, plan.format, res.Data)
		if cancelled {
			return toolErrorf("cancelled"), nil, nil
		}
		if cacheable {
			b.docs.put(cacheKey, e)
		}
		outcome = &e
		return documentResult(meta, plan, e, in), nil, nil
	case "image":
		// The daemon reports the declared type; the bytes decide here.
		sniffed := mediaType(http.DetectContentType(res.Data))
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
	if sniffed := mediaType(http.DetectContentType(res.Data)); !strings.HasPrefix(sniffed, "text/") || sniffed == "text/html" || bytes.IndexByte(res.Data, 0) >= 0 {
		return withheld(fmt.Sprintf("content does not look like text (detected %s)", sniffed)), nil, nil
	}
	raw := string(res.Data)
	before := strings.Count(raw, string(utf8.RuneError))
	valid := strings.ToValidUTF8(raw, string(utf8.RuneError))
	replaced := strings.Count(valid, string(utf8.RuneError)) - before
	if runes := utf8.RuneCountInString(valid); runes > 0 && replaced*100 > runes*maxReplacedPercent {
		return withheld(fmt.Sprintf("not text: %d of %d characters are not valid UTF-8", replaced, runes)), nil, nil
	}
	trailing := ""
	if replaced > 0 {
		trailing = fmt.Sprintf("replacedBytes: %d (invalid UTF-8 shown as U+FFFD)", replaced)
	}
	return pagedText(meta, clean(valid), in.Offset, in.Limit, trailing), nil, nil
}

// withheldResult is an attachment whose content is not returned, and why,
// in the bridge's own words: a normal result, not a tool error.
func withheldResult(meta, reason string) *mcp.CallToolResult {
	return textResult(meta + "\ncontent not returned: " + reason + "; the user can open it in Malachi Mail")
}

// pagedText is the result of a text, a text attachment's or a document's:
// meta, the line saying which bytes of the text the slice from offset is
// (limit bytes, by default and at most those of a text attachment), the
// trailing line if any, then the slice in a fence. The text is cleaned.
func pagedText(meta, text string, offset, limit int, trailing string) *mcp.CallToolResult {
	slice, total, end := sliceBytes(text, offset, clampLimit(limit, defaultAttachmentTextBytes, maxAttachmentTextBytes))
	start := end - len(slice)
	if end < total {
		meta += fmt.Sprintf("\ntext: bytes %d-%d of %d (truncated; call again with offset=%d)", start, end, total, end)
	} else {
		meta += fmt.Sprintf("\ntext: bytes %d-%d of %d", start, end, total)
	}
	if trailing != "" {
		meta += "\n" + trailing
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: meta},
		&mcp.TextContent{Text: fenced(newNonce(), slice)},
	}}
}

// readDocument turns the fetched bytes of a document into what is kept of
// it: its cleaned text and facts, or the reason it is withheld. cacheable
// says whether the outcome may be cached (doc_cache.go); cancelled that
// the tool call ended first.
func (b *bridge) readDocument(ctx context.Context, f extract.Format, data []byte) (e docEntry, cacheable, cancelled bool) {
	e.format = f
	if len(data) > maxAttachmentDocumentBytes {
		e.reason = fmt.Sprintf("too big: %d bytes, limit %d", len(data), maxAttachmentDocumentBytes)
		return e, true, false
	}
	if reason := checkDocumentBytes(f, data); reason != "" {
		e.reason = reason
		return e, true, false
	}
	x := b.extractDocument(ctx, f, data)
	switch {
	case x.busy:
		e.reason = workerFailureReason(x)
	case x.outcome == extract.Cancelled:
		return e, false, true
	case x.outcome == extract.Refused:
		e.reason = refusalReason(f, x.refusal)
	case x.outcome == extract.OK:
		// The worker's text is as untrusted as the document: cleaned like
		// any mail text, and withheld when too much of it could not be
		// decoded (fonts without a character map that the reader's own
		// per-page check did not catch).
		text := clean(x.res.Text)
		if bad, runes := strings.Count(text, string(utf8.RuneError)), utf8.RuneCountInString(text); runes > 0 && bad*100 > runes*maxReplacedPercent {
			e.reason = fmt.Sprintf("not text: %d of %d characters could not be decoded", bad, runes)
		} else {
			e.text, e.facts = text, x.res.Facts
		}
	default:
		e.reason = workerFailureReason(x)
	}
	return e, x.cacheable(), false
}

// documentResult is the result of a document from what is kept of it:
// withheld with its reason, or the trusted lines about it (counts and
// flags, worded here) and one page of its text.
func documentResult(meta string, plan attachmentPlan, e docEntry, in getAttachmentIn) *mcp.CallToolResult {
	if e.reason != "" {
		return withheldResult(meta, e.reason)
	}
	meta += "\n" + documentLine(e.format, e.facts, plan.byName)
	if notes := documentNotes(e.format, e.facts); notes != "" {
		meta += "\ndocument-notes: " + notes
	}
	return pagedText(meta, e.text, in.Offset, in.Limit, cutLine(e.facts))
}

// documentLine is the trusted line naming what the text was taken from.
func documentLine(f extract.Format, facts extract.Facts, byName bool) string {
	var b strings.Builder
	b.WriteString("document: ")
	b.WriteString(formatName(f))
	switch f {
	case extract.PDF:
		b.WriteString(", " + plural(facts.Pages, "page", "pages"))
	case extract.XLSX:
		b.WriteString(", " + plural(facts.Sheets, "sheet", "sheets") + ", " + plural(facts.Rows, "row", "rows") + " with content")
	}
	if byName {
		b.WriteString(" (format taken from the file name and confirmed from the content)")
	}
	b.WriteString("; text extracted by the bridge (")
	switch f {
	case extract.PDF:
		b.WriteString("no layout, pictures or OCR")
	case extract.DOCX:
		b.WriteString("no formatting or pictures")
	case extract.XLSX:
		b.WriteString("cell values only, no formatting, pictures or charts")
	}
	b.WriteString(")")
	return b.String()
}

// documentNotes are the trusted notes about the text: what was left out,
// cut or added, from the facts; "" when there are none.
func documentNotes(f extract.Format, facts extract.Facts) string {
	lim := extract.DefaultLimits()
	var notes []string
	add := func(cond bool, format string, args ...any) {
		if cond {
			notes = append(notes, fmt.Sprintf(format, args...))
		}
	}
	switch f {
	case extract.PDF:
		add(facts.PagesWithoutText > 0, "%s without text (scans or pictures)", plural(facts.PagesWithoutText, "page", "pages"))
		add(facts.PagesUndecodable > 0, "%s left out as undecodable", plural(facts.PagesUndecodable, "page", "pages"))
		add(facts.PagesCut > 0, "%s longer than %d bytes of text, cut there", plural(facts.PagesCut, "page", "pages"), lim.MaxPageBytes)
	case extract.DOCX:
		var after []string
		for _, c := range []struct {
			n           int
			one, others string
		}{
			{facts.Comments, "comment", "comments"},
			{facts.Footnotes, "footnote", "footnotes"},
			{facts.Endnotes, "endnote", "endnotes"},
			{facts.HeadersFooters, "header or footer", "headers and footers"},
		} {
			if c.n > 0 {
				after = append(after, plural(c.n, c.one, c.others))
			}
		}
		add(len(after) > 0, "after the body: %s", strings.Join(after, ", "))
		add(facts.TrackedChanges, "tracked changes shown as accepted (deleted text left out)")
	case extract.XLSX:
		add(facts.SheetsHidden > 0, "%s included, marked (hidden)", plural(facts.SheetsHidden, "hidden sheet", "hidden sheets"))
		add(facts.SheetsSkipped > 0, "%s not read", plural(facts.SheetsSkipped, "chart or dialog sheet", "chart or dialog sheets"))
		switch {
		case facts.Formulas > 0 && facts.Uncalculated > 0:
			add(true, "formulas shown as their last calculated value: %s (%d never calculated, shown empty)", plural(facts.Formulas, "cell", "cells"), facts.Uncalculated)
		case facts.Formulas > 0:
			add(true, "formulas shown as their last calculated value: %s", plural(facts.Formulas, "cell", "cells"))
		}
		add(facts.ColumnsDropped > 0, "%s beyond the first %d columns of a row left out", plural(facts.ColumnsDropped, "cell", "cells"), lim.MaxColumns)
		add(facts.CellsUnresolved > 0, "%s naming a shared string that does not exist, shown empty", plural(facts.CellsUnresolved, "cell", "cells"))
		add(facts.Comments > 0, "%s after the sheets", plural(facts.Comments, "cell comment", "cell comments"))
	}
	add(facts.HiddenContent, "hidden content included")
	return strings.Join(notes, "; ")
}

// cutLine is the trusted line saying where a volume cap cut the text, ""
// when none did.
func cutLine(facts extract.Facts) string {
	if !facts.Cut {
		return ""
	}
	lim := extract.DefaultLimits()
	var what string
	switch facts.CutAt {
	case extract.CutTextBytes:
		what = fmt.Sprintf("the text stops at %d bytes", lim.MaxTextBytes)
	case extract.CutPages:
		what = fmt.Sprintf("only the first %d pages are read", lim.MaxPages)
	case extract.CutSheets:
		what = fmt.Sprintf("only the first %d sheets are read", lim.MaxSheets)
	case extract.CutRows:
		what = fmt.Sprintf("a sheet has more than %d rows with content", lim.MaxRowsPerSheet)
	case extract.CutCells:
		what = fmt.Sprintf("the workbook has more than %d cells with content", lim.MaxCells)
	case extract.CutNotes:
		return fmt.Sprintf("cut: there are more than %d comments and notes; the rest of them are not included", lim.MaxNotes)
	default:
		what = "the text is cut"
	}
	var where string
	switch {
	case facts.CutAt == extract.CutTextBytes && facts.CutPage > 0:
		// The text cap stops a PDF inside a page: the lines of that page
		// that fit are in the text.
		return fmt.Sprintf("cut: %s, within page %d; the rest of page %d and the pages after it are not included",
			what, facts.CutPage, facts.CutPage)
	case facts.CutPage > 0:
		where = fmt.Sprintf("page %d", facts.CutPage)
	case facts.CutSheet > 0 && facts.CutRow > 0:
		where = fmt.Sprintf("sheet %d row %d", facts.CutSheet, facts.CutRow)
	case facts.CutSheet > 0:
		where = fmt.Sprintf("sheet %d", facts.CutSheet)
	}
	if where == "" {
		return "cut: " + what + "; the rest is not included"
	}
	return "cut: " + what + "; from " + where + " on it is not included"
}

// plural is n with the noun for its number.
func plural(n int, one, others string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, others)
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
