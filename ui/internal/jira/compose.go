// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/schotek/malachi/backend/pkg/api"
)

// A comment is written in the compose window, in a comment mode for a
// draft with Draft.Comment (draft.create reply on an account with
// api.CapabilityComment): the title names the issue, there are no
// recipients, subject or attachments and no Save Draft (a comment draft
// stays on this computer), the toolbar keeps only CommentFormats, and a
// service-desk issue offers the choice between a reply to the customer
// and an internal note. Sending queues the comment like a message.

// Format is a formatting control of the compose window's toolbar.
type Format string

// The controls.
const (
	FormatBold         Format = "bold"
	FormatItalic       Format = "italic"
	FormatUnderline    Format = "underline"
	FormatCode         Format = "code"
	FormatHeading      Format = "heading" // the paragraph style menu
	FormatAlignment    Format = "alignment"
	FormatBulletList   Format = "bulletList"
	FormatNumberedList Format = "numberedList"
	FormatQuote        Format = "quote"
	FormatLink         Format = "link"
	FormatColour       Format = "colour"
	FormatImage        Format = "image"
	FormatClear        Format = "clear" // removes formatting, so it stays within the others
)

// CommentFormats are the controls of the comment mode, in toolbar order:
// what both Jira Cloud (ADF) and Data Center (wiki markup) keep.
var CommentFormats = []Format{
	FormatBold, FormatItalic, FormatCode, FormatLink,
	FormatBulletList, FormatNumberedList, FormatQuote, FormatClear,
}

// CommentAllows reports a control the comment mode keeps.
func CommentAllows(f Format) bool {
	for _, have := range CommentFormats {
		if have == f {
			return true
		}
	}
	return false
}

// VisibilityOption is one choice of who reads a comment.
type VisibilityOption struct {
	Visibility api.CommentVisibility
	Label      string
}

// VisibilityOptions are the choices of a comment on issue: a reply to the
// customer and an internal note when the issue allows both (a service-desk
// request), none otherwise (the comment is public).
func VisibilityOptions(issue api.IssueInfo, tr Translator) []VisibilityOption {
	if !allowsBoth(issue) {
		return nil
	}
	return []VisibilityOption{
		// TRANSLATORS: a comment on a service-desk request that the customer reads too.
		{Visibility: api.CommentPublic, Label: tr.T("Reply to Customer")},
		// TRANSLATORS: a comment on a service-desk request that only the team reads.
		{Visibility: api.CommentInternal, Label: tr.T("Internal Note")},
	}
}

// allowsBoth reports an issue whose comments may be public or internal:
// its CommentVisibilities are exactly those two.
func allowsBoth(issue api.IssueInfo) bool {
	var public, internal bool
	for _, v := range issue.CommentVisibilities {
		switch v {
		case api.CommentPublic:
			public = true
		case api.CommentInternal:
			internal = true
		default:
			return false
		}
	}
	return public && internal
}

// SelectedVisibility is the visibility the comment mode shows as chosen:
// internal only when the draft asks for it and the issue allows it,
// public otherwise.
func SelectedVisibility(c api.DraftComment) api.CommentVisibility {
	if c.Visibility == api.CommentInternal && allowsBoth(c.Issue) {
		return api.CommentInternal
	}
	return api.CommentPublic
}

// CommentTitle is the compose window's title for a comment on the issue
// with key.
func CommentTitle(key string, tr Translator) string {
	// TRANSLATORS: title of the window that writes a comment; %s is an issue key such as "ITSD-42".
	return fmt.Sprintf(tr.T("Comment on %s"), Clean(key))
}

// CommentWindow is how the compose window presents a comment draft.
type CommentWindow struct {
	Title string
	// Visibilities are the choices shown (none: no choice, the comment is
	// public); Visibility is the chosen one.
	Visibilities []VisibilityOption
	Visibility   api.CommentVisibility
	// Formats are the toolbar controls kept, CommentFormats.
	Formats []Format
}

// CommentCompose returns the comment mode of draft d; false for a draft of
// an e-mail.
func CommentCompose(d api.Draft, tr Translator) (CommentWindow, bool) {
	if d.Comment == nil {
		return CommentWindow{}, false
	}
	return CommentWindow{
		Title:        CommentTitle(d.Comment.Issue.Key, tr),
		Visibilities: VisibilityOptions(d.Comment.Issue, tr),
		Visibility:   SelectedVisibility(*d.Comment),
		Formats:      append([]Format(nil), CommentFormats...),
	}, true
}

// SendProblem is why a comment with the editor's plain text cannot be
// sent; "" when it can. A comment of spaces and invisible characters only
// is empty.
func SendProblem(text string, tr Translator) string {
	blank := strings.TrimFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
	if blank == "" {
		return tr.T("Write a comment first")
	}
	return ""
}

// CommentQueued is the toast after a comment was sent to the outbox.
func CommentQueued(tr Translator) string {
	return tr.T("Comment queued")
}

// ReplyLabel is the label of the Reply action: "Comment" when replying to
// the selected message writes a comment (api.CapabilityComment).
func ReplyLabel(comment bool, tr Translator) string {
	if comment {
		// TRANSLATORS: the Reply action of an issue: writes a comment.
		return tr.T("Comment")
	}
	return tr.T("Reply")
}
