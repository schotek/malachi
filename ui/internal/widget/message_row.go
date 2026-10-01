// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"fmt"
	"strings"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Message is what a MessageRow displays; a projection of api.MessageSummary.
// A search result adds where it lies (Origin, with the full path and
// account as OriginTooltip) and the matched words of Snippet (Highlights,
// byte ranges into it).
type Message struct {
	From           []api.Address
	Subject        string
	Snippet        string
	Date           time.Time
	Unread         bool
	Flagged        bool
	HasAttachments bool

	Origin        string
	OriginTooltip string
	Highlights    []api.MatchRange

	// Issue is the issue of a message of a Jira account: its key, summary
	// and status, and whether the row is an event (a status or assignee
	// change); nil for a mail message.
	Issue *jira.IssueRow

	// Tag is the neutral pill of a bulk message (bulkmail.Tag): "Bulk",
	// "Mailing List", "Automated"; empty for personal mail.
	Tag string
}

// Thread is what a conversation row displays; a projection of
// api.ThreadSummary over the members of the listed folder.
type Thread struct {
	Participants   []api.Address // newest first, as the daemon sent them
	Subject        string
	Snippet        string
	Date           time.Time
	Count          int // members in the folder
	Unread         int
	Flagged        bool
	HasAttachments bool
	Expanded       bool
	Loading        bool // unfolded, members not answered yet

	// Issue is the issue of a conversation of a Jira account, and the
	// change its latest member stands for when that is an event; nil for
	// mail.
	Issue *jira.IssueRow

	// Tag is the tag of the latest member (bulkmail.Tag).
	Tag string
}

// Start margins of the row content: the plain one (message_row.blp), the
// tighter one of the rows of a grouped list, where the fold arrow (or its
// kept place) sits in front of the avatar, the gap between the two
// (lead_box spacing in the Blueprint), and the indent of an expanded
// member row when no avatar is shown (with avatars a member's text lines
// up with its conversation's, see applyLead).
const (
	rowMarginStart       = 6
	threadRowMarginStart = 2
	leadSpacing          = 4
	memberIndent         = 24
)

// Row geometry for the two list densities.
const (
	avatarSizeComfortable = 40
	avatarSizeCompact     = 28
	rowMarginComfortable  = 8
	rowMarginCompact      = 3
)

// MessageRow is one row of the message list, built from
// data/ui/message_row.blp.
//
// Each row parses its own copy of the builder XML. That is fine for lists
// bounded by the offline window; a Gtk.ListView with a list-item factory
// can replace it later.
type MessageRow struct {
	*gtk.ListBoxRow

	box        *gtk.Box
	avatar     *adw.Avatar
	from       *gtk.Label
	date       *gtk.Label
	origin     *gtk.Label
	subject    *gtk.Label
	preview    *gtk.Label
	unreadDot  *gtk.Box
	attachment *gtk.Image
	star       *gtk.Image
	expander   *gtk.Button
	spinner    *adw.Spinner
	badge      *gtk.Label

	// The issue's parts of a row of a Jira account: the subject line with
	// the key and the pills, and the change of an event row.
	subjectLine  *gtk.Box
	issueKey     *gtk.Label
	statusPill   *gtk.Label
	bulkPill     *gtk.Label
	internalPill *gtk.Label
	event        *gtk.Label

	// thread says the row shows a conversation (the arrow is live), loading
	// that its members are on their way (the spinner instead), reserve that
	// a plain row keeps the arrow's place so every avatar of a grouped list
	// lines up, member that the row is indented under its conversation
	// (without an avatar of its own).
	thread, loading, reserve, member bool
	// showAvatar is the setting; avatarSize the current density's.
	showAvatar bool
	avatarSize int
	// showPreview is the setting; an event row hides the preview whatever
	// it says.
	showPreview bool
	// issue is what the row shows of an issue (nil for mail); eventRow
	// says the row is an event message (not a conversation).
	issue    *jira.IssueRow
	tag      string // bulk tag; never shown on a row of an issue
	eventRow bool
}

// NewMessageRow builds an empty row; call SetMessage or SetThread to fill it.
func NewMessageRow() *MessageRow {
	b := data.Builder("message_row.ui")
	return &MessageRow{
		ListBoxRow: b.GetObject("message_row").Cast().(*gtk.ListBoxRow),
		box:        b.GetObject("content_box").Cast().(*gtk.Box),
		avatar:     b.GetObject("avatar").Cast().(*adw.Avatar),
		from:       b.GetObject("from_label").Cast().(*gtk.Label),
		date:       b.GetObject("date_label").Cast().(*gtk.Label),
		origin:     b.GetObject("origin_label").Cast().(*gtk.Label),
		subject:    b.GetObject("subject_label").Cast().(*gtk.Label),
		preview:    b.GetObject("preview_label").Cast().(*gtk.Label),
		unreadDot:  b.GetObject("unread_dot").Cast().(*gtk.Box),
		attachment: b.GetObject("attachment_icon").Cast().(*gtk.Image),
		star:       b.GetObject("star_icon").Cast().(*gtk.Image),
		expander:   b.GetObject("expander").Cast().(*gtk.Button),
		spinner:    b.GetObject("expander_spinner").Cast().(*adw.Spinner),
		badge:      b.GetObject("count_badge").Cast().(*gtk.Label),

		subjectLine:  b.GetObject("subject_line").Cast().(*gtk.Box),
		issueKey:     b.GetObject("issue_key").Cast().(*gtk.Label),
		statusPill:   b.GetObject("status_pill").Cast().(*gtk.Label),
		bulkPill:     b.GetObject("bulk_pill").Cast().(*gtk.Label),
		internalPill: b.GetObject("internal_pill").Cast().(*gtk.Label),
		event:        b.GetObject("event_label").Cast().(*gtk.Label),

		showAvatar:  true,
		showPreview: true,
		avatarSize:  avatarSizeComfortable,
	}
}

