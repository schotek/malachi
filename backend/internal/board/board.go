// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package board holds the rules of the board (docs/api.md §4.13): which
// thread is a case, in which state and why, plus the pure text helpers the
// board's methods need (the verbatim quote check, the cleaning of
// annotation strings, the plain-text excerpts handed to clients and to an
// assistant).
//
// The package is pure: no database, no I/O, no clock. The store gathers a
// Thread, the caller passes the account's Identity and the time, and
// Evaluate returns a Verdict to store. Everything in a Thread except the
// folder roles, the Mine flags and the ids comes from mail and is treated
// as hostile: the rules read headers, structure, folder roles, flags and
// the bulk classification, never the words of a message, with one
// exception — a question mark in the user's own text (them.asked).
package board

import (
	"slices"
	"strings"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// RulesVersion names the rules below. A new value makes the caller
// evaluate every thread again (meta board.rules). 3: the store hands the
// rules the References of each member (a reply known by its References
// alone no longer starts its thread). 4: sanitize.TrimQuotedText also cuts
// at an Outlook header block without a separator line, which changes the
// own text of the user's plain-text messages and what is shaped like a
// forward. 5: a message of the user's to nothing but the user's own
// addresses (Identity.Self, every account's) is a note to self and never
// decides the state, and the store hands the rules each member's Bcc.
const RulesVersion = "5"

// Bulk classification values of messages.bulk the rules distinguish; any
// other non-empty value is bulk mail (api.BulkKind).
const (
	BulkUnclassified = ""     // not classified yet: the member does not count until it is
	BulkNone         = "none" // classified, not bulk
)

// Thread is what the rules read of one thread of an account: every member
// the store holds under the thread id (hidden rows, trash and the like
// included — Evaluate decides what counts), and for a jira account the
// issue.
type Thread struct {
	Members []Member
	// Issue is set for the thread of a jira account (thread id
	// "jira:<issueId>"); nil for mail.
	Issue *Issue
}

// Member is one stored message of a thread.
type Member struct {
	ID         api.MessageID
	FolderID   api.FolderID
	FolderRole api.FolderRole
	// Virtual: the row is a copy in a virtual folder (a jira view); it
	// never counts.
	Virtual bool
	Hidden  bool // messages.hidden
	// Mine: the row is in a folder of role sent or outbox. The caller
	// decides it from the folder role alone, never from From. A row whose
	// Message-ID equals that of a Mine row of the thread is treated as the
	// same message (a twin) by Evaluate.
	Mine bool

	MessageID string // the Message-ID header, "" when absent
	InReplyTo string // the In-Reply-To header's first identifier
	// References are the identifiers of the References header, optional
	// (nil when the caller does not load them). With InReplyTo they tell
	// whether a message starts its thread: it does when it has neither.
	References []string
	From       api.Address   // as parsed; may name the user (spoofed) — never decides Mine
	ReplyTo    []api.Address // the senders a reply goes to besides From; nil when none
	To         []api.Address
	Cc         []api.Address
	// Bcc is known for the user's own messages (the sent copy keeps it);
	// a note to self needs every recipient, Bcc included, to be the
	// user's.
	Bcc     []api.Address
	Subject string

	Date         time.Time // the Date header (forgeable), zero when absent
	InternalDate time.Time // the server's arrival time, zero when unknown
	StoredAt     time.Time // when the daemon stored the row (created_at); never forged by a sender

	Flagged        bool
	Unread         bool
	HasAttachments bool
	// HasMessagePart: the message has a message/rfc822 part (a forward as
	// attachment).
	HasMessagePart bool
	BodyState      string // messages.body_state; part of InputKey
	// Bulk is messages.bulk: BulkUnclassified, BulkNone or a bulk kind.
	// Ignored for Mine members and on jira accounts.
	Bulk string
	// Importance and XPriority are the message's own header fields (the
	// curated headers: the first field of each), "" when absent. Text in
	// the body never counts.
	Importance string
	XPriority  string
	// Text is the stored plain text (messages.text_body). Evaluate reads it
	// only for Mine members (forward shape, question mark); the caller may
	// leave it empty for the others.
	Text string
	// OwnText, when OwnTextSet, is the own text of a message that has an
	// HTML part, derived by the caller from that part (its quoted history
	// cut off by the HTML quote trimming, then turned into plain text).
	// OwnText (the function) then starts from it instead of from Text; Text
	// still decides the forward shape. Only read for Mine members.
	OwnText    string
	OwnTextSet bool

	// Jira: what item of the issue the row is, and who wrote it.
	IssueKind api.IssueItemKind
	AuthorID  string
}

// Issue is what the rules read of the issue behind a jira thread.
type Issue struct {
	StatusCategory api.IssueStatusCategory
	// Closed: the status is one of the account's closedStatuses.
	Closed     bool
	AssigneeID string
	ReporterID string
	Watching   bool
	// Commented: the user wrote a description or comment of the issue,
	// also among items no longer stored as members (issue_items); the
	// members are checked as well.
	Commented bool
}

// Identity is who the user is on an account: the account's address and
// the senders of its sent folders, used only to tell whether a message is
// addressed to the user (never whether it is the user's); the user's known
// correspondents, the addresses the user has written to; and for a jira
// account the user's account id on the site ("" while unknown: no jira
// cases then). Besides, the addresses of every account of the user
// (WithSelf), which tell a note to self.
type Identity struct {
	addresses  map[string]bool
	self       map[string]bool
	known      map[string]bool
	JiraUserID string
}

// MaxSelfAddresses bounds the addresses WithSelf adds; further ones are
// ignored.
const MaxSelfAddresses = 1024

// WithSelf returns id with the addresses of every account of the user
// (each account's own and the senders of its sent folders) as the user's
// for notes to self (Self): a message of the user's to nothing but those
// addresses (note to self), and inbound mail from one of them to nothing
// but them (info.yourNote). They never tell whether mail is addressed to
// the user on this account (Owns).
func (id Identity) WithSelf(addresses []string) Identity {
	id.self = addrSet(addresses, MaxSelfAddresses)
	return id
}

// MaxIdentityAddresses bounds the addresses of an Identity; further ones
// are ignored.
const MaxIdentityAddresses = 64

// MaxKnownCorrespondents bounds the known correspondents of an Identity;
// further ones are ignored, so the caller passes the most recent first.
const MaxKnownCorrespondents = 20_000

// NewIdentity returns the identity of the given jira user id, the user's
// addresses and the user's known correspondents: every address in To or
// Cc of a message in a folder of role sent or outbox of the user's enabled
// mail accounts (all of them, not only this account's), most recent first.
// Addresses are compared case-insensitively, surrounding space ignored,
// empty and over-long ones dropped.
func NewIdentity(jiraUserID string, addresses, known []string) Identity {
	return Identity{
		addresses:  addrSet(addresses, MaxIdentityAddresses),
		known:      addrSet(known, MaxKnownCorrespondents),
		JiraUserID: strings.TrimSpace(jiraUserID),
	}
}

func addrSet(list []string, limit int) map[string]bool {
	set := make(map[string]bool, min(len(list), limit))
	for _, a := range list {
		if len(set) >= limit {
			break
		}
		if a = normAddr(a); a != "" {
			set[a] = true
		}
	}
	return set
}

// Owns reports whether addr is one of the user's addresses.
func (id Identity) Owns(addr string) bool {
	a := normAddr(addr)
	return a != "" && id.addresses[a]
}

// Self reports whether addr is one of the user's addresses on this
// account (Owns) or on any other account of the user (WithSelf).
func (id Identity) Self(addr string) bool {
	a := normAddr(addr)
	return a != "" && (id.addresses[a] || id.self[a])
}

// Knows reports whether addr is one of the user's known correspondents
// (an address the user has written to).
func (id Identity) Knows(addr string) bool {
	a := normAddr(addr)
	return a != "" && id.known[a]
}

// maxAddrBytes bounds an address compared; a longer one is nobody's.
const maxAddrBytes = 320

func normAddr(a string) string {
	if len(a) > maxAddrBytes {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(a))
}

// Verdict is what the rules made of a thread: the rule columns of its case.
// The derived fields (from Subject on) are filled whenever a member counts,
// also with State "" (no case by the rules), so that a case a user state or
// an annotation keeps (api.BoardReasonKept) can still be shown.
type Verdict struct {
	// State is the rules' state; "" = the rules make no case.
	State  api.BoardState
	Reason api.BoardReason // "" with State ""
	// Pending: the newest relevant member waits for the bulk
	// classification; the rules decide nothing yet (State ""). The thread
	// is evaluated again once it is classified; the caller may keep the
	// previous rule columns meanwhile.
	Pending bool

	// Subject, Date, LatestID and ReplyID's fallback are those of the
	// newest counting member the rules decide by: a note to self of the
	// user's (Evaluate) is passed over, unless every counting message is
	// one.
	Subject string      // the newest deciding member's, Re:/Fwd: stripped, cleaned, one line (URLs kept)
	Person  api.Address // the other party (api.BoardCase.Person), cleaned (CleanAddress)
	// Date is when the newest deciding member arrived (Arrival), never
	// later than now; zero when nothing counts.
	Date time.Time
	// NewestInboundAt is the arrival of the newest inbound counting
	// member, zero when none.
	NewestInboundAt time.Time
	LatestID        api.MessageID // the newest deciding member
	ReplyID         api.MessageID // the newest inbound counting member, else LatestID
	ReplyFolderID   api.FolderID  // ReplyID's folder (that of its representative copy)
	Unread          bool          // a counting member is unread
	HasAttachments  bool          // a counting member has attachments
	Count           int           // counting members (copies with one Message-ID count once)
	// Members are the counting members, oldest first, one row per
	// message: the representative copy (the user's copy in a sent/outbox
	// folder; for a message that is not the user's, the copy stored first),
	// without Text and OwnText.
	Members []Member

	// inbound are the inbound counting messages, newest first
	// (NewestInbound).
	inbound []arrival
	// texts are the members whose text Evaluate reads (TextMembers).
	texts []api.MessageID
}

// arrival is an inbound counting message as NewestInbound sees it: when the
// daemon first stored it (the earliest StoredAt of every row of the thread
// with its Message-ID, also rows that do not count, such as a copy in the
// trash), when it arrived (Arrival) and its Message-ID as the
// representative copy carries it.
type arrival struct {
	stored, at time.Time
	id         string
}

// reopenSlack is how much earlier than the done time a message may have
// arrived and still reopen a case (another client moved it in late).
const reopenSlack = 24 * time.Hour

// NewestInbound returns what the store compares to reopen a case marked
// done at doneAt: when the daemon first stored an inbound counting message
// (the earliest StoredAt of its rows, the daemon's clock, not the
// sender's), when it arrived, and its Message-ID ("" when it has none).
//
// While the case is done (doneAt not zero), a message seen reports as
// already a member when the case was marked done is passed over (a copy
// another client moved is stored anew; seen may be nil), and the newest
// message stored after doneAt that arrived no earlier than a day before it
// is taken, so neither a backfill of old mail nor a forged Date reopens the
// case. Otherwise (or with doneAt zero) the newest inbound counting
// message's, which then does not reopen it. Zero times and "" when nothing
// inbound counts. The user's own messages never reopen a case.
func (v Verdict) NewestInbound(doneAt time.Time, seen func(messageID string) bool) (stored, at time.Time, messageID string) {
	if !doneAt.IsZero() {
		for _, a := range v.inbound {
			if seen != nil && seen(a.id) {
				continue
			}
			if a.stored.After(doneAt) && a.at.After(doneAt.Add(-reopenSlack)) {
				return a.stored, a.at, a.id
			}
		}
	}
	if len(v.inbound) > 0 {
		return v.inbound[0].stored, v.inbound[0].at, v.inbound[0].id
	}
	return time.Time{}, time.Time{}, ""
}

// TextMembers returns the ids of the members whose Text and OwnText
// Evaluate reads, newest first; it reads no other member's. They are
// decided without any text, so a caller evaluates a thread without text
// first and loads only these: none unless the newest deciding member
// (notes to self passed over) is the user's (and never on a jira
// account); then that member (its forward shape), and when no inbound
// member counts, the user's newest deciding messages up to the ask scan
// (them.asked).
func (v Verdict) TextMembers() []api.MessageID {
	return slices.Clone(v.texts)
}

// Arrival is when a member arrived as the rules see it: its internal date,
// else its Date header, else when it was stored; never later than when it
// was stored (when known) nor than now.
func Arrival(m Member, now time.Time) time.Time {
	t := m.InternalDate
	if t.IsZero() {
		t = m.Date
	}
	if t.IsZero() {
		t = m.StoredAt
	}
	if !m.StoredAt.IsZero() && t.After(m.StoredAt) {
		t = m.StoredAt
	}
	if t.After(now) {
		t = now
	}
	return t
}