// SetMessage displays m. All strings are untrusted and shown as plain text.
// The first sender is shown; a message without one gets an empty name. A
// row that showed a conversation before loses the badge and the arrow.
func (r *MessageRow) SetMessage(m Message) {
	var from api.Address
	if len(m.From) > 0 {
		from = m.From[0]
	}
	name := DisplayName(from)
	r.avatar.SetText(name)
	r.from.SetText(name)
	r.from.SetTooltipText(FormatAddress(from))
	r.fill(m.Subject, m.Snippet, m.Date, m.Unread, m.Flagged, m.HasAttachments)
	r.preview.SetAttributes(highlightAttrs(m.Snippet, m.Highlights))
	r.origin.SetText(m.Origin)
	r.origin.SetTooltipText(m.OriginTooltip)
	r.origin.SetVisible(m.Origin != "")
	r.badge.SetVisible(false)
	r.RemoveCSSClass("thread-row")
	r.RemoveCSSClass("thread-expanded")
	r.thread, r.loading = false, false
	r.issue = m.Issue
	r.tag = m.Tag
	r.applyLead()
	r.applyIssue()
}

// SetThread displays a conversation: the participants where the sender
// goes, the member count in a badge, the fold arrow (or the spinner while
// the members load). Strings are untrusted, as in SetMessage.
func (r *MessageRow) SetThread(t Thread) {
	var first api.Address
	if len(t.Participants) > 0 {
		first = t.Participants[0]
	}
	r.avatar.SetText(DisplayName(first))
	r.from.SetText(FormatParticipants(t.Participants))
	lines := make([]string, 0, len(t.Participants))
	for _, a := range t.Participants {
		lines = append(lines, FormatAddress(a))
	}
	r.from.SetTooltipText(strings.Join(lines, "\n"))
	r.fill(t.Subject, t.Snippet, t.Date, t.Unread > 0, t.Flagged, t.HasAttachments)
	r.preview.SetAttributes(nil)
	r.origin.SetVisible(false)
	r.badge.SetText(ThreadCountText(t.Count))
	// TRANSLATORS: tooltip of the member count of a conversation row.
	r.badge.SetTooltipText(fmt.Sprintf(i18n.N("%d message", "%d messages", t.Count), t.Count))
	r.badge.SetVisible(t.Count >= 2)
	r.AddCSSClass("thread-row")
	r.thread, r.loading = true, t.Loading
	r.issue = t.Issue
	r.tag = t.Tag
	r.applyLead()
	r.applyIssue()
	r.SetExpanded(t.Expanded)
}

// applyIssue shows the issue of a row of a Jira account over what
// SetMessage or SetThread laid out: the key, the issue's summary for the
// subject ("KEY: Summary" without the key), the status pill and the
// Internal badge. A conversation whose latest member is an event shows the
// change as its preview; an event message row is the actor and the change
// on its first line, without a preview or an unread dot, and under its
// conversation without the subject line either (one line). A mail row
// hides all of it. Every text is the site's, plain.
func (r *MessageRow) applyIssue() {
	issue := r.issue
	r.eventRow = issue != nil && issue.Event && !r.thread
	SetBulkPill(r.bulkPill, r.tag, issue == nil)
	if issue == nil {
		r.issueKey.SetVisible(false)
		r.statusPill.SetVisible(false)
		r.internalPill.SetVisible(false)
		r.event.SetVisible(false)
		r.from.SetHExpand(true)
		r.from.RemoveCSSClass("caption")
		r.subjectLine.SetVisible(true)
		r.preview.SetVisible(r.showPreview)
		return
	}
	r.issueKey.SetText(issue.Key)
	r.issueKey.SetVisible(issue.Key != "")
	if issue.Summary != "" {
		r.subject.SetText(issue.Summary)
	}
	SetStatusPill(r.statusPill, issue.Status, issue.StatusStyle)
	SetInternalPill(r.internalPill, issue.InternalLabel)
	r.internalPill.SetVisible(issue.Internal)
	switch {
	case r.thread:
		if issue.Event && issue.EventText != "" {
			r.preview.SetText(issue.EventText)
			r.preview.SetAttributes(nil)
		}
		r.event.SetVisible(false)
		r.subjectLine.SetVisible(true)
	case r.eventRow:
		// A symbol, not a word, leads the change after its actor.
		r.event.SetText("→ " + issue.EventText)
		r.event.SetTooltipText(issue.EventText)
		r.event.SetVisible(true)
		r.subjectLine.SetVisible(!r.member)
		r.unreadDot.SetVisible(false)
	default:
		r.event.SetVisible(false)
		r.subjectLine.SetVisible(true)
	}
	// The actor of an event gives way to the change and is set like it.
	r.from.SetHExpand(!r.eventRow)
	if r.eventRow {
		r.from.AddCSSClass("caption")
		r.from.RemoveCSSClass("heading")
		r.subject.RemoveCSSClass("heading")
	} else {
		r.from.RemoveCSSClass("caption")
	}
	r.preview.SetVisible(r.showPreview && !r.eventRow)
}

// SetReserveExpander makes a plain row keep the fold arrow's place, so its
// avatar lines up with the conversation rows of a grouped list; off in a
// flat list.
func (r *MessageRow) SetReserveExpander(on bool) {
	r.reserve = on
	r.applyLead()
}

// fill sets the parts a message and a conversation row share.
func (r *MessageRow) fill(subject, snippet string, date time.Time, unread, flagged, attachments bool) {
	r.date.SetText(FormatDate(date, time.Now()))
	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = i18n.T("(No subject)")
	}
	r.subject.SetText(subject)
	r.preview.SetText(snippet)
	r.attachment.SetVisible(attachments)
	r.star.SetVisible(flagged)
	r.unreadDot.SetVisible(unread)
	for _, l := range []*gtk.Label{r.from, r.subject} {
		if unread {
			l.AddCSSClass("heading")
		} else {
			l.RemoveCSSClass("heading")
		}
	}
}

// SetExpanded turns the fold arrow of a conversation row.
func (r *MessageRow) SetExpanded(on bool) {
	if on {
		r.expander.SetIconName("pan-down-symbolic")
		r.expander.SetTooltipText(i18n.T("Collapse"))
		r.AddCSSClass("thread-expanded")
	} else {
		r.expander.SetIconName("pan-end-symbolic")
		r.expander.SetTooltipText(i18n.T("Expand"))
		r.RemoveCSSClass("thread-expanded")
	}
}

// SetMember indents the row as a member of an unfolded conversation.
func (r *MessageRow) SetMember(on bool) {
	r.member = on
	if on {
		r.AddCSSClass("thread-member")
	} else {
		r.RemoveCSSClass("thread-member")
	}
	r.applyLead()
	if r.eventRow {
		r.subjectLine.SetVisible(!on)
	}
}

// applyLead lays the start of the row out: on a conversation row the
// arrow (or the spinner while loading), on a plain row of a grouped list
// the arrow's place kept empty (invisible and inert, so a click reaches
// the row), on a flat list's row nothing. A member row shows no avatar;
// it is indented so that its text lines up with its conversation's (the
// avatar's width and gap), or by a fixed step when avatars are off. The
// start margin follows.
func (r *MessageRow) applyLead() {
	live := r.thread && !r.loading
	r.spinner.SetVisible(r.thread && r.loading)
	r.expander.SetVisible(live || (!r.thread && r.reserve))
	r.expander.SetCanTarget(live)
	r.expander.SetSensitive(live)
	if live {
		r.expander.SetOpacity(1)
	} else {
		r.expander.SetOpacity(0)
		r.expander.SetTooltipText("")
	}
	r.avatar.SetVisible(r.showAvatar && !r.member)
	margin := rowMarginStart
	if r.thread || r.reserve {
		margin = threadRowMarginStart
	}
	if r.member {
		if r.showAvatar {
			margin += r.avatarSize + leadSpacing
		} else {
			margin += memberIndent
		}
	}
	r.box.SetMarginStart(margin)
}

// ConnectExpander runs f when the fold arrow is clicked.
func (r *MessageRow) ConnectExpander(f func()) {
	r.expander.ConnectClicked(f)
}

// SetCompact switches between the comfortable and compact densities.
func (r *MessageRow) SetCompact(compact bool) {
	margin, size := rowMarginComfortable, avatarSizeComfortable
	if compact {
		margin, size = rowMarginCompact, avatarSizeCompact
		r.AddCSSClass("compact")
	} else {
		r.RemoveCSSClass("compact")
	}
	r.box.SetMarginTop(margin)
	r.box.SetMarginBottom(margin)
	r.avatar.SetSize(size)
	r.avatarSize = size
	r.applyLead()
}

// SetShowPreview shows or hides the snippet line (an event row never has
// one).
func (r *MessageRow) SetShowPreview(show bool) {
	r.showPreview = show
	r.preview.SetVisible(show && !r.eventRow)
}

// SetShowAvatar shows or hides the sender avatar (a member row of an
// unfolded conversation never shows one).
func (r *MessageRow) SetShowAvatar(show bool) {
	r.showAvatar = show
	r.applyLead()
}
